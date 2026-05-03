# Copyright 2025 ArcaLB Authors
# SPDX-License-Identifier: Apache-2.0

"""Unit tests for Kubernetes VirtualIP client helpers."""

import threading
import unittest

from octavia_arca_driver import constants
from octavia_arca_driver.k8s_client import (
    VirtualIPClient,
    VirtualIPStatusWatcher,
)


class FakeCustomObjectsApi:
    def __init__(self, current):
        self.current = current
        self.patch_body = None

    def get_namespaced_custom_object(self, **_kwargs):
        return self.current

    def patch_namespaced_custom_object(self, **kwargs):
        self.patch_body = kwargs["body"]
        return self.patch_body


class FakeWatch:
    def __init__(self):
        self.stopped = False

    def stop(self):
        self.stopped = True


class FakeThread:
    def __init__(self, alive):
        self.alive = alive
        self.join_timeout = None

    def join(self, timeout=None):
        self.join_timeout = timeout

    def is_alive(self):
        return self.alive


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
                "dscp": 10,
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
        self.assertIsNone(spec["dscp"])

    def test_update_virtualip_includes_resource_version_precondition(self):
        api = FakeCustomObjectsApi({
            "metadata": {"annotations": {}},
        })
        client = VirtualIPClient.__new__(VirtualIPClient)
        client._namespace = "arca-lb-system"
        client._api = api

        client.update_virtualip(
            "vip-1",
            {"address": "203.0.113.10", "port": 80, "protocol": "TCP"},
            resource_version="42",
        )

        metadata = api.patch_body["metadata"]
        self.assertEqual(metadata["resourceVersion"], "42")

    def test_resource_name_uses_full_id_entropy(self):
        client = VirtualIPClient.__new__(VirtualIPClient)

        first = client._resource_name(
            "12345678-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
            "87654321-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
        )
        second = client._resource_name(
            "12345678-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
            "87654321-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
        )

        self.assertNotEqual(first, second)
        self.assertTrue(first.startswith("octavia-"))
        self.assertLessEqual(len(first), 63)


class TestVirtualIPStatusWatcher(unittest.TestCase):
    def _watcher_with_thread(self, thread):
        watcher = VirtualIPStatusWatcher.__new__(VirtualIPStatusWatcher)
        watcher._stop_event = threading.Event()
        watcher._thread = thread
        watcher._watch = FakeWatch()
        watcher._watch_lock = threading.Lock()
        return watcher

    def test_stop_stops_active_watch_and_keeps_live_thread(self):
        thread = FakeThread(alive=True)
        watcher = self._watcher_with_thread(thread)
        watch = watcher._watch

        watcher.stop()

        self.assertTrue(watcher._stop_event.is_set())
        self.assertTrue(watch.stopped)
        self.assertEqual(thread.join_timeout, 5)
        self.assertIs(watcher._thread, thread)

    def test_stop_clears_finished_thread(self):
        thread = FakeThread(alive=False)
        watcher = self._watcher_with_thread(thread)

        watcher.stop()

        self.assertIsNone(watcher._thread)


if __name__ == "__main__":
    unittest.main()
