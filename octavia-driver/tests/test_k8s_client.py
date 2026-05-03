# Copyright 2025 ArcaLB Authors
# SPDX-License-Identifier: Apache-2.0

"""Unit tests for Kubernetes VirtualIP client helpers."""

import unittest

from octavia_arca_driver import constants
from octavia_arca_driver.k8s_client import VirtualIPClient


class FakeCustomObjectsApi:
    def __init__(self, current):
        self.current = current
        self.patch_body = None

    def get_namespaced_custom_object(self, **_kwargs):
        return self.current

    def patch_namespaced_custom_object(self, **kwargs):
        self.patch_body = kwargs["body"]
        return self.patch_body


class TestVirtualIPClient(unittest.TestCase):
    def test_update_virtualip_sets_null_for_removed_managed_annotations(self):
        api = FakeCustomObjectsApi({
            "metadata": {
                "annotations": {
                    constants.ANNOTATION_LB_ID: "lb-1111",
                    constants.ANNOTATION_LISTENER_ID: "listener-1111",
                    constants.ANNOTATION_POOL_ID: "pool-1111",
                    constants.ANNOTATION_HM_ID: "hm-1111",
                    constants.ANNOTATION_MEMBER_MAP: "{\"member\": \"addr\"}",
                    constants.ANNOTATION_DRAINING_MEMBER_IDS: "[\"member\"]",
                    "example.com/keep-out-of-scope": "value",
                },
            },
        })
        client = VirtualIPClient.__new__(VirtualIPClient)
        client._namespace = "arca-lb-system"
        client._api = api

        client.update_virtualip(
            "vip-1",
            {"address": "203.0.113.10", "port": 80, "protocol": "TCP"},
            annotations={
                constants.ANNOTATION_LB_ID: "lb-1111",
                constants.ANNOTATION_LISTENER_ID: "listener-1111",
            },
        )

        annotations = api.patch_body["metadata"]["annotations"]
        self.assertIsNone(annotations[constants.ANNOTATION_POOL_ID])
        self.assertIsNone(annotations[constants.ANNOTATION_HM_ID])
        self.assertIsNone(annotations[constants.ANNOTATION_MEMBER_MAP])
        self.assertIsNone(
            annotations[constants.ANNOTATION_DRAINING_MEMBER_IDS]
        )
        self.assertNotIn("example.com/keep-out-of-scope", annotations)

    def test_update_virtualip_sets_null_for_removed_health_check(self):
        api = FakeCustomObjectsApi({
            "metadata": {"annotations": {}},
            "spec": {
                "address": "203.0.113.10",
                "port": 80,
                "protocol": "TCP",
                "healthCheck": {
                    "type": "tcp",
                    "tcp": {"port": 80},
                },
            },
        })
        client = VirtualIPClient.__new__(VirtualIPClient)
        client._namespace = "arca-lb-system"
        client._api = api

        client.update_virtualip(
            "vip-1",
            {"address": "203.0.113.10", "port": 80, "protocol": "TCP"},
        )

        spec = api.patch_body["spec"]
        self.assertIsNone(spec["healthCheck"])


if __name__ == "__main__":
    unittest.main()
