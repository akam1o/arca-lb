# Copyright 2025 ArcaLB Authors
# SPDX-License-Identifier: Apache-2.0

"""Unit tests for the ArcaLB Octavia provider driver."""

import json
import unittest
from unittest import mock

from octavia_arca_driver import constants
from octavia_arca_driver.driver import ArcaLBDriver


class FakeObj:
    """Minimal object with to_dict support for test data."""

    def __init__(self, data):
        self._data = data

    def to_dict(self):
        return self._data


def _make_vip(name, spec, annotations=None, status=None):
    """Build a fake VirtualIP dict as returned by the K8s API."""
    return {
        "apiVersion": "arca.io/v1alpha1",
        "kind": "VirtualIP",
        "metadata": {
            "name": name,
            "namespace": "arca-system",
            "labels": {
                constants.LABEL_MANAGED_BY: constants.LABEL_MANAGED_BY_VALUE,
            },
            "annotations": annotations or {},
        },
        "spec": spec,
        "status": status or {},
    }


class TestParseExpectedCodes(unittest.TestCase):
    def test_single(self):
        self.assertEqual(ArcaLBDriver._parse_expected_codes("200"), [200])

    def test_multiple(self):
        self.assertEqual(ArcaLBDriver._parse_expected_codes("200,201"), [200, 201])

    def test_range(self):
        self.assertEqual(ArcaLBDriver._parse_expected_codes("200-204"),
                         [200, 201, 202, 203, 204])

    def test_mixed(self):
        self.assertEqual(ArcaLBDriver._parse_expected_codes("200,300-302"),
                         [200, 300, 301, 302])

    def test_empty(self):
        self.assertEqual(ArcaLBDriver._parse_expected_codes(""), [200])

    def test_none(self):
        self.assertEqual(ArcaLBDriver._parse_expected_codes(None), [200])


