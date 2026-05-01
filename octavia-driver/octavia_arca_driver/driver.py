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

import json
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
PROVISIONING_DELETED = "DELETED"
OPERATING_ONLINE = "ONLINE"
OPERATING_DEGRADED = "DEGRADED"
OPERATING_OFFLINE = "OFFLINE"
OPERATING_ERROR = "ERROR"
OPERATING_DRAINING = "DRAINING"
DEFAULT_MEMBER_WEIGHT = 1
OCTAVIA_MEMBER_WEIGHT_DRAINING = 0


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
        self._push_loadbalancer_status(
            lb_id, PROVISIONING_ACTIVE, OPERATING_OFFLINE
        )
        LOG.info("Loadbalancer %s created with VIP %s (deferred until listener)",
                 lb_id, vip_address)

    def loadbalancer_delete(self, loadbalancer, cascade=False):
        """Delete all VirtualIP CRDs associated with this loadbalancer."""
        lb = loadbalancer.to_dict() if hasattr(loadbalancer, 'to_dict') else loadbalancer
        lb_id = lb.get("loadbalancer_id")
        vips = self._k8s.find_by_loadbalancer(lb_id)
        listener_ids, pool_ids, hm_ids, member_ids = (
            self._octavia_ids_from_virtualips(vips)
        )
        for vip in vips:
            name = vip["metadata"]["name"]
            self._k8s.delete_virtualip(name)
        self._forget_loadbalancer_vip(lb_id)
        self._push_resource_delete_status(
            f"loadbalancer/{lb_id}",
            lb_id=lb_id,
            delete_loadbalancer=True,
            deleted_listener_ids=listener_ids,
            deleted_pool_ids=pool_ids,
            deleted_hm_ids=hm_ids,
            deleted_member_ids=member_ids,
        )
        LOG.info("Deleted %d VirtualIP(s) for loadbalancer %s",
                 len(vips), lb_id)

    def loadbalancer_update(self, old_loadbalancer, new_loadbalancer):
        """Handle loadbalancer update.

        Most load balancer updates are Octavia-only metadata. admin_state_up
        toggles are reflected by clearing or restoring VirtualIP backends.
        """
        lb = new_loadbalancer.to_dict() if hasattr(new_loadbalancer, 'to_dict') else new_loadbalancer
        lb_id = lb.get("loadbalancer_id")
        admin_state = lb.get("admin_state_up")
        vip_address = lb.get("vip_address")
        if vip_address:
            self._remember_loadbalancer_vip(lb_id, vip_address)
        vips = self._k8s.find_by_loadbalancer(lb_id)
        if admin_state is False:
            # Remove all backends to effectively disable.
            for vip in vips:
                name = vip["metadata"]["name"]
                spec = vip.get("spec", {})
                spec["backends"] = []
                self._k8s.update_virtualip(name, spec)
            LOG.info("Loadbalancer %s disabled, cleared backends from %d VIPs",
                     lb_id, len(vips))
        elif admin_state is True:
            restored = 0
            for vip in vips:
                try:
                    restored += self._restore_virtualip_backends([vip])
                except Exception:
                    metadata = vip.get("metadata", {})
                    annotations = metadata.get("annotations", {})
                    name = metadata.get("name")
                    listener_id = annotations.get(constants.ANNOTATION_LISTENER_ID)
                    pool_id = annotations.get(constants.ANNOTATION_POOL_ID)
                    error_lb_id = annotations.get(constants.ANNOTATION_LB_ID) or lb_id
                    LOG.exception(
                        "Failed to restore pool %s while enabling loadbalancer %s",
                        pool_id, lb_id,
                    )
                    try:
                        self._push_pool_restore_error_status(
                            name, error_lb_id, listener_id, pool_id
                        )
                    except Exception:
                        LOG.exception(
                            "Failed to push Octavia ERROR status for "
                            "loadbalancer %s", lb_id
                        )
                    raise
            LOG.info("Loadbalancer %s enabled, restored backends on %d VIPs",
                     lb_id, restored)

        self._push_resource_active_status(
            f"loadbalancer/{lb_id}",
            lb_id=lb_id,
        )

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

        mapped_protocol = self._map_listener_protocol(protocol)

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
        pool_id = self._listener_default_pool_id(lst)
        if pool_id:
            annotations[constants.ANNOTATION_POOL_ID] = pool_id

        self._k8s.create_virtualip(name, spec, annotations=annotations)
        self._remember_loadbalancer_vip(lb_id, vip_address)
        if pool_id:
            created_vip = {
                "metadata": {
                    "name": name,
                    "annotations": annotations,
                },
                "spec": spec,
            }
            try:
                self._restore_virtualip_backends([created_vip])
            except Exception:
                LOG.exception(
                    "Failed to restore pool %s while creating listener %s; "
                    "deleting VirtualIP %s",
                    pool_id, listener_id, name,
                )
                try:
                    self._k8s.delete_virtualip(name)
                except Exception:
                    LOG.exception("Failed to delete VirtualIP %s after "
                                  "listener_create restore failure", name)
                try:
                    self._push_pool_restore_error_status(
                        name, lb_id, listener_id, pool_id
                    )
                except Exception:
                    LOG.exception("Failed to push Octavia ERROR status for "
                                  "listener %s", listener_id)
                raise
        LOG.info("Created VirtualIP %s for listener %s (LB %s)",
                 name, listener_id, lb_id)

    def listener_delete(self, listener):
        """Delete the VirtualIP CRD for this listener."""
        lst = listener.to_dict() if hasattr(listener, 'to_dict') else listener
        listener_id = lst.get("listener_id")
        lb_id = lst.get("loadbalancer_id")
        vip = self._k8s.find_by_listener(listener_id)
        if vip:
            annotations = vip.get("metadata", {}).get("annotations", {})
            lb_id = lb_id or annotations.get(constants.ANNOTATION_LB_ID)
            pool_id = annotations.get(constants.ANNOTATION_POOL_ID)
            hm_id = annotations.get(constants.ANNOTATION_HM_ID)
            member_ids = sorted(self._member_map_from_annotations(annotations))
            name = vip["metadata"]["name"]
            self._k8s.delete_virtualip(name)
            self._push_resource_delete_status(
                name,
                lb_id=lb_id,
                deleted_listener_ids=[listener_id],
                deleted_pool_ids=[pool_id],
                deleted_hm_ids=[hm_id],
                deleted_member_ids=member_ids,
            )
            LOG.info("Deleted VirtualIP %s for listener %s", name, listener_id)
        else:
            self._push_resource_delete_status(
                f"listener/{listener_id}",
                lb_id=lb_id,
                deleted_listener_ids=[listener_id],
            )

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
        annotations = vip.get("metadata", {}).get("annotations", {})
        lb_id = annotations.get(constants.ANNOTATION_LB_ID) or lst.get(
            "loadbalancer_id"
        )

        protocol = lst.get("protocol")
        if protocol:
            spec["protocol"] = self._map_listener_protocol(protocol)

        port = lst.get("protocol_port")
        old_port = self._valid_port(spec.get("port"))
        new_port = self._valid_port(port)
        port_changed = port is not None and new_port != old_port
        if port is not None:
            spec["port"] = port

        should_restore_pool = False
        pool_id = self._listener_default_pool_id(lst)
        if pool_id:
            should_restore_pool = (
                annotations.get(constants.ANNOTATION_POOL_ID) != pool_id
            )
            annotations[constants.ANNOTATION_POOL_ID] = pool_id
        elif port_changed and annotations.get(constants.ANNOTATION_POOL_ID):
            should_restore_pool = True

        admin_state = lst.get("admin_state_up")
        if admin_state is False:
            spec["backends"] = []
        elif admin_state is True or should_restore_pool:
            try:
                restored = self._restore_virtualip_backends([vip])
            except Exception:
                error_pool_id = annotations.get(constants.ANNOTATION_POOL_ID)
                LOG.exception(
                    "Failed to restore pool %s while updating listener %s",
                    error_pool_id, listener_id,
                )
                try:
                    self._push_pool_restore_error_status(
                        name, lb_id, listener_id, error_pool_id
                    )
                except Exception:
                    LOG.exception("Failed to push Octavia ERROR status for "
                                  "listener %s", listener_id)
                raise
            if restored:
                self._push_resource_active_status(
                    name,
                    lb_id=lb_id,
                    active_listener_ids=[listener_id],
                    active_pool_ids=[
                        annotations.get(constants.ANNOTATION_POOL_ID)
                    ],
                )
                return

        self._k8s.update_virtualip(name, spec, annotations=annotations)
        self._push_resource_active_status(
            name,
            lb_id=lb_id,
            active_listener_ids=[listener_id],
            active_pool_ids=[annotations.get(constants.ANNOTATION_POOL_ID)],
        )

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
        listener_id = self._pool_listener_id(p)
        lb_id = self._pool_loadbalancer_id(p)

        if not listener_id:
            self._push_resource_active_status(
                f"pool/{pool_id}",
                lb_id=lb_id,
                active_pool_ids=[pool_id],
            )
            LOG.info("Pool %s created without listener_id, deferred VirtualIP "
                     "association", pool_id)
            return

        vip = self._k8s.find_by_listener(listener_id)
        if not vip:
            LOG.warning("No VirtualIP found for listener %s (pool %s)",
                        listener_id, pool_id)
            return

        associated = self._associate_pool_with_virtualip(vip, pool_id)
        if not associated:
            return
        name, associated_lb_id, associated_listener_id = associated
        self._push_resource_active_status(
            name,
            lb_id=associated_lb_id or lb_id,
            active_listener_ids=[associated_listener_id or listener_id],
            active_pool_ids=[pool_id],
        )
        LOG.info("Pool %s associated with VirtualIP %s", pool_id, name)

    def pool_delete(self, pool):
        """Remove pool association and clear backends from VirtualIP."""
        p = pool.to_dict() if hasattr(pool, 'to_dict') else pool
        pool_id = p.get("pool_id")
        vip = self._k8s.find_by_pool(pool_id)
        if not vip:
            self._push_resource_delete_status(
                f"pool/{pool_id}",
                lb_id=self._pool_loadbalancer_id(p),
                active_listener_ids=[self._pool_listener_id(p)],
                deleted_pool_ids=[pool_id],
            )
            return

        name = vip["metadata"]["name"]
        spec = vip.get("spec", {})
        spec["backends"] = []
        spec.pop("healthCheck", None)

        annotations = vip.get("metadata", {}).get("annotations", {})
        lb_id = annotations.get(constants.ANNOTATION_LB_ID)
        listener_id = annotations.get(constants.ANNOTATION_LISTENER_ID)
        hm_id = annotations.get(constants.ANNOTATION_HM_ID)
        deleted_member_ids = sorted(
            self._member_map_from_annotations(annotations)
        )
        annotations.pop(constants.ANNOTATION_POOL_ID, None)
        annotations.pop(constants.ANNOTATION_HM_ID, None)
        annotations.pop(constants.ANNOTATION_MEMBER_MAP, None)

        self._k8s.update_virtualip(name, spec, annotations=annotations)
        self._push_resource_delete_status(
            name,
            lb_id=lb_id,
            active_listener_ids=[listener_id],
            deleted_pool_ids=[pool_id],
            deleted_hm_ids=[hm_id],
            deleted_member_ids=deleted_member_ids,
        )
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
        vip = self._k8s.find_by_pool(pool_id)
        if vip:
            annotations = vip.get("metadata", {}).get("annotations", {})
            self._push_resource_active_status(
                vip.get("metadata", {}).get("name"),
                lb_id=annotations.get(constants.ANNOTATION_LB_ID),
                active_listener_ids=[
                    annotations.get(constants.ANNOTATION_LISTENER_ID)
                ],
                active_pool_ids=[pool_id],
            )
        else:
            self._push_resource_active_status(
                f"pool/{pool_id}",
                lb_id=p.get("loadbalancer_id"),
                active_listener_ids=[p.get("listener_id")],
                active_pool_ids=[pool_id],
            )

    # ------------------------------------------------------------------
    # Member operations
    # ------------------------------------------------------------------

    def member_create(self, member):
        """Add a backend to the VirtualIP."""
        m = member.to_dict() if hasattr(member, 'to_dict') else member
        self._validate_member_supported(m)
        pool_id = m.get("pool_id")
        address = m.get("address")
        weight = self._member_weight(m)
        is_draining = self._member_is_draining(m)
        backend = None if is_draining else self._backend_from_member(m)

        vip = self._k8s.find_by_pool(pool_id)
        if not vip:
            deferred_context = self._deferred_pool_context(pool_id)
            if deferred_context:
                self._push_deferred_member_active_status(
                    pool_id, deferred_context["lb_id"], [m]
                )
                LOG.info("Deferred member %s for listener-less pool %s",
                         address, pool_id)
                return
            raise driver_exc.UnsupportedOptionError(
                user_fault_string="Pool not associated with a VirtualIP.",
                operator_fault_string=f"No VirtualIP for pool {pool_id}",
            )

        name = vip["metadata"]["name"]
        spec = vip.get("spec", {})
        backends = spec.get("backends", [])
        annotations = vip.get("metadata", {}).get("annotations", {})
        self._validate_member_dataplane_port(vip, m)

        backends = self._backends_with_member_state(
            backends, address, backend, is_draining
        )

        spec["backends"] = backends
        self._remember_member_mapping(annotations, m)
        self._set_member_draining_state(annotations, m, is_draining)
        self._refresh_health_check_port(
            spec, pool_id, vip, extra_members=[m]
        )
        self._k8s.update_virtualip(name, spec, annotations=annotations)
        if is_draining:
            LOG.info("Marked member %s as DRAINING on VirtualIP %s",
                     address, name)
        else:
            LOG.info("Added member %s (weight=%d) to VirtualIP %s",
                     address, weight, name)

    def member_delete(self, member):
        """Remove a backend from the VirtualIP."""
        m = member.to_dict() if hasattr(member, 'to_dict') else member
        pool_id = m.get("pool_id")
        address = m.get("address")

        vip = self._k8s.find_by_pool(pool_id)
        if not vip:
            deferred_context = self._deferred_pool_context(pool_id)
            if deferred_context:
                member_id = self._member_id(m)
                self._push_resource_delete_status(
                    f"member/{member_id or address}",
                    lb_id=deferred_context["lb_id"],
                    active_pool_ids=[pool_id],
                    deleted_member_ids=[member_id],
                )
                LOG.info("Deleted deferred member %s from listener-less pool %s",
                         member_id or address, pool_id)
            return

        name = vip["metadata"]["name"]
        spec = vip.get("spec", {})
        annotations = vip.get("metadata", {}).get("annotations", {})
        backends = [b for b in spec.get("backends", [])
                    if b.get("address") != address]
        spec["backends"] = backends
        deleted_member_ids = self._forget_member_mapping(annotations, m)
        member_id = self._member_id(m)
        if member_id and member_id not in deleted_member_ids:
            deleted_member_ids.append(member_id)
        self._discard_draining_member_ids(annotations, deleted_member_ids)
        self._refresh_health_check_port(spec, pool_id, vip)
        self._k8s.update_virtualip(name, spec, annotations=annotations)
        self._push_deleted_member_statuses(vip, deleted_member_ids)
        LOG.info("Removed member %s from VirtualIP %s", address, name)

    def member_update(self, old_member, new_member):
        """Update a backend's weight in the VirtualIP."""
        m = new_member.to_dict() if hasattr(new_member, 'to_dict') else new_member
        self._validate_member_supported(m)
        pool_id = m.get("pool_id")
        address = m.get("address")
        weight = self._member_weight(m)
        is_draining = self._member_is_draining(m)
        backend = None if is_draining else self._backend_from_member(m)

        vip = self._k8s.find_by_pool(pool_id)
        if not vip:
            deferred_context = self._deferred_pool_context(pool_id)
            if deferred_context:
                self._push_deferred_member_active_status(
                    pool_id, deferred_context["lb_id"], [m]
                )
                LOG.info("Deferred member update %s for listener-less pool %s",
                         address, pool_id)
            return

        name = vip["metadata"]["name"]
        spec = vip.get("spec", {})
        annotations = vip.get("metadata", {}).get("annotations", {})
        self._validate_member_dataplane_port(vip, m)
        backends = self._backends_with_member_state(
            spec.get("backends", []), address, backend, is_draining
        )
        spec["backends"] = backends

        self._remember_member_mapping(annotations, m)
        self._set_member_draining_state(annotations, m, is_draining)
        self._refresh_health_check_port(
            spec, pool_id, vip, extra_members=[m]
        )
        self._k8s.update_virtualip(name, spec, annotations=annotations)

    def member_batch_update(self, pool_id, members):
        """Replace all members of a pool at once."""
        vip = self._k8s.find_by_pool(pool_id)
        if not vip:
            deferred_context = self._deferred_pool_context(pool_id)
            if deferred_context:
                member_dicts = [
                    member.to_dict() if hasattr(member, 'to_dict') else member
                    for member in members
                ]
                for m in member_dicts:
                    self._validate_member_supported(m)
                self._push_deferred_member_active_status(
                    pool_id, deferred_context["lb_id"], member_dicts
                )
                LOG.info("Deferred batch update of %d members for "
                         "listener-less pool %s", len(member_dicts), pool_id)
                return
            raise driver_exc.UnsupportedOptionError(
                user_fault_string="Pool not associated with a VirtualIP.",
                operator_fault_string=f"No VirtualIP for pool {pool_id}",
            )

        name = vip["metadata"]["name"]
        spec = vip.get("spec", {})
        annotations = vip.get("metadata", {}).get("annotations", {})
        old_member_map = self._member_map_from_annotations(annotations)
        member_dicts = [
            member.to_dict() if hasattr(member, 'to_dict') else member
            for member in members
        ]
        for m in member_dicts:
            self._validate_member_supported(m)
            self._validate_member_dataplane_port(vip, m)

        backends = []
        new_member_map = {}
        draining_member_ids = set()
        for m in member_dicts:
            is_draining = self._member_is_draining(m)
            if not is_draining:
                backends.append(self._backend_from_member(m))
            member_id = self._member_id(m)
            if member_id and m.get("address"):
                new_member_map[member_id] = m.get("address")
                if is_draining:
                    draining_member_ids.add(member_id)
        spec["backends"] = backends
        self._set_member_map_annotation(annotations, new_member_map)
        self._set_draining_member_ids_annotation(
            annotations, draining_member_ids
        )
        self._refresh_health_check_port(
            spec, pool_id, vip, extra_members=member_dicts
        )
        self._k8s.update_virtualip(name, spec, annotations=annotations)
        self._push_deleted_member_statuses(
            vip, sorted(set(old_member_map) - set(new_member_map))
        )
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
            deferred_context = self._deferred_pool_context(pool_id)
            if deferred_context:
                self._map_health_monitor_type(hm.get("type", "TCP"))
                self._push_resource_active_status(
                    f"healthmonitor/{hm_id}",
                    lb_id=deferred_context["lb_id"],
                    active_pool_ids=[pool_id],
                    active_hm_ids=[hm_id],
                )
                LOG.info("Deferred health monitor %s for listener-less pool %s",
                         hm_id, pool_id)
                return
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
        hm_id = hm.get("healthmonitor_id")

        vip = self._k8s.find_by_pool(pool_id)
        if not vip:
            deferred_context = self._deferred_pool_context(pool_id)
            if deferred_context:
                self._push_resource_delete_status(
                    f"healthmonitor/{hm_id}",
                    lb_id=deferred_context["lb_id"],
                    active_pool_ids=[pool_id],
                    deleted_hm_ids=[hm_id],
                )
                return
            self._push_resource_delete_status(
                f"healthmonitor/{hm_id}",
                deleted_hm_ids=[hm_id],
            )
            return

        name = vip["metadata"]["name"]
        spec = vip.get("spec", {})
        spec.pop("healthCheck", None)

        annotations = vip.get("metadata", {}).get("annotations", {})
        lb_id = annotations.get(constants.ANNOTATION_LB_ID)
        listener_id = annotations.get(constants.ANNOTATION_LISTENER_ID)
        pool_id = annotations.get(constants.ANNOTATION_POOL_ID) or pool_id
        hm_id = annotations.get(constants.ANNOTATION_HM_ID) or hm_id
        annotations.pop(constants.ANNOTATION_HM_ID, None)

        self._k8s.update_virtualip(name, spec, annotations=annotations)
        self._push_resource_delete_status(
            name,
            lb_id=lb_id,
            active_listener_ids=[listener_id],
            active_pool_ids=[pool_id],
            deleted_hm_ids=[hm_id],
        )
        LOG.info("Health monitor removed from VirtualIP %s", name)

    def health_monitor_update(self, old_hm, new_hm):
        """Update the health check on the VirtualIP."""
        hm = new_hm.to_dict() if hasattr(new_hm, 'to_dict') else new_hm
        pool_id = hm.get("pool_id")

        vip = self._k8s.find_by_pool(pool_id)
        if not vip:
            deferred_context = self._deferred_pool_context(pool_id)
            if deferred_context:
                self._map_health_monitor_type(hm.get("type", "TCP"))
                self._push_resource_active_status(
                    f"healthmonitor/{hm.get('healthmonitor_id')}",
                    lb_id=deferred_context["lb_id"],
                    active_pool_ids=[pool_id],
                    active_hm_ids=[hm.get("healthmonitor_id")],
                )
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
                if not 1 <= dscp_val <= 63:
                    raise ValueError()
            except (ValueError, TypeError):
                raise driver_exc.UnsupportedOptionError(
                    user_fault_string="DSCP must be an integer 1-63.",
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

    @staticmethod
    def _map_listener_protocol(protocol):
        mapped_protocol = constants.PROTOCOL_MAP.get(protocol)
        if not mapped_protocol:
            raise driver_exc.UnsupportedOptionError(
                user_fault_string=f"Protocol {protocol} is not supported.",
                operator_fault_string=f"Unsupported protocol: {protocol}",
            )
        return mapped_protocol

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

    def _deferred_pool_context(self, pool_id):
        pool = self._pool_from_octavia(pool_id)
        if not pool or self._pool_listener_id(pool):
            return None

        lb_id = self._pool_loadbalancer_id(pool)
        if not lb_id:
            return None
        return {
            "lb_id": lb_id,
            "pool": pool,
        }

    def _push_deferred_member_active_status(self, pool_id, lb_id, members):
        member_ids = [
            self._member_id(self._as_dict(member))
            for member in members
        ]
        self._push_resource_active_status(
            f"pool/{pool_id}",
            lb_id=lb_id,
            active_pool_ids=[pool_id],
            active_member_ids=member_ids,
        )

    @classmethod
    def _map_health_monitor_type(cls, hm_type):
        mapped_type = constants.HEALTH_MONITOR_TYPE_MAP.get(hm_type)
        if not mapped_type:
            raise driver_exc.UnsupportedOptionError(
                user_fault_string=(
                    f"Health monitor type {hm_type} is not supported by "
                    "ArcaLB."
                ),
                operator_fault_string=(
                    f"Unsupported health monitor type: {hm_type}"
                ),
            )
        return mapped_type

    def _associate_pool_with_virtualip(self, vip, pool_id):
        if not vip or not pool_id:
            return None

        metadata = vip.get("metadata", {})
        name = metadata.get("name")
        annotations = metadata.get("annotations") or {}
        existing_pool_id = annotations.get(constants.ANNOTATION_POOL_ID)
        if existing_pool_id and existing_pool_id != pool_id:
            LOG.warning(
                "VirtualIP %s is already associated with pool %s; "
                "cannot associate pool %s",
                name, existing_pool_id, pool_id,
            )
            return None

        if existing_pool_id != pool_id:
            annotations[constants.ANNOTATION_POOL_ID] = pool_id
            self._k8s.update_virtualip(
                name, vip.get("spec", {}), annotations=annotations
            )

        return (
            name,
            annotations.get(constants.ANNOTATION_LB_ID),
            annotations.get(constants.ANNOTATION_LISTENER_ID),
        )

    @classmethod
    def _pool_listener_id(cls, pool):
        pool = cls._as_dict(pool)
        return (
            pool.get("listener_id") or
            cls._first_object_id(pool.get("listeners"), "listener_id", "id")
        )

    @classmethod
    def _pool_loadbalancer_id(cls, pool):
        pool = cls._as_dict(pool)
        return (
            pool.get("loadbalancer_id") or
            cls._first_object_id(
                pool.get("loadbalancers"), "loadbalancer_id", "id"
            )
        )

    @classmethod
    def _listener_default_pool_id(cls, listener):
        listener = cls._as_dict(listener)
        return (
            listener.get("default_pool_id") or
            cls._object_id(
                listener.get("default_pool"), "pool_id", "id"
            )
        )

    @classmethod
    def _first_object_id(cls, value, *keys):
        if not value:
            return ""
        if isinstance(value, (list, tuple)):
            for item in value:
                object_id = cls._object_id(item, *keys)
                if object_id:
                    return object_id
            return ""
        return cls._object_id(value, *keys)

    @classmethod
    def _object_id(cls, value, *keys):
        if isinstance(value, str):
            return value
        data = cls._as_dict(value)
        for key in keys:
            object_id = data.get(key)
            if object_id:
                return object_id
        return ""

    def _restore_virtualip_backends(self, vips):
        restored = 0
        for vip in vips:
            annotations = vip.get("metadata", {}).get("annotations", {})
            pool_id = annotations.get(constants.ANNOTATION_POOL_ID)
            if not pool_id:
                continue

            pool = self._pool_from_octavia(pool_id)
            members = self._resolved_pool_members(pool_id, pool=pool)
            backends = []
            member_map = {}
            draining_member_ids = set()
            for member in members:
                if not member.get("address"):
                    continue
                self._validate_member_supported(member)
                self._validate_member_dataplane_port(vip, member)
                is_draining = self._member_is_draining(member)
                member_id = self._member_id(member)
                if not is_draining:
                    backends.append(self._backend_from_member(member))
                elif member_id:
                    draining_member_ids.add(member_id)
                if member_id:
                    member_map[member_id] = member.get("address")

            name = vip["metadata"]["name"]
            spec = vip.get("spec", {})
            spec["backends"] = backends
            self._set_member_map_annotation(annotations, member_map)
            self._set_draining_member_ids_annotation(
                annotations, draining_member_ids
            )
            hm, hm_known = self._pool_health_monitor(pool, pool_id)
            if hm:
                spec["healthCheck"] = self._build_health_check(hm, vip)
                hm_id = self._health_monitor_id(hm)
                if hm_id:
                    annotations[constants.ANNOTATION_HM_ID] = hm_id
            elif hm_known:
                spec.pop("healthCheck", None)
                annotations.pop(constants.ANNOTATION_HM_ID, None)
            else:
                self._refresh_health_check_port(
                    spec, pool_id, vip, extra_members=members
                )
            self._k8s.update_virtualip(name, spec, annotations=annotations)
            restored += 1
        return restored

    def _push_pool_restore_error_status(self, vip_name, lb_id, listener_id,
                                        pool_id):
        member_ids, hm_ids = self._pool_dependent_resource_ids(pool_id)
        self._push_resource_error_status(
            vip_name,
            lb_id=lb_id,
            error_listener_ids=[listener_id],
            error_pool_ids=[pool_id],
            error_hm_ids=hm_ids,
            error_member_ids=member_ids,
        )

    def _pool_dependent_resource_ids(self, pool_id):
        pool = self._pool_from_octavia(pool_id)

        member_ids = []
        for member in self._pool_members(pool_id, pool=pool):
            member_id = self._object_id(member, "member_id", "id")
            if member_id:
                member_ids.append(member_id)

        hm_id = ""
        if "healthmonitor" in pool:
            hm_id = self._object_id(
                pool.get("healthmonitor"), "healthmonitor_id", "id"
            )
        if not hm_id:
            hm_id = (
                pool.get("healthmonitor_id") or
                pool.get("health_monitor_id")
            )

        return self._clean_ids(member_ids), self._clean_ids([hm_id])

    def _resolved_pool_members(self, pool_id, pool=None):
        members = []
        for member in self._pool_members(pool_id, pool=pool):
            if isinstance(member, str):
                member = self._member_from_octavia(member)
            else:
                member = self._as_dict(member)
                member_id = self._member_id(member)
                if member_id and self._member_needs_detail_fetch(member):
                    fetched = self._member_from_octavia(member_id)
                    if fetched:
                        member.update(fetched)
            if member:
                members.append(member)
        return members

    def _pool_health_monitor(self, pool, pool_id):
        pool = self._as_dict(pool)
        if not pool:
            return None, False

        if "healthmonitor" in pool:
            hm = pool.get("healthmonitor")
            if not hm:
                return None, True
            hm = self._resolved_health_monitor(hm)
            if hm:
                hm.setdefault("pool_id", pool_id)
            return hm, True

        hm_id = (
            pool.get("healthmonitor_id") or
            pool.get("health_monitor_id")
        )
        if hm_id:
            hm = self._health_monitor_from_octavia(hm_id)
            if hm:
                hm.setdefault("pool_id", pool_id)
            return hm, True

        return None, False

    def _resolved_health_monitor(self, health_monitor):
        if isinstance(health_monitor, str):
            return self._health_monitor_from_octavia(health_monitor)

        hm = self._as_dict(health_monitor)
        hm_id = self._health_monitor_id(hm)
        if hm_id and self._health_monitor_needs_detail_fetch(hm):
            fetched = self._health_monitor_from_octavia(hm_id)
            if fetched:
                hm.update(fetched)
        return hm

    @staticmethod
    def _health_monitor_id(health_monitor):
        return (
            health_monitor.get("healthmonitor_id") or
            health_monitor.get("id")
        )

    @staticmethod
    def _health_monitor_needs_detail_fetch(health_monitor):
        return (
            "type" not in health_monitor or
            "pool_id" not in health_monitor
        )

    def _remember_member_mapping(self, annotations, member):
        member_id = self._member_id(member)
        address = member.get("address")
        if not member_id or not address:
            return

        member_map = self._member_map_from_annotations(annotations)
        member_map[member_id] = address
        self._set_member_map_annotation(annotations, member_map)

    def _set_member_draining_state(self, annotations, member, is_draining):
        member_id = self._member_id(member)
        if not member_id:
            return

        draining_member_ids = self._draining_member_ids_from_annotations(
            annotations
        )
        if is_draining:
            draining_member_ids.add(member_id)
        else:
            draining_member_ids.discard(member_id)
        self._set_draining_member_ids_annotation(
            annotations, draining_member_ids
        )

    def _forget_member_mapping(self, annotations, member):
        member_map = self._member_map_from_annotations(annotations)
        member_id = self._member_id(member)
        address = member.get("address")
        removed = []

        if member_id and member_id in member_map:
            removed.append(member_id)
            member_map.pop(member_id, None)
        elif address:
            for mapped_id, mapped_address in list(member_map.items()):
                if mapped_address == address:
                    removed.append(mapped_id)
                    member_map.pop(mapped_id, None)

        if removed:
            self._set_member_map_annotation(annotations, member_map)
        return removed

    def _discard_draining_member_ids(self, annotations, member_ids):
        draining_member_ids = self._draining_member_ids_from_annotations(
            annotations
        )
        for member_id in member_ids:
            draining_member_ids.discard(member_id)
        self._set_draining_member_ids_annotation(
            annotations, draining_member_ids
        )

    @staticmethod
    def _member_id(member):
        return member.get("member_id") or member.get("id")

    @classmethod
    def _member_needs_detail_fetch(cls, member):
        return (
            not member.get("address") or
            "admin_state_up" not in member or
            "backup" not in member or
            "weight" not in member
        )

    @classmethod
    def _validate_member_supported(cls, member):
        member = cls._as_dict(member)
        backup = member.get("backup")
        if isinstance(backup, str):
            is_backup = backup.strip().lower() in ("true", "1", "yes", "on")
        else:
            is_backup = bool(backup)
        if not is_backup:
            return

        raise driver_exc.UnsupportedOptionError(
            user_fault_string="Backup members are not supported by ArcaLB provider.",
            operator_fault_string=(
                "VirtualIP does not support Octavia backup member failover "
                "semantics for member "
                f"{cls._member_id(member) or member.get('address')}"
            ),
        )

    @classmethod
    def _backends_with_member_state(cls, backends, address, backend,
                                    is_draining):
        next_backends = []
        replaced = False
        for existing_backend in backends:
            if existing_backend.get("address") != address:
                next_backends.append(existing_backend)
                continue
            if not is_draining and not replaced:
                next_backends.append(backend)
                replaced = True

        if not is_draining and not replaced:
            next_backends.append(backend)
        return next_backends

    @classmethod
    def _member_weight(cls, member):
        weight = member.get("weight", DEFAULT_MEMBER_WEIGHT)
        if weight is None:
            return DEFAULT_MEMBER_WEIGHT

        try:
            weight = int(weight)
        except (TypeError, ValueError):
            raise driver_exc.UnsupportedOptionError(
                user_fault_string="Member weight must be an integer.",
                operator_fault_string=(
                    f"Invalid Octavia member weight: {weight}"
                ),
            )

        if weight == OCTAVIA_MEMBER_WEIGHT_DRAINING:
            return weight
        return min(max(weight, 1), 100)

    @classmethod
    def _member_backend_weight(cls, member):
        weight = cls._member_weight(member)
        if weight == OCTAVIA_MEMBER_WEIGHT_DRAINING:
            return DEFAULT_MEMBER_WEIGHT
        return weight

    @staticmethod
    def _member_admin_state_up(member):
        admin_state_up = member.get("admin_state_up")
        if admin_state_up is None:
            return True
        if isinstance(admin_state_up, str):
            return admin_state_up.strip().lower() not in (
                "false", "0", "no", "off"
            )
        return bool(admin_state_up)

    @classmethod
    def _member_is_draining(cls, member):
        member = cls._as_dict(member)
        if not cls._member_admin_state_up(member):
            return True
        return (
            cls._member_weight(member) ==
            OCTAVIA_MEMBER_WEIGHT_DRAINING
        )

    @classmethod
    def _backend_from_member(cls, member):
        backend = {
            "address": member.get("address"),
            "weight": cls._member_backend_weight(member),
        }
        monitor_address = member.get("monitor_address")
        if monitor_address:
            backend["monitorAddress"] = monitor_address
        return backend

    @staticmethod
    def _member_map_from_annotations(annotations):
        raw = annotations.get(constants.ANNOTATION_MEMBER_MAP, "")
        if not raw:
            return {}
        try:
            parsed = json.loads(raw)
        except (TypeError, ValueError):
            LOG.warning("Invalid Octavia member map annotation: %s", raw)
            return {}
        if not isinstance(parsed, dict):
            return {}

        member_map = {}
        for member_id, address in parsed.items():
            if member_id and address:
                member_map[str(member_id)] = str(address)
        return member_map

    @staticmethod
    def _set_member_map_annotation(annotations, member_map):
        if member_map:
            annotations[constants.ANNOTATION_MEMBER_MAP] = json.dumps(
                member_map, sort_keys=True, separators=(",", ":")
            )
        else:
            annotations.pop(constants.ANNOTATION_MEMBER_MAP, None)

    @staticmethod
    def _draining_member_ids_from_annotations(annotations):
        raw = annotations.get(constants.ANNOTATION_DRAINING_MEMBER_IDS, "")
        if not raw:
            return set()
        try:
            parsed = json.loads(raw)
        except (TypeError, ValueError):
            LOG.warning("Invalid Octavia draining member annotation: %s", raw)
            return set()
        if not isinstance(parsed, list):
            return set()
        return {str(member_id) for member_id in parsed if member_id}

    @staticmethod
    def _set_draining_member_ids_annotation(annotations, member_ids):
        member_ids = sorted(str(member_id) for member_id in member_ids
                            if member_id)
        if member_ids:
            annotations[constants.ANNOTATION_DRAINING_MEMBER_IDS] = json.dumps(
                member_ids, sort_keys=True, separators=(",", ":")
            )
        else:
            annotations.pop(constants.ANNOTATION_DRAINING_MEMBER_IDS, None)

    def _build_health_check(self, hm, vip=None):
        """Convert Octavia HealthMonitor dict to VirtualIP healthCheck spec."""
        hm_type = hm.get("type", "TCP")
        mapped_type = self._map_health_monitor_type(hm_type)

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
        elif mapped_type in ("tcp", "tls-hello"):
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
        if hc_type not in ("http", "https", "tcp", "tls-hello"):
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

    def _pool_members(self, pool_id, extra_members=None, pool=None):
        members = []
        if extra_members:
            members.extend(extra_members)

        if pool is None:
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
        ports_missing = (
            self._valid_port(member.get("monitor_port")) is None and
            self._valid_port(member.get("protocol_port")) is None
        )
        state_missing = (
            "admin_state_up" not in member or
            "weight" not in member
        )
        if member_id and (ports_missing or state_missing):
            fetched = self._member_from_octavia(member_id)
            if fetched:
                member.update(fetched)

        if self._member_is_draining(member):
            return None

        return (
            self._valid_port(member.get("monitor_port")) or
            self._valid_port(member.get("protocol_port"))
        )

    def _validate_member_dataplane_port(self, vip, member):
        member = self._as_dict(member)
        protocol_port = self._valid_port(member.get("protocol_port"))
        if protocol_port is None:
            return

        vip_port = self._vip_port(vip)
        if vip_port is None:
            raise driver_exc.UnsupportedOptionError(
                user_fault_string="Cannot determine listener port for member.",
                operator_fault_string=(
                    "VirtualIP spec.port is missing while validating Octavia "
                    f"member {self._member_id(member) or member.get('address')}"
                ),
            )

        if protocol_port != vip_port:
            raise driver_exc.UnsupportedOptionError(
                user_fault_string=(
                    "ArcaLB requires member protocol_port to match the "
                    "listener protocol_port."
                ),
                operator_fault_string=(
                    "ArcaLB maps one VirtualIP port to backend addresses only; "
                    f"member {self._member_id(member) or member.get('address')} "
                    f"has protocol_port={protocol_port}, listener port={vip_port}"
                ),
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

    def _health_monitor_from_octavia(self, healthmonitor_id):
        if not healthmonitor_id:
            return {}
        try:
            return self._as_dict(
                self._driver_lib.get_healthmonitor(healthmonitor_id)
            )
        except Exception:
            LOG.exception("Failed to fetch Octavia health monitor %s",
                          healthmonitor_id)
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
            member_map=self._member_map_from_annotations(annotations),
            draining_member_ids=(
                self._draining_member_ids_from_annotations(annotations)
            ),
            backend_statuses=status.get("backends", []),
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

    @staticmethod
    def _empty_octavia_status_update():
        return {
            "loadbalancers": [],
            "listeners": [],
            "pools": [],
            "members": [],
            "healthmonitors": [],
            "l7policies": [],
            "l7rules": [],
        }

    @staticmethod
    def _clean_ids(object_ids):
        if not object_ids:
            return []

        unique = []
        seen = set()
        for object_id in object_ids:
            if not object_id or object_id in seen:
                continue
            unique.append(object_id)
            seen.add(object_id)
        return unique

    def _extend_status_entries(self, status_update, collection, object_ids,
                               provisioning_status, operating_status=None):
        for object_id in self._clean_ids(object_ids):
            status_update[collection].append(
                self._status_entry(
                    object_id, provisioning_status, operating_status
                )
            )

    def _push_loadbalancer_status(self, lb_id, provisioning_status,
                                  operating_status=None):
        if not lb_id:
            return

        status_update = self._empty_octavia_status_update()
        status_update["loadbalancers"].append(
            self._status_entry(
                lb_id, provisioning_status, operating_status
            )
        )
        self._push_octavia_status(status_update, f"loadbalancer/{lb_id}")

    def _push_resource_active_status(self, vip_name, lb_id=None,
                                     active_listener_ids=None,
                                     active_pool_ids=None,
                                     active_hm_ids=None,
                                     active_member_ids=None):
        status_update = self._empty_octavia_status_update()

        if lb_id:
            status_update["loadbalancers"].append(
                self._status_entry(lb_id, PROVISIONING_ACTIVE)
            )
        self._extend_status_entries(
            status_update, "listeners", active_listener_ids,
            PROVISIONING_ACTIVE,
        )
        self._extend_status_entries(
            status_update, "pools", active_pool_ids,
            PROVISIONING_ACTIVE,
        )
        self._extend_status_entries(
            status_update, "healthmonitors", active_hm_ids,
            PROVISIONING_ACTIVE,
        )
        self._extend_status_entries(
            status_update, "members", active_member_ids,
            PROVISIONING_ACTIVE,
        )

        if any(status_update.values()):
            self._push_octavia_status(status_update, vip_name)

    def _push_resource_error_status(self, vip_name, lb_id=None,
                                    error_listener_ids=None,
                                    error_pool_ids=None,
                                    error_hm_ids=None,
                                    error_member_ids=None):
        status_update = self._empty_octavia_status_update()

        if lb_id:
            status_update["loadbalancers"].append(
                self._status_entry(lb_id, PROVISIONING_ACTIVE)
            )
        self._extend_status_entries(
            status_update, "listeners", error_listener_ids,
            PROVISIONING_ERROR,
        )
        self._extend_status_entries(
            status_update, "pools", error_pool_ids,
            PROVISIONING_ERROR,
        )
        self._extend_status_entries(
            status_update, "healthmonitors", error_hm_ids,
            PROVISIONING_ERROR,
        )
        self._extend_status_entries(
            status_update, "members", error_member_ids,
            PROVISIONING_ERROR,
        )

        if any(status_update.values()):
            self._push_octavia_status(status_update, vip_name)

    def _push_resource_delete_status(self, vip_name, lb_id=None,
                                     delete_loadbalancer=False,
                                     active_listener_ids=None,
                                     active_pool_ids=None,
                                     deleted_listener_ids=None,
                                     deleted_pool_ids=None,
                                     deleted_hm_ids=None,
                                     deleted_member_ids=None):
        status_update = self._empty_octavia_status_update()

        if lb_id:
            lb_status = (
                PROVISIONING_DELETED
                if delete_loadbalancer
                else PROVISIONING_ACTIVE
            )
            status_update["loadbalancers"].append(
                self._status_entry(lb_id, lb_status)
            )

        self._extend_status_entries(
            status_update, "listeners", active_listener_ids,
            PROVISIONING_ACTIVE,
        )
        self._extend_status_entries(
            status_update, "pools", active_pool_ids,
            PROVISIONING_ACTIVE,
        )
        self._extend_status_entries(
            status_update, "listeners", deleted_listener_ids,
            PROVISIONING_DELETED,
        )
        self._extend_status_entries(
            status_update, "pools", deleted_pool_ids,
            PROVISIONING_DELETED,
        )
        self._extend_status_entries(
            status_update, "healthmonitors", deleted_hm_ids,
            PROVISIONING_DELETED,
        )
        self._extend_status_entries(
            status_update, "members", deleted_member_ids,
            PROVISIONING_DELETED,
        )

        if any(status_update.values()):
            self._push_octavia_status(status_update, vip_name)

    def _octavia_ids_from_virtualips(self, vips):
        listener_ids = []
        pool_ids = []
        hm_ids = []
        member_ids = []

        for vip in vips:
            annotations = vip.get("metadata", {}).get("annotations", {})
            listener_ids.append(annotations.get(constants.ANNOTATION_LISTENER_ID))
            pool_ids.append(annotations.get(constants.ANNOTATION_POOL_ID))
            hm_ids.append(annotations.get(constants.ANNOTATION_HM_ID))
            member_ids.extend(
                self._member_map_from_annotations(annotations).keys()
            )

        return (
            self._clean_ids(listener_ids),
            self._clean_ids(pool_ids),
            self._clean_ids(hm_ids),
            self._clean_ids(member_ids),
        )

    def _build_octavia_status_update(self, lb_id, listener_id, pool_id, hm_id,
                                     provisioning_status, operating_status,
                                     member_map=None, backend_statuses=None,
                                     member_statuses=None,
                                     draining_member_ids=None):
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
        if member_statuses:
            status_update["members"].extend(member_statuses)
        elif member_map:
            status_update["members"].extend(
                self._build_member_statuses(
                    member_map, backend_statuses, provisioning_status,
                    operating_status, draining_member_ids,
                )
            )
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

    def _build_member_statuses(self, member_map, backend_statuses,
                               provisioning_status, fallback_operating_status,
                               draining_member_ids=None):
        draining_member_ids = draining_member_ids or set()
        backend_health = {}
        for backend in backend_statuses or []:
            address = backend.get("address")
            if address:
                backend_health[address] = bool(backend.get("healthy"))

        member_statuses = []
        for member_id, address in sorted(member_map.items()):
            if member_id in draining_member_ids:
                operating_status = OPERATING_DRAINING
            else:
                operating_status = self._member_operating_status(
                    address, backend_health, fallback_operating_status
                )
            member_statuses.append(
                self._status_entry(
                    member_id, provisioning_status, operating_status
                )
            )
        return member_statuses

    @staticmethod
    def _member_operating_status(address, backend_health,
                                 fallback_operating_status):
        if address in backend_health:
            return (
                OPERATING_ONLINE
                if backend_health[address]
                else OPERATING_OFFLINE
            )
        if fallback_operating_status == OPERATING_ERROR:
            return OPERATING_ERROR
        if fallback_operating_status == OPERATING_ONLINE:
            return OPERATING_ONLINE
        if fallback_operating_status == OPERATING_OFFLINE:
            return OPERATING_OFFLINE
        return fallback_operating_status

    def _push_deleted_member_statuses(self, vip, member_ids):
        if not member_ids:
            return

        annotations = vip.get("metadata", {}).get("annotations", {})
        lb_id = annotations.get(constants.ANNOTATION_LB_ID)
        if not lb_id:
            return

        member_statuses = [
            self._status_entry(member_id, PROVISIONING_DELETED)
            for member_id in member_ids
        ]
        status_update = self._build_octavia_status_update(
            lb_id=lb_id,
            listener_id=annotations.get(constants.ANNOTATION_LISTENER_ID),
            pool_id=annotations.get(constants.ANNOTATION_POOL_ID),
            hm_id=annotations.get(constants.ANNOTATION_HM_ID),
            provisioning_status=PROVISIONING_ACTIVE,
            operating_status=None,
            member_statuses=member_statuses,
        )
        self._push_octavia_status(
            status_update, vip.get("metadata", {}).get("name")
        )

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
