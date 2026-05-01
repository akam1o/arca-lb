# Copyright 2025 ArcaLB Authors
# SPDX-License-Identifier: Apache-2.0

"""Octavia provider driver for ArcaLB.

This driver translates Octavia load balancer API operations into
VirtualIP custom resource operations on a Kubernetes cluster where
arca-lb is deployed. Each Octavia Listener maps to one VirtualIP CR.

Mapping:
    Octavia Loadbalancer  →  VirtualIP.spec.address (VIP address)
    Octavia Listener      →  VirtualIP (port + protocol)
    Octavia Pool+Members  →  VirtualIP.spec.backends[]
    Octavia HealthMonitor →  VirtualIP.spec.healthCheck
"""

import logging
import threading

from oslo_config import cfg
from octavia_lib.api.drivers import driver_lib
from octavia_lib.api.drivers import exceptions as driver_exc
from octavia_lib.api.drivers import provider_base as driver_base

from octavia_arca_driver import constants
from octavia_arca_driver.k8s_client import VirtualIPClient, VirtualIPStatusWatcher

LOG = logging.getLogger(__name__)
CONF = cfg.CONF

PROVISIONING_ACTIVE = "ACTIVE"
PROVISIONING_ERROR = "ERROR"
OPERATING_ONLINE = "ONLINE"
OPERATING_DEGRADED = "DEGRADED"
OPERATING_OFFLINE = "OFFLINE"
OPERATING_ERROR = "ERROR"