class TestBuildHealthCheck(unittest.TestCase):
    """Test the _build_health_check helper."""

    @mock.patch("octavia_arca_driver.driver.CONF")
    @mock.patch("octavia_arca_driver.driver.driver_lib.DriverLibrary")
    @mock.patch("octavia_arca_driver.driver.VirtualIPStatusWatcher")
    @mock.patch("octavia_arca_driver.driver.VirtualIPClient")
    def setUp(self, mock_client_cls, mock_watcher_cls, mock_driver_lib_cls,
              mock_conf):
        mock_conf.driver_arca.kubernetes_config = ""
        mock_conf.driver_arca.namespace = "arca-system"
        mock_conf.driver_arca.default_encap_type = "L3DSR"
        mock_conf.driver_arca.default_dscp = 10
        mock_conf.driver_arca.status_sync_interval = 10
        self.mock_driver_lib = mock_driver_lib_cls.return_value
        self.mock_driver_lib.get_pool.return_value = None
        self.mock_driver_lib.get_member.return_value = None
        self.mock_driver_lib.update_loadbalancer_status.return_value = None
        self.driver = ArcaLBDriver()
        self.vip = _make_vip(
            "octavia-bbbbbbbb-aaaaaaaa",
            {"address": "203.0.113.10", "port": 8080, "protocol": "TCP"},
            annotations={constants.ANNOTATION_POOL_ID: "pool-1111"},
        )

    def test_http_monitor(self):
        hm = {
            "type": "HTTP",
            "delay": 10,
            "timeout": 5,
            "max_retries": 3,
            "max_retries_down": 2,
            "http_method": "GET",
            "url_path": "/healthz",
            "expected_codes": "200",
        }
        hc = self.driver._build_health_check(hm, self.vip)
        self.assertEqual(hc["type"], "http")
        self.assertEqual(hc["intervalSeconds"], 10)
        self.assertEqual(hc["timeoutSeconds"], 5)
        self.assertEqual(hc["riseCount"], 3)
        self.assertEqual(hc["fallCount"], 2)
        self.assertEqual(hc["http"]["port"], 8080)
        self.assertEqual(hc["http"]["path"], "/healthz")
        self.assertEqual(hc["http"]["method"], "GET")
        self.assertEqual(hc["http"]["expectedCodes"], [200])

    def test_http_monitor_uses_member_protocol_port(self):
        self.mock_driver_lib.get_pool.return_value = FakeObj({
            "pool_id": "pool-1111",
            "members": [{
                "member_id": "member-1111",
                "address": "10.0.1.1",
                "protocol_port": 9000,
            }],
        })
        hm = {
            "pool_id": "pool-1111",
            "type": "HTTP",
            "delay": 10,
            "timeout": 5,
            "max_retries": 3,
            "max_retries_down": 2,
        }

        hc = self.driver._build_health_check(hm, self.vip)

        self.assertEqual(hc["http"]["port"], 9000)

    def test_http_monitor_prefers_member_monitor_port(self):
        self.mock_driver_lib.get_pool.return_value = FakeObj({
            "pool_id": "pool-1111",
            "members": [{
                "member_id": "member-1111",
                "address": "10.0.1.1",
                "monitor_port": 9001,
                "protocol_port": 9000,
            }],
        })
        hm = {
            "pool_id": "pool-1111",
            "type": "HTTP",
            "delay": 10,
            "timeout": 5,
            "max_retries": 3,
            "max_retries_down": 2,
        }

        hc = self.driver._build_health_check(hm, self.vip)

        self.assertEqual(hc["http"]["port"], 9001)

    def test_http_monitor_rejects_mixed_member_ports(self):
        from octavia_lib.api.drivers import exceptions as driver_exc
        self.mock_driver_lib.get_pool.return_value = FakeObj({
            "pool_id": "pool-1111",
            "members": [
                {"member_id": "member-1111", "protocol_port": 9000},
                {"member_id": "member-2222", "protocol_port": 9001},
            ],
        })
        hm = {
            "pool_id": "pool-1111",
            "type": "HTTP",
            "delay": 10,
            "timeout": 5,
            "max_retries": 3,
            "max_retries_down": 2,
        }

        with self.assertRaises(driver_exc.UnsupportedOptionError):
            self.driver._build_health_check(hm, self.vip)

    def test_tcp_monitor(self):
        hm = {
            "type": "TCP",
            "delay": 5,
            "timeout": 3,
            "max_retries": 2,
        }
        hc = self.driver._build_health_check(hm, self.vip)
        self.assertEqual(hc["type"], "tcp")
        self.assertIn("tcp", hc)
        self.assertEqual(hc["tcp"]["port"], 8080)

    def test_ping_monitor(self):
        hm = {
            "type": "PING",
            "delay": 5,
            "timeout": 3,
            "max_retries": 3,
        }
        hc = self.driver._build_health_check(hm)
        self.assertEqual(hc["type"], "ping")