class ArcaLBDriver(driver_base.ProviderDriver):
    """Octavia provider driver backed by ArcaLB VirtualIP CRDs."""

    name = constants.DRIVER_NAME
    description = constants.DRIVER_DESCRIPTION

    def __init__(self):
        super().__init__()
        constants.register_opts(CONF)
        conf = CONF.driver_arca

        self._k8s = VirtualIPClient(
            kubeconfig_path=conf.kubernetes_config,
            namespace=conf.namespace,
        )
        self._default_encap_type = conf.default_encap_type
        self._default_dscp = conf.default_dscp
        self._driver_lib = driver_lib.DriverLibrary()
        self._loadbalancer_vips = {}
        self._loadbalancer_vips_lock = threading.Lock()

        # Start background status watcher.
        self._status_watcher = VirtualIPStatusWatcher(
            kubeconfig_path=conf.kubernetes_config,
            namespace=conf.namespace,
        )
        self._status_watcher.start(self._on_virtualip_status_change)
        LOG.info("ArcaLB Octavia driver initialized (namespace=%s)",
                 conf.namespace)

    # ------------------------------------------------------------------
    # Loadbalancer operations
    # ------------------------------------------------------------------

    def loadbalancer_create(self, loadbalancer):
        """Handle loadbalancer creation.

        At this stage only the VIP address is known. The actual VirtualIP
        CRD is created later when a Listener is attached, because a
        VirtualIP requires address + port + protocol.

        We validate the VIP address and mark the LB as ACTIVE.
        """
        lb = loadbalancer.to_dict() if hasattr(loadbalancer, 'to_dict') else loadbalancer
        lb_id = lb.get("loadbalancer_id")
        vip_address = lb.get("vip_address")

        if not vip_address:
            raise driver_exc.UnsupportedOptionError(
                user_fault_string="VIP address is required.",
                operator_fault_string="loadbalancer_create called without vip_address",
            )

        self._remember_loadbalancer_vip(lb_id, vip_address)
        LOG.info("Loadbalancer %s created with VIP %s (deferred until listener)",
                 lb_id, vip_address)

    def loadbalancer_delete(self, loadbalancer, cascade=False):
        """Delete all VirtualIP CRDs associated with this loadbalancer."""
        lb = loadbalancer.to_dict() if hasattr(loadbalancer, 'to_dict') else loadbalancer
        lb_id = lb.get("loadbalancer_id")
        vips = self._k8s.find_by_loadbalancer(lb_id)
        for vip in vips:
            name = vip["metadata"]["name"]
            self._k8s.delete_virtualip(name)
        self._forget_loadbalancer_vip(lb_id)
        LOG.info("Deleted %d VirtualIP(s) for loadbalancer %s",
                 len(vips), lb_id)

    def loadbalancer_update(self, old_loadbalancer, new_loadbalancer):
        """Handle loadbalancer update.

        Loadbalancer updates (e.g., admin_state_up change, name change)
        don't affect VirtualIP spec directly.
        """
        lb = new_loadbalancer.to_dict() if hasattr(new_loadbalancer, 'to_dict') else new_loadbalancer
        lb_id = lb.get("loadbalancer_id")
        admin_state = lb.get("admin_state_up")
        vip_address = lb.get("vip_address")
        if vip_address:
            self._remember_loadbalancer_vip(lb_id, vip_address)
        if admin_state is False:
            # Remove all backends to effectively disable.
            vips = self._k8s.find_by_loadbalancer(lb_id)
            for vip in vips:
                name = vip["metadata"]["name"]
                spec = vip.get("spec", {})
                spec["backends"] = []
                self._k8s.update_virtualip(name, spec)
            LOG.info("Loadbalancer %s disabled, cleared backends from %d VIPs",
                     lb_id, len(vips))

    def loadbalancer_failover(self, loadbalancer_id):
        raise driver_exc.UnsupportedOptionError(
            user_fault_string="Failover is not supported by ArcaLB driver.",
            operator_fault_string="ArcaLB uses BGP ECMP for HA, failover is automatic.",
        )

    # ------------------------------------------------------------------
    # Listener operations
    # ------------------------------------------------------------------

    def listener_create(self, listener):
        """Create a VirtualIP CRD for this listener.

        A VirtualIP requires address (from LB) + port + protocol (from listener).
        """
        lst = listener.to_dict() if hasattr(listener, 'to_dict') else listener
        listener_id = lst.get("listener_id")
        lb_id = lst.get("loadbalancer_id")
        protocol = lst.get("protocol", "TCP")
        port = lst.get("protocol_port")

        mapped_protocol = constants.PROTOCOL_MAP.get(protocol)
        if not mapped_protocol:
            raise driver_exc.UnsupportedOptionError(
                user_fault_string=f"Protocol {protocol} is not supported.",
                operator_fault_string=f"Unsupported protocol: {protocol}",
            )

        # We need the VIP address from the loadbalancer. Octavia's normal
        # listener_create payload does not include it, so remember the value
        # from loadbalancer_create and fall back to existing VIPs after a
        # driver restart or for additional listeners on the same LB.
        vip_address = lst.get("vip_address", "")
        if not vip_address:
            vip_address = self._loadbalancer_vip(lb_id)
        if not vip_address:
            # Try to find another VIP for this LB to get the address.
            existing = self._k8s.find_by_loadbalancer(lb_id)
            if existing:
                vip_address = existing[0].get("spec", {}).get("address", "")
        if not vip_address:
            vip_address = self._loadbalancer_vip_from_octavia(lb_id)
        if not vip_address:
            raise driver_exc.UnsupportedOptionError(
                user_fault_string="Cannot determine VIP address for listener.",
                operator_fault_string=(
                    f"No vip_address for listener {listener_id}, lb {lb_id}"
                ),
            )

        # Parse flavor metadata for encap settings.
        flavor = lst.get("flavor") or {}
        encap_type = flavor.get("encap_type", self._default_encap_type)
        dscp = flavor.get("dscp", self._default_dscp)

        name = self._k8s._resource_name(lb_id, listener_id)
        spec = {
            "address": vip_address,
            "port": port,
            "protocol": mapped_protocol,
            "encapType": encap_type,
        }
        if encap_type == "L3DSR" and dscp is not None:
            spec["dscp"] = int(dscp)

        annotations = {
            constants.ANNOTATION_LB_ID: lb_id,
            constants.ANNOTATION_LISTENER_ID: listener_id,
            constants.ANNOTATION_PROJECT_ID: lst.get("project_id", ""),
        }

        self._k8s.create_virtualip(name, spec, annotations=annotations)
        self._remember_loadbalancer_vip(lb_id, vip_address)
        LOG.info("Created VirtualIP %s for listener %s (LB %s)",
                 name, listener_id, lb_id)

    def listener_delete(self, listener):
        """Delete the VirtualIP CRD for this listener."""
        lst = listener.to_dict() if hasattr(listener, 'to_dict') else listener
        listener_id = lst.get("listener_id")
        vip = self._k8s.find_by_listener(listener_id)
        if vip:
            name = vip["metadata"]["name"]
            self._k8s.delete_virtualip(name)
            LOG.info("Deleted VirtualIP %s for listener %s", name, listener_id)

    def listener_update(self, old_listener, new_listener):
        """Update VirtualIP for an updated listener."""
        lst = new_listener.to_dict() if hasattr(new_listener, 'to_dict') else new_listener
        listener_id = lst.get("listener_id")
        vip = self._k8s.find_by_listener(listener_id)
        if not vip:
            LOG.warning("No VirtualIP found for listener %s", listener_id)
            return

        name = vip["metadata"]["name"]
        spec = vip.get("spec", {})

        protocol = lst.get("protocol")
        if protocol:
            mapped = constants.PROTOCOL_MAP.get(protocol)
            if mapped:
                spec["protocol"] = mapped

        port = lst.get("protocol_port")
        if port:
            spec["port"] = port

        admin_state = lst.get("admin_state_up")
        if admin_state is False:
            spec["backends"] = []

        self._k8s.update_virtualip(name, spec)

    # ------------------------------------------------------------------
    # Pool operations
    # ------------------------------------------------------------------

    def pool_create(self, pool):
        """Associate a pool with a listener's VirtualIP.

        The pool itself doesn't change VirtualIP spec, but we store
        the pool→listener mapping via annotation for future member operations.
        """
        p = pool.to_dict() if hasattr(pool, 'to_dict') else pool
        pool_id = p.get("pool_id")
        listener_id = p.get("listener_id")

        if not listener_id:
            # Pool may be associated via loadbalancer_id + default_pool_id.
            LOG.info("Pool %s created without listener_id, no VirtualIP update",
                     pool_id)
            return

        vip = self._k8s.find_by_listener(listener_id)
        if not vip:
            LOG.warning("No VirtualIP found for listener %s (pool %s)",
                        listener_id, pool_id)
            return

        name = vip["metadata"]["name"]
        annotations = vip.get("metadata", {}).get("annotations", {})
        annotations[constants.ANNOTATION_POOL_ID] = pool_id
        self._k8s.update_virtualip(name, vip.get("spec", {}),
                                   annotations=annotations)
        LOG.info("Pool %s associated with VirtualIP %s", pool_id, name)

    def pool_delete(self, pool):
        """Remove pool association and clear backends from VirtualIP."""
        p = pool.to_dict() if hasattr(pool, 'to_dict') else pool
        pool_id = p.get("pool_id")
        vip = self._k8s.find_by_pool(pool_id)
        if not vip:
            return

        name = vip["metadata"]["name"]
        spec = vip.get("spec", {})
        spec["backends"] = []
        spec.pop("healthCheck", None)

        annotations = vip.get("metadata", {}).get("annotations", {})
        annotations.pop(constants.ANNOTATION_POOL_ID, None)
        annotations.pop(constants.ANNOTATION_HM_ID, None)

        self._k8s.update_virtualip(name, spec, annotations=annotations)
        LOG.info("Pool %s removed from VirtualIP %s", pool_id, name)

    def pool_update(self, old_pool, new_pool):
        """Handle pool update (e.g., algorithm change)."""
        # VPP uses Maglev hashing; lb_algorithm changes are acknowledged
        # but cannot change the underlying algorithm.
        p = new_pool.to_dict() if hasattr(new_pool, 'to_dict') else new_pool
        pool_id = p.get("pool_id")
        algorithm = p.get("lb_algorithm", "")
        if algorithm and algorithm != "SOURCE_IP":
            LOG.warning(
                "Pool %s requested lb_algorithm=%s, but ArcaLB uses Maglev "
                "consistent hashing (functionally similar to SOURCE_IP)",
                pool_id, algorithm,
            )

    # ------------------------------------------------------------------
    # Member operations
    # ------------------------------------------------------------------

    def member_create(self, member):
        """Add a backend to the VirtualIP."""
        m = member.to_dict() if hasattr(member, 'to_dict') else member
        pool_id = m.get("pool_id")
        address = m.get("address")
        weight = m.get("weight", 100)

        vip = self._k8s.find_by_pool(pool_id)
        if not vip:
            raise driver_exc.UnsupportedOptionError(
                user_fault_string="Pool not associated with a VirtualIP.",
                operator_fault_string=f"No VirtualIP for pool {pool_id}",
            )

        name = vip["metadata"]["name"]
        spec = vip.get("spec", {})
        backends = spec.get("backends", [])

        # Avoid duplicates.
        for b in backends:
            if b.get("address") == address:
                b["weight"] = min(max(weight, 1), 100)
                break
        else:
            backends.append({
                "address": address,
                "weight": min(max(weight, 1), 100),
            })

        spec["backends"] = backends
        self._refresh_health_check_port(
            spec, pool_id, vip, extra_members=[m]
        )
        self._k8s.update_virtualip(name, spec)
        LOG.info("Added member %s (weight=%d) to VirtualIP %s",
                 address, weight, name)

    def member_delete(self, member):
        """Remove a backend from the VirtualIP."""
        m = member.to_dict() if hasattr(member, 'to_dict') else member
        pool_id = m.get("pool_id")
        address = m.get("address")

        vip = self._k8s.find_by_pool(pool_id)
        if not vip:
            return

        name = vip["metadata"]["name"]
        spec = vip.get("spec", {})
        backends = [b for b in spec.get("backends", [])
                    if b.get("address") != address]
        spec["backends"] = backends
        self._refresh_health_check_port(spec, pool_id, vip)
        self._k8s.update_virtualip(name, spec)
        LOG.info("Removed member %s from VirtualIP %s", address, name)

    def member_update(self, old_member, new_member):
        """Update a backend's weight in the VirtualIP."""
        m = new_member.to_dict() if hasattr(new_member, 'to_dict') else new_member
        pool_id = m.get("pool_id")
        address = m.get("address")
        weight = m.get("weight", 100)

        vip = self._k8s.find_by_pool(pool_id)
        if not vip:
            return

        name = vip["metadata"]["name"]
        spec = vip.get("spec", {})
        for b in spec.get("backends", []):
            if b.get("address") == address:
                b["weight"] = min(max(weight, 1), 100)
                break

        self._refresh_health_check_port(
            spec, pool_id, vip, extra_members=[m]
        )
        self._k8s.update_virtualip(name, spec)

    def member_batch_update(self, pool_id, members):
        """Replace all members of a pool at once."""
        vip = self._k8s.find_by_pool(pool_id)
        if not vip:
            raise driver_exc.UnsupportedOptionError(
                user_fault_string="Pool not associated with a VirtualIP.",
                operator_fault_string=f"No VirtualIP for pool {pool_id}",
            )

        name = vip["metadata"]["name"]
        spec = vip.get("spec", {})
        member_dicts = [
            member.to_dict() if hasattr(member, 'to_dict') else member
            for member in members
        ]
        backends = []
        for m in member_dicts:
            backends.append({
                "address": m.get("address"),
                "weight": min(max(m.get("weight", 100), 1), 100),
            })
        spec["backends"] = backends
        self._refresh_health_check_port(
            spec, pool_id, vip, extra_members=member_dicts
        )
        self._k8s.update_virtualip(name, spec)
        LOG.info("Batch updated %d members for VirtualIP %s",
                 len(backends), name)

    # ------------------------------------------------------------------
    # Health Monitor operations
    # ------------------------------------------------------------------

    def health_monitor_create(self, health_monitor):
        """Create a health check on the VirtualIP."""
        hm = health_monitor.to_dict() if hasattr(health_monitor, 'to_dict') else health_monitor
        pool_id = hm.get("pool_id")
        hm_id = hm.get("healthmonitor_id")

        vip = self._k8s.find_by_pool(pool_id)
        if not vip:
            raise driver_exc.UnsupportedOptionError(
                user_fault_string="Pool not associated with a VirtualIP.",
                operator_fault_string=f"No VirtualIP for pool {pool_id}",
            )

        name = vip["metadata"]["name"]
        spec = vip.get("spec", {})
        spec["healthCheck"] = self._build_health_check(hm, vip)

        annotations = vip.get("metadata", {}).get("annotations", {})
        annotations[constants.ANNOTATION_HM_ID] = hm_id

        self._k8s.update_virtualip(name, spec, annotations=annotations)
        LOG.info("Health monitor %s applied to VirtualIP %s", hm_id, name)

    def health_monitor_delete(self, health_monitor):
        """Remove the health check from the VirtualIP."""
        hm = health_monitor.to_dict() if hasattr(health_monitor, 'to_dict') else health_monitor
        pool_id = hm.get("pool_id")

        vip = self._k8s.find_by_pool(pool_id)
        if not vip:
            return

        name = vip["metadata"]["name"]
        spec = vip.get("spec", {})
        spec.pop("healthCheck", None)

        annotations = vip.get("metadata", {}).get("annotations", {})
        annotations.pop(constants.ANNOTATION_HM_ID, None)

        self._k8s.update_virtualip(name, spec, annotations=annotations)
        LOG.info("Health monitor removed from VirtualIP %s", name)

    def health_monitor_update(self, old_hm, new_hm):
        """Update the health check on the VirtualIP."""
        hm = new_hm.to_dict() if hasattr(new_hm, 'to_dict') else new_hm
        pool_id = hm.get("pool_id")

        vip = self._k8s.find_by_pool(pool_id)
        if not vip:
            return

        name = vip["metadata"]["name"]
        spec = vip.get("spec", {})
        spec["healthCheck"] = self._build_health_check(hm, vip)
        self._k8s.update_virtualip(name, spec)

    # ------------------------------------------------------------------
    # L7Policy / L7Rule — not supported (L4 only)
    # ------------------------------------------------------------------

    def l7policy_create(self, l7policy):
        raise driver_exc.UnsupportedOptionError(
            user_fault_string="L7 policies are not supported. ArcaLB is an L4 load balancer.",
            operator_fault_string="L7 policy not supported by ArcaLB driver",
        )

    def l7policy_delete(self, l7policy):
        raise driver_exc.UnsupportedOptionError(
            user_fault_string="L7 policies are not supported.",
            operator_fault_string="L7 policy not supported by ArcaLB driver",
        )

    def l7policy_update(self, old_l7policy, new_l7policy):
        raise driver_exc.UnsupportedOptionError(
            user_fault_string="L7 policies are not supported.",
            operator_fault_string="L7 policy not supported by ArcaLB driver",
        )

    def l7rule_create(self, l7rule):
        raise driver_exc.UnsupportedOptionError(
            user_fault_string="L7 rules are not supported.",
            operator_fault_string="L7 rule not supported by ArcaLB driver",
        )

    def l7rule_delete(self, l7rule):
        raise driver_exc.UnsupportedOptionError(
            user_fault_string="L7 rules are not supported.",
            operator_fault_string="L7 rule not supported by ArcaLB driver",
        )

    def l7rule_update(self, old_l7rule, new_l7rule):
        raise driver_exc.UnsupportedOptionError(
            user_fault_string="L7 rules are not supported.",
            operator_fault_string="L7 rule not supported by ArcaLB driver",
        )

    # ------------------------------------------------------------------
    # Flavor / Availability Zone capabilities
    # ------------------------------------------------------------------

    def get_supported_flavor_metadata(self):
        return constants.FLAVOR_METADATA

    def validate_flavor(self, flavor_metadata):
        encap = flavor_metadata.get("encap_type")
        if encap and encap not in constants.VALID_ENCAP_TYPES:
            raise driver_exc.UnsupportedOptionError(
                user_fault_string=(
                    f"Invalid encap_type '{encap}'. "
                    f"Supported: {', '.join(sorted(constants.VALID_ENCAP_TYPES))}"
                ),
                operator_fault_string=f"Invalid encap_type: {encap}",
            )
        dscp = flavor_metadata.get("dscp")
        if dscp is not None:
            try:
                dscp_val = int(dscp)
                if not 0 <= dscp_val <= 63:
                    raise ValueError()
            except (ValueError, TypeError):
                raise driver_exc.UnsupportedOptionError(
                    user_fault_string="DSCP must be an integer 0-63.",
                    operator_fault_string=f"Invalid dscp: {dscp}",
                )

    def get_supported_availability_zone_metadata(self):
        return {}

    def validate_availability_zone(self, availability_zone_metadata):
        # No AZ-specific configuration.
        pass

    # ------------------------------------------------------------------
    # Internal helpers
    # ------------------------------------------------------------------

    def _remember_loadbalancer_vip(self, lb_id, vip_address):
        if not lb_id or not vip_address:
            return
        with self._loadbalancer_vips_lock:
            self._loadbalancer_vips[lb_id] = vip_address

    def _forget_loadbalancer_vip(self, lb_id):
        if not lb_id:
            return
        with self._loadbalancer_vips_lock:
            self._loadbalancer_vips.pop(lb_id, None)

    def _loadbalancer_vip(self, lb_id):
        if not lb_id:
            return ""
        with self._loadbalancer_vips_lock:
            return self._loadbalancer_vips.get(lb_id, "")

    def _loadbalancer_vip_from_octavia(self, lb_id):
        if not lb_id:
            return ""
        loadbalancer = self._driver_lib.get_loadbalancer(lb_id)
        if not loadbalancer:
            return ""
        lb = (
            loadbalancer.to_dict()
            if hasattr(loadbalancer, "to_dict")
            else loadbalancer
        )
        vip_address = lb.get("vip_address", "")
        self._remember_loadbalancer_vip(lb_id, vip_address)
        return vip_address

    def _build_health_check(self, hm, vip=None):
        """Convert Octavia HealthMonitor dict to VirtualIP healthCheck spec."""
        hm_type = hm.get("type", "TCP")
        mapped_type = constants.HEALTH_MONITOR_TYPE_MAP.get(
            hm_type, "tcp"
        )

        hc = {
            "type": mapped_type,
            "intervalSeconds": hm.get("delay", 5),
            "timeoutSeconds": hm.get("timeout", 3),
            "riseCount": hm.get("max_retries", 3),
            "fallCount": hm.get("max_retries_down", 2),
        }

        if mapped_type in ("http", "https"):
            http_method = hm.get("http_method", "GET")
            url_path = hm.get("url_path", "/")
            expected_codes = hm.get("expected_codes", "200")
            port = self._resolve_health_check_port(hm, vip)

            # Parse expected_codes string (e.g., "200,201,301-302") to list.
            codes = self._parse_expected_codes(expected_codes)

            hc["http"] = {
                "port": port,
                "path": url_path,
                "method": http_method if http_method in ("GET", "HEAD", "POST") else "GET",
                "expectedCodes": codes,
            }
            host = hm.get("domain_name")
            if host:
                hc["http"]["host"] = host
        elif mapped_type == "tcp":
            port = self._resolve_health_check_port(hm, vip)
            hc["tcp"] = {
                "port": port,
            }

        return hc

    def _refresh_health_check_port(self, spec, pool_id, vip,
                                   extra_members=None):
        """Keep an existing health check pointed at Octavia member ports."""
        hc = spec.get("healthCheck")
        if not hc:
            return

        hc_type = hc.get("type")
        if hc_type not in ("http", "https", "tcp"):
            return

        port = self._resolve_health_check_port(
            {"pool_id": pool_id}, vip, extra_members=extra_members
        )
        if hc_type in ("http", "https"):
            hc.setdefault("http", {})["port"] = port
        else:
            hc.setdefault("tcp", {})["port"] = port

    def _resolve_health_check_port(self, hm, vip=None, extra_members=None):
        """Resolve the single probe port representable by VirtualIP.

        Octavia health monitors do not carry their own port. The probe port is
        member.monitor_port when set, otherwise member.protocol_port. ArcaLB's
        current CRD has one health check port per VIP, so mixed member ports are
        rejected instead of silently probing the wrong target.
        """
        pool_id = hm.get("pool_id")
        member_ports = self._member_probe_ports(pool_id, extra_members)
        if len(member_ports) == 1:
            return next(iter(member_ports))
        if len(member_ports) > 1:
            ports = ", ".join(str(p) for p in sorted(member_ports))
            raise driver_exc.UnsupportedOptionError(
                user_fault_string=(
                    "ArcaLB health checks require all pool members to use "
                    "the same monitor/protocol port."
                ),
                operator_fault_string=(
                    f"Pool {pool_id} has multiple health check ports: {ports}"
                ),
            )

        vip_port = self._vip_port(vip)
        if vip_port is not None:
            return vip_port

        raise driver_exc.UnsupportedOptionError(
            user_fault_string="Cannot determine health monitor probe port.",
            operator_fault_string=(
                f"No member monitor/protocol port or listener port found for "
                f"pool {pool_id}"
            ),
        )

    def _member_probe_ports(self, pool_id, extra_members=None):
        ports = set()
        for member in self._pool_members(pool_id, extra_members):
            port = self._member_probe_port(member)
            if port is not None:
                ports.add(port)
        return ports

    def _pool_members(self, pool_id, extra_members=None):
        members = []
        if extra_members:
            members.extend(extra_members)

        pool = self._pool_from_octavia(pool_id)
        pool_members = pool.get("members") if pool else None
        if pool_members:
            members.extend(pool_members)

        return members

    def _member_probe_port(self, member):
        if isinstance(member, str):
            member = self._member_from_octavia(member)
        else:
            member = self._as_dict(member)

        member_id = member.get("member_id") or member.get("id")
        if (
            self._valid_port(member.get("monitor_port")) is None and
            self._valid_port(member.get("protocol_port")) is None and
            member_id
        ):
            member = self._member_from_octavia(member_id)

        return (
            self._valid_port(member.get("monitor_port")) or
            self._valid_port(member.get("protocol_port"))
        )

    def _pool_from_octavia(self, pool_id):
        if not pool_id:
            return {}
        try:
            return self._as_dict(self._driver_lib.get_pool(pool_id))
        except Exception:
            LOG.exception("Failed to fetch Octavia pool %s", pool_id)
            return {}

    def _member_from_octavia(self, member_id):
        if not member_id:
            return {}
        try:
            return self._as_dict(self._driver_lib.get_member(member_id))
        except Exception:
            LOG.exception("Failed to fetch Octavia member %s", member_id)
            return {}

    @staticmethod
    def _as_dict(value):
        if isinstance(value, dict):
            return value
        if hasattr(value, "to_dict"):
            data = value.to_dict()
            if isinstance(data, dict):
                return data
        return {}

    @staticmethod
    def _valid_port(value):
        try:
            port = int(value)
        except (TypeError, ValueError):
            return None
        if 1 <= port <= 65535:
            return port
        return None

    @classmethod
    def _vip_port(cls, vip):
        if not vip:
            return None
        spec = vip.get("spec", {}) if isinstance(vip, dict) else {}
        return cls._valid_port(spec.get("port"))

    @staticmethod
    def _parse_expected_codes(codes_str):
        """Parse Octavia expected_codes string into a list of integer codes.

        Octavia accepts formats like "200", "200,201", "200-204".
        """
        if not codes_str:
            return [200]
        codes = []
        for part in str(codes_str).split(","):
            part = part.strip()
            if "-" in part:
                try:
                    start, end = part.split("-", 1)
                    codes.extend(range(int(start), int(end) + 1))
                except (ValueError, TypeError):
                    codes.append(200)
            else:
                try:
                    codes.append(int(part))
                except (ValueError, TypeError):
                    codes.append(200)
        return codes or [200]

    def _on_virtualip_status_change(self, event_type, vip_obj):
        """Handle VirtualIP status changes from the K8s watch.

        Pushes operating_status updates back to Octavia.
        """
        if event_type == "DELETED":
            return

        metadata = vip_obj.get("metadata", {})
        annotations = metadata.get("annotations", {})
        status = vip_obj.get("status", {})

        lb_id = annotations.get(constants.ANNOTATION_LB_ID)
        listener_id = annotations.get(constants.ANNOTATION_LISTENER_ID)
        pool_id = annotations.get(constants.ANNOTATION_POOL_ID)
        hm_id = annotations.get(constants.ANNOTATION_HM_ID)
        if not lb_id:
            return

        ready_condition = self._ready_condition(status.get("conditions", []))
        if ready_condition is None:
            LOG.debug(
                "VirtualIP %s has no Ready condition yet; skipping Octavia "
                "status update",
                metadata.get("name"),
            )
            return
        is_ready = ready_condition.get("status") == "True"
        is_no_backends = (
            ready_condition.get("status") == "False" and
            ready_condition.get("reason") == "NoBackends"
        )

        # Build Octavia status update.
        healthy = status.get("healthyBackends", 0)
        total = status.get("totalBackends", 0)
        provisioning_status = (
            PROVISIONING_ACTIVE
            if is_ready or is_no_backends
            else PROVISIONING_ERROR
        )
        operating_status = self._octavia_operating_status(
            is_ready, is_no_backends, healthy, total
        )

        LOG.debug(
            "VirtualIP %s status: ready=%s, healthy=%d/%d -> %s/%s",
            metadata.get("name"), is_ready, healthy, total,
            provisioning_status, operating_status,
        )

        status_update = self._build_octavia_status_update(
            lb_id=lb_id,
            listener_id=listener_id,
            pool_id=pool_id,
            hm_id=hm_id,
            provisioning_status=provisioning_status,
            operating_status=operating_status,
        )
        self._push_octavia_status(status_update, metadata.get("name"))

    @staticmethod
    def _ready_condition(conditions):
        for condition in conditions:
            if condition.get("type") == "Ready":
                return condition
        return None

    @staticmethod
    def _octavia_operating_status(is_ready, is_no_backends, healthy, total):
        if not is_ready:
            if is_no_backends:
                return OPERATING_OFFLINE
            return OPERATING_ERROR
        if total <= 0:
            return OPERATING_OFFLINE
        if healthy >= total:
            return OPERATING_ONLINE
        if healthy > 0:
            return OPERATING_DEGRADED
        return OPERATING_ERROR

    @staticmethod
    def _status_entry(object_id, provisioning_status, operating_status=None):
        entry = {
            "id": object_id,
            "provisioning_status": provisioning_status,
        }
        if operating_status is not None:
            entry["operating_status"] = operating_status
        return entry

    def _build_octavia_status_update(self, lb_id, listener_id, pool_id, hm_id,
                                     provisioning_status, operating_status):
        status_update = {
            "loadbalancers": [
                self._status_entry(
                    lb_id, provisioning_status, operating_status
                )
            ],
            "listeners": [],
            "pools": [],
            "members": [],
            "healthmonitors": [],
            "l7policies": [],
            "l7rules": [],
        }
        if listener_id:
            status_update["listeners"].append(
                self._status_entry(
                    listener_id, provisioning_status, operating_status
                )
            )
        if pool_id:
            status_update["pools"].append(
                self._status_entry(
                    pool_id, provisioning_status, operating_status
                )
            )
        if hm_id:
            # Health monitors only need lifecycle status here. Backend health is
            # reflected by the pool/listener/load balancer operating status.
            status_update["healthmonitors"].append(
                self._status_entry(hm_id, provisioning_status)
            )
        return status_update

    def _push_octavia_status(self, status_update, vip_name):
        try:
            result = self._driver_lib.update_loadbalancer_status(status_update)
        except Exception:
            LOG.exception("Failed to update Octavia status for VirtualIP %s",
                          vip_name)
            return

        if result:
            LOG.warning(
                "Octavia status update for VirtualIP %s returned: %s",
                vip_name, result,
            )
            return

        LOG.info(
            "Updated Octavia status for VirtualIP %s: %s",
            vip_name, status_update,
        )