class TestDriverLifecycle(unittest.TestCase):
    """Test complete Octavia lifecycle operations."""

    @mock.patch("octavia_arca_driver.driver.CONF")
    @mock.patch("octavia_arca_driver.driver.driver_lib.DriverLibrary")
    @mock.patch("octavia_arca_driver.driver.VirtualIPStatusWatcher")
    @mock.patch("octavia_arca_driver.driver.VirtualIPClient")
    def setUp(self, mock_client_cls, mock_watcher_cls, mock_driver_lib_cls,
              mock_conf):
        mock_conf.driver_arca.kubernetes_config = ""
        mock_conf.driver_arca.namespace = "arca-system"
        mock_conf.driver_arca.default_encap_type = "L3DSR"
        mock_conf.driver_arca.default_dscp = 10
        mock_conf.driver_arca.status_sync_interval = 10
        self.mock_k8s = mock_client_cls.return_value
        self.mock_k8s._resource_name.side_effect = (
            lambda lb_id, listener_id: f"octavia-{lb_id[:8]}-{listener_id[:8]}"
        )
        self.mock_driver_lib = mock_driver_lib_cls.return_value
        self.mock_driver_lib.get_pool.return_value = None
        self.mock_driver_lib.get_member.return_value = None
        self.mock_driver_lib.update_loadbalancer_status.return_value = None
        self.driver = ArcaLBDriver()

    def test_listener_create_creates_virtualip(self):
        listener = FakeObj({
            "listener_id": "aaaaaaaa-1111-2222-3333-444444444444",
            "loadbalancer_id": "bbbbbbbb-1111-2222-3333-444444444444",
            "protocol": "TCP",
            "protocol_port": 80,
            "vip_address": "203.0.113.10",
            "project_id": "test-project",
        })

        self.mock_k8s.find_by_loadbalancer.return_value = []
        self.driver.listener_create(listener)

        self.mock_k8s.create_virtualip.assert_called_once()
        args = self.mock_k8s.create_virtualip.call_args
        name = args[0][0]
        spec = args[0][1]
        self.assertIn("octavia-", name)
        self.assertEqual(spec["address"], "203.0.113.10")
        self.assertEqual(spec["port"], 80)
        self.assertEqual(spec["protocol"], "TCP")
        self.assertEqual(spec["encapType"], "L3DSR")
        self.assertEqual(spec["dscp"], 10)

    def test_first_listener_uses_loadbalancer_create_vip(self):
        lb_id = "bbbbbbbb-1111-2222-3333-444444444444"
        listener_id = "aaaaaaaa-1111-2222-3333-444444444444"
        loadbalancer = FakeObj({
            "loadbalancer_id": lb_id,
            "vip_address": "203.0.113.10",
        })
        listener = FakeObj({
            "listener_id": listener_id,
            "loadbalancer_id": lb_id,
            "protocol": "TCP",
            "protocol_port": 80,
            "project_id": "test-project",
        })

        self.mock_k8s.find_by_loadbalancer.return_value = []

        self.driver.loadbalancer_create(loadbalancer)
        self.driver.listener_create(listener)

        self.mock_k8s.create_virtualip.assert_called_once()
        spec = self.mock_k8s.create_virtualip.call_args[0][1]
        self.assertEqual(spec["address"], "203.0.113.10")
        self.mock_k8s.find_by_loadbalancer.assert_not_called()

    def test_first_listener_can_fetch_vip_from_octavia_loadbalancer(self):
        lb_id = "bbbbbbbb-1111-2222-3333-444444444444"
        listener = FakeObj({
            "listener_id": "aaaaaaaa-1111-2222-3333-444444444444",
            "loadbalancer_id": lb_id,
            "protocol": "TCP",
            "protocol_port": 80,
            "project_id": "test-project",
        })

        self.mock_k8s.find_by_loadbalancer.return_value = []
        self.mock_driver_lib.get_loadbalancer.return_value = FakeObj({
            "loadbalancer_id": lb_id,
            "vip_address": "203.0.113.10",
        })

        self.driver.listener_create(listener)

        self.mock_driver_lib.get_loadbalancer.assert_called_once_with(lb_id)
        self.mock_k8s.create_virtualip.assert_called_once()
        spec = self.mock_k8s.create_virtualip.call_args[0][1]
        self.assertEqual(spec["address"], "203.0.113.10")

    def test_member_create_adds_backend(self):
        existing_vip = _make_vip(
            "octavia-bbbbbbbb-aaaaaaaa",
            {"address": "203.0.113.10", "port": 80, "protocol": "TCP",
             "backends": []},
            annotations={
                constants.ANNOTATION_POOL_ID: "pool-1111",
            },
        )
        self.mock_k8s.find_by_pool.return_value = existing_vip

        member = FakeObj({
            "member_id": "member-1111",
            "pool_id": "pool-1111",
            "address": "10.0.1.1",
            "monitor_address": "192.0.2.10",
            "protocol_port": 80,
            "weight": 100,
        })
        self.driver.member_create(member)

        self.mock_k8s.update_virtualip.assert_called_once()
        spec = self.mock_k8s.update_virtualip.call_args[0][1]
        self.assertEqual(len(spec["backends"]), 1)
        self.assertEqual(spec["backends"][0]["address"], "10.0.1.1")
        self.assertEqual(
            spec["backends"][0]["monitorAddress"], "192.0.2.10"
        )
        annotations = self.mock_k8s.update_virtualip.call_args[1]["annotations"]
        self.assertEqual(
            json.loads(annotations[constants.ANNOTATION_MEMBER_MAP]),
            {"member-1111": "10.0.1.1"},
        )

    def test_member_create_refreshes_existing_health_check_port(self):
        existing_vip = _make_vip(
            "octavia-bbbbbbbb-aaaaaaaa",
            {"address": "203.0.113.10", "port": 80, "protocol": "TCP",
             "backends": [],
             "healthCheck": {
                 "type": "http",
                 "intervalSeconds": 10,
                 "timeoutSeconds": 5,
                 "riseCount": 3,
                 "fallCount": 2,
                 "http": {"port": 80, "path": "/healthz"},
             }},
            annotations={constants.ANNOTATION_POOL_ID: "pool-1111"},
        )
        self.mock_k8s.find_by_pool.return_value = existing_vip

        member = FakeObj({
            "pool_id": "pool-1111",
            "address": "10.0.1.1",
            "protocol_port": 80,
            "monitor_port": 8080,
            "weight": 100,
        })
        self.driver.member_create(member)

        spec = self.mock_k8s.update_virtualip.call_args[0][1]
        self.assertEqual(spec["healthCheck"]["http"]["port"], 8080)

    def test_member_create_rejects_protocol_port_mismatch(self):
        from octavia_lib.api.drivers import exceptions as driver_exc
        existing_vip = _make_vip(
            "octavia-bbbbbbbb-aaaaaaaa",
            {"address": "203.0.113.10", "port": 80, "protocol": "TCP",
             "backends": []},
            annotations={constants.ANNOTATION_POOL_ID: "pool-1111"},
        )
        self.mock_k8s.find_by_pool.return_value = existing_vip

        member = FakeObj({
            "member_id": "member-1111",
            "pool_id": "pool-1111",
            "address": "10.0.1.1",
            "protocol_port": 8080,
            "weight": 100,
        })

        with self.assertRaises(driver_exc.UnsupportedOptionError):
            self.driver.member_create(member)

        self.mock_k8s.update_virtualip.assert_not_called()

    def test_member_delete_removes_backend(self):
        existing_vip = _make_vip(
            "octavia-bbbbbbbb-aaaaaaaa",
            {"address": "203.0.113.10", "port": 80, "protocol": "TCP",
             "backends": [{"address": "10.0.1.1", "weight": 100},
                          {"address": "10.0.1.2", "weight": 100}]},
            annotations={constants.ANNOTATION_POOL_ID: "pool-1111"},
        )
        self.mock_k8s.find_by_pool.return_value = existing_vip

        member = FakeObj({
            "member_id": "member-1111",
            "pool_id": "pool-1111",
            "address": "10.0.1.1",
            "protocol_port": 80,
        })
        self.driver.member_delete(member)

        spec = self.mock_k8s.update_virtualip.call_args[0][1]
        self.assertEqual(len(spec["backends"]), 1)
        self.assertEqual(spec["backends"][0]["address"], "10.0.1.2")

    def test_member_delete_reports_deleted_status(self):
        existing_vip = _make_vip(
            "octavia-bbbbbbbb-aaaaaaaa",
            {"address": "203.0.113.10", "port": 80, "protocol": "TCP",
             "backends": [{"address": "10.0.1.1", "weight": 100}]},
            annotations={
                constants.ANNOTATION_LB_ID: "lb-1111",
                constants.ANNOTATION_LISTENER_ID: "listener-1111",
                constants.ANNOTATION_POOL_ID: "pool-1111",
                constants.ANNOTATION_MEMBER_MAP: json.dumps({
                    "member-1111": "10.0.1.1",
                }),
            },
        )
        self.mock_k8s.find_by_pool.return_value = existing_vip

        member = FakeObj({
            "member_id": "member-1111",
            "pool_id": "pool-1111",
            "address": "10.0.1.1",
            "protocol_port": 80,
        })
        self.driver.member_delete(member)

        annotations = self.mock_k8s.update_virtualip.call_args[1]["annotations"]
        self.assertNotIn(constants.ANNOTATION_MEMBER_MAP, annotations)
        status = self.mock_driver_lib.update_loadbalancer_status.call_args[0][0]
        self.assertEqual(status["members"], [{
            "id": "member-1111",
            "provisioning_status": "DELETED",
        }])

    def test_member_update_rejects_protocol_port_mismatch(self):
        from octavia_lib.api.drivers import exceptions as driver_exc
        existing_vip = _make_vip(
            "octavia-bbbbbbbb-aaaaaaaa",
            {"address": "203.0.113.10", "port": 80, "protocol": "TCP",
             "backends": [{"address": "10.0.1.1", "weight": 100}]},
            annotations={constants.ANNOTATION_POOL_ID: "pool-1111"},
        )
        self.mock_k8s.find_by_pool.return_value = existing_vip

        member = FakeObj({
            "member_id": "member-1111",
            "pool_id": "pool-1111",
            "address": "10.0.1.1",
            "protocol_port": 8080,
            "weight": 50,
        })

        with self.assertRaises(driver_exc.UnsupportedOptionError):
            self.driver.member_update(FakeObj({}), member)

        self.mock_k8s.update_virtualip.assert_not_called()

    def test_member_batch_update_rejects_protocol_port_mismatch(self):
        from octavia_lib.api.drivers import exceptions as driver_exc
        existing_vip = _make_vip(
            "octavia-bbbbbbbb-aaaaaaaa",
            {"address": "203.0.113.10", "port": 80, "protocol": "TCP",
             "backends": []},
            annotations={constants.ANNOTATION_POOL_ID: "pool-1111"},
        )
        self.mock_k8s.find_by_pool.return_value = existing_vip

        members = [
            FakeObj({
                "member_id": "member-1111",
                "pool_id": "pool-1111",
                "address": "10.0.1.1",
                "protocol_port": 80,
                "weight": 100,
            }),
            FakeObj({
                "member_id": "member-2222",
                "pool_id": "pool-1111",
                "address": "10.0.1.2",
                "protocol_port": 8080,
                "weight": 100,
            }),
        ]

        with self.assertRaises(driver_exc.UnsupportedOptionError):
            self.driver.member_batch_update("pool-1111", members)

        self.mock_k8s.update_virtualip.assert_not_called()

    def test_member_batch_update_preserves_monitor_address(self):
        existing_vip = _make_vip(
            "octavia-bbbbbbbb-aaaaaaaa",
            {"address": "203.0.113.10", "port": 80, "protocol": "TCP",
             "backends": []},
            annotations={constants.ANNOTATION_POOL_ID: "pool-1111"},
        )
        self.mock_k8s.find_by_pool.return_value = existing_vip

        members = [
            FakeObj({
                "member_id": "member-1111",
                "pool_id": "pool-1111",
                "address": "10.0.1.1",
                "monitor_address": "192.0.2.10",
                "protocol_port": 80,
                "weight": 100,
            }),
        ]

        self.driver.member_batch_update("pool-1111", members)

        spec = self.mock_k8s.update_virtualip.call_args[0][1]
        self.assertEqual(spec["backends"], [{
            "address": "10.0.1.1",
            "monitorAddress": "192.0.2.10",
            "weight": 100,
        }])

    def test_loadbalancer_delete_removes_all_vips(self):
        vips = [
            _make_vip("octavia-bbbbbbbb-aaaaaaaa",
                       {"address": "203.0.113.10", "port": 80}),
            _make_vip("octavia-bbbbbbbb-cccccccc",
                       {"address": "203.0.113.10", "port": 443}),
        ]
        self.mock_k8s.find_by_loadbalancer.return_value = vips

        lb = FakeObj({"loadbalancer_id": "bbbbbbbb-1111-2222-3333-444444444444"})
        self.driver.loadbalancer_delete(lb)

        self.assertEqual(self.mock_k8s.delete_virtualip.call_count, 2)

    def test_health_monitor_create(self):
        existing_vip = _make_vip(
            "octavia-bbbbbbbb-aaaaaaaa",
            {"address": "203.0.113.10", "port": 80, "protocol": "TCP",
             "backends": [{"address": "10.0.1.1", "weight": 100}]},
            annotations={constants.ANNOTATION_POOL_ID: "pool-1111"},
        )
        self.mock_k8s.find_by_pool.return_value = existing_vip

        hm = FakeObj({
            "healthmonitor_id": "hm-1111",
            "pool_id": "pool-1111",
            "type": "HTTP",
            "delay": 10,
            "timeout": 5,
            "max_retries": 3,
            "max_retries_down": 2,
            "http_method": "GET",
            "url_path": "/healthz",
            "expected_codes": "200",
        })
        self.driver.health_monitor_create(hm)

        spec = self.mock_k8s.update_virtualip.call_args[0][1]
        self.assertIn("healthCheck", spec)
        self.assertEqual(spec["healthCheck"]["type"], "http")
        self.assertEqual(spec["healthCheck"]["http"]["port"], 80)

    def test_health_monitor_create_uses_member_protocol_port(self):
        existing_vip = _make_vip(
            "octavia-bbbbbbbb-aaaaaaaa",
            {"address": "203.0.113.10", "port": 80, "protocol": "TCP",
             "backends": [{"address": "10.0.1.1", "weight": 100}]},
            annotations={constants.ANNOTATION_POOL_ID: "pool-1111"},
        )
        self.mock_k8s.find_by_pool.return_value = existing_vip
        self.mock_driver_lib.get_pool.return_value = FakeObj({
            "pool_id": "pool-1111",
            "members": [{
                "member_id": "member-1111",
                "address": "10.0.1.1",
                "protocol_port": 8080,
            }],
        })

        hm = FakeObj({
            "healthmonitor_id": "hm-1111",
            "pool_id": "pool-1111",
            "type": "HTTP",
            "delay": 10,
            "timeout": 5,
            "max_retries": 3,
            "max_retries_down": 2,
            "http_method": "GET",
            "url_path": "/healthz",
            "expected_codes": "200",
        })
        self.driver.health_monitor_create(hm)

        spec = self.mock_k8s.update_virtualip.call_args[0][1]
        self.assertEqual(spec["healthCheck"]["http"]["port"], 8080)

    def test_health_monitor_create_prefers_member_monitor_port(self):
        existing_vip = _make_vip(
            "octavia-bbbbbbbb-aaaaaaaa",
            {"address": "203.0.113.10", "port": 80, "protocol": "TCP",
             "backends": [{"address": "10.0.1.1", "weight": 100}]},
            annotations={constants.ANNOTATION_POOL_ID: "pool-1111"},
        )
        self.mock_k8s.find_by_pool.return_value = existing_vip
        self.mock_driver_lib.get_pool.return_value = FakeObj({
            "pool_id": "pool-1111",
            "members": [{
                "member_id": "member-1111",
                "address": "10.0.1.1",
                "monitor_port": 9090,
                "protocol_port": 8080,
            }],
        })

        hm = FakeObj({
            "healthmonitor_id": "hm-1111",
            "pool_id": "pool-1111",
            "type": "TCP",
            "delay": 10,
            "timeout": 5,
            "max_retries": 3,
            "max_retries_down": 2,
        })
        self.driver.health_monitor_create(hm)

        spec = self.mock_k8s.update_virtualip.call_args[0][1]
        self.assertEqual(spec["healthCheck"]["tcp"]["port"], 9090)

    def test_health_monitor_create_rejects_mixed_member_ports(self):
        from octavia_lib.api.drivers import exceptions as driver_exc
        existing_vip = _make_vip(
            "octavia-bbbbbbbb-aaaaaaaa",
            {"address": "203.0.113.10", "port": 80, "protocol": "TCP",
             "backends": [{"address": "10.0.1.1", "weight": 100},
                          {"address": "10.0.1.2", "weight": 100}]},
            annotations={constants.ANNOTATION_POOL_ID: "pool-1111"},
        )
        self.mock_k8s.find_by_pool.return_value = existing_vip
        self.mock_driver_lib.get_pool.return_value = FakeObj({
            "pool_id": "pool-1111",
            "members": [
                {"member_id": "member-1111", "protocol_port": 8080},
                {"member_id": "member-2222", "protocol_port": 8081},
            ],
        })

        hm = FakeObj({
            "healthmonitor_id": "hm-1111",
            "pool_id": "pool-1111",
            "type": "TCP",
            "delay": 10,
            "timeout": 5,
            "max_retries": 3,
            "max_retries_down": 2,
        })

        with self.assertRaises(driver_exc.UnsupportedOptionError):
            self.driver.health_monitor_create(hm)
        self.mock_k8s.update_virtualip.assert_not_called()

    def test_l7policy_not_supported(self):
        from octavia_lib.api.drivers import exceptions as driver_exc
        with self.assertRaises(driver_exc.UnsupportedOptionError):
            self.driver.l7policy_create(FakeObj({}))

    def test_validate_flavor_valid(self):
        # Should not raise.
        self.driver.validate_flavor({"encap_type": "L3DSR", "dscp": "10"})

    def test_validate_flavor_invalid_encap(self):
        from octavia_lib.api.drivers import exceptions as driver_exc
        with self.assertRaises(driver_exc.UnsupportedOptionError):
            self.driver.validate_flavor({"encap_type": "INVALID"})

    def test_validate_flavor_invalid_dscp(self):
        from octavia_lib.api.drivers import exceptions as driver_exc
        with self.assertRaises(driver_exc.UnsupportedOptionError):
            self.driver.validate_flavor({"dscp": "999"})

    def test_validate_flavor_rejects_zero_dscp(self):
        from octavia_lib.api.drivers import exceptions as driver_exc
        with self.assertRaises(driver_exc.UnsupportedOptionError):
            self.driver.validate_flavor({"encap_type": "L3DSR", "dscp": "0"})

    def test_virtualip_status_change_updates_octavia_status(self):
        vip = _make_vip(
            "octavia-bbbbbbbb-aaaaaaaa",
            {"address": "203.0.113.10", "port": 80, "protocol": "TCP"},
            annotations={
                constants.ANNOTATION_LB_ID: "lb-1111",
                constants.ANNOTATION_LISTENER_ID: "listener-1111",
                constants.ANNOTATION_POOL_ID: "pool-1111",
                constants.ANNOTATION_HM_ID: "hm-1111",
                constants.ANNOTATION_MEMBER_MAP: json.dumps({
                    "member-1111": "10.0.1.1",
                    "member-2222": "10.0.1.2",
                }),
            },
            status={
                "healthyBackends": 2,
                "totalBackends": 2,
                "backends": [
                    {"address": "10.0.1.1", "healthy": True},
                    {"address": "10.0.1.2", "healthy": True},
                ],
                "conditions": [
                    {"type": "Ready", "status": "True"},
                ],
            },
        )

        self.driver._on_virtualip_status_change("MODIFIED", vip)

        self.mock_driver_lib.update_loadbalancer_status.assert_called_once_with({
            "loadbalancers": [{
                "id": "lb-1111",
                "provisioning_status": "ACTIVE",
                "operating_status": "ONLINE",
            }],
            "listeners": [{
                "id": "listener-1111",
                "provisioning_status": "ACTIVE",
                "operating_status": "ONLINE",
            }],
            "pools": [{
                "id": "pool-1111",
                "provisioning_status": "ACTIVE",
                "operating_status": "ONLINE",
            }],
            "members": [
                {
                    "id": "member-1111",
                    "provisioning_status": "ACTIVE",
                    "operating_status": "ONLINE",
                },
                {
                    "id": "member-2222",
                    "provisioning_status": "ACTIVE",
                    "operating_status": "ONLINE",
                },
            ],
            "healthmonitors": [{
                "id": "hm-1111",
                "provisioning_status": "ACTIVE",
            }],
            "l7policies": [],
            "l7rules": [],
        })

    def test_virtualip_status_change_reports_degraded(self):
        vip = _make_vip(
            "octavia-bbbbbbbb-aaaaaaaa",
            {"address": "203.0.113.10", "port": 80, "protocol": "TCP"},
            annotations={
                constants.ANNOTATION_LB_ID: "lb-1111",
                constants.ANNOTATION_LISTENER_ID: "listener-1111",
                constants.ANNOTATION_MEMBER_MAP: json.dumps({
                    "member-1111": "10.0.1.1",
                    "member-2222": "10.0.1.2",
                }),
            },
            status={
                "healthyBackends": 1,
                "totalBackends": 2,
                "backends": [
                    {"address": "10.0.1.1", "healthy": True},
                    {"address": "10.0.1.2", "healthy": False},
                ],
                "conditions": [
                    {"type": "Ready", "status": "True"},
                ],
            },
        )

        self.driver._on_virtualip_status_change("MODIFIED", vip)

        status = self.mock_driver_lib.update_loadbalancer_status.call_args[0][0]
        self.assertEqual(
            status["loadbalancers"][0]["provisioning_status"], "ACTIVE"
        )
        self.assertEqual(
            status["loadbalancers"][0]["operating_status"], "DEGRADED"
        )
        self.assertEqual(status["members"], [
            {
                "id": "member-1111",
                "provisioning_status": "ACTIVE",
                "operating_status": "ONLINE",
            },
            {
                "id": "member-2222",
                "provisioning_status": "ACTIVE",
                "operating_status": "OFFLINE",
            },
        ])

    def test_virtualip_status_change_reports_error_when_not_ready(self):
        vip = _make_vip(
            "octavia-bbbbbbbb-aaaaaaaa",
            {"address": "203.0.113.10", "port": 80, "protocol": "TCP"},
            annotations={
                constants.ANNOTATION_LB_ID: "lb-1111",
            },
            status={
                "healthyBackends": 0,
                "totalBackends": 2,
                "conditions": [
                    {
                        "type": "Ready",
                        "status": "False",
                        "reason": "InvalidSpec",
                    },
                ],
            },
        )

        self.driver._on_virtualip_status_change("MODIFIED", vip)

        status = self.mock_driver_lib.update_loadbalancer_status.call_args[0][0]
        self.assertEqual(
            status["loadbalancers"][0]["provisioning_status"], "ERROR"
        )
        self.assertEqual(
            status["loadbalancers"][0]["operating_status"], "ERROR"
        )

    def test_virtualip_status_change_reports_offline_without_backends(self):
        vip = _make_vip(
            "octavia-bbbbbbbb-aaaaaaaa",
            {"address": "203.0.113.10", "port": 80, "protocol": "TCP"},
            annotations={
                constants.ANNOTATION_LB_ID: "lb-1111",
                constants.ANNOTATION_LISTENER_ID: "listener-1111",
            },
            status={
                "healthyBackends": 0,
                "totalBackends": 0,
                "conditions": [
                    {
                        "type": "Ready",
                        "status": "False",
                        "reason": "NoBackends",
                    },
                ],
            },
        )

        self.driver._on_virtualip_status_change("MODIFIED", vip)

        status = self.mock_driver_lib.update_loadbalancer_status.call_args[0][0]
        self.assertEqual(
            status["loadbalancers"][0]["provisioning_status"], "ACTIVE"
        )
        self.assertEqual(
            status["loadbalancers"][0]["operating_status"], "OFFLINE"
        )

    def test_virtualip_status_change_waits_for_ready_condition(self):
        vip = _make_vip(
            "octavia-bbbbbbbb-aaaaaaaa",
            {"address": "203.0.113.10", "port": 80, "protocol": "TCP"},
            annotations={
                constants.ANNOTATION_LB_ID: "lb-1111",
            },
            status={},
        )

        self.driver._on_virtualip_status_change("ADDED", vip)

        self.mock_driver_lib.update_loadbalancer_status.assert_not_called()

    def test_virtualip_status_change_ignores_unmanaged_vip(self):
        vip = _make_vip(
            "manual-vip",
            {"address": "203.0.113.10", "port": 80, "protocol": "TCP"},
            annotations={},
        )

        self.driver._on_virtualip_status_change("MODIFIED", vip)

        self.mock_driver_lib.update_loadbalancer_status.assert_not_called()


if __name__ == "__main__":
    unittest.main()
