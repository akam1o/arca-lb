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
    def __init__(self, current=None, items=None, list_results=None):
        self.current = current
        self.items = items or []
        self.list_results = list(list_results) if list_results else None
        self.create_body = None
        self.patch_body = None
        self.list_kwargs = None
        self.list_calls = []

    def get_namespaced_custom_object(self, **_kwargs):
        return self.current

    def list_namespaced_custom_object(self, **kwargs):
        self.list_kwargs = kwargs
        self.list_calls.append(kwargs)
        if self.list_results is not None:
            return self.list_results.pop(0)
        return {"items": self.items}

    def create_namespaced_custom_object(self, **kwargs):
        self.create_body = kwargs["body"]
        return self.create_body

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
    def test_create_virtualip_adds_octavia_identity_hash_labels(self):
        api = FakeCustomObjectsApi()
        client = VirtualIPClient.__new__(VirtualIPClient)
        client._namespace = "arca-lb-system"
        client._api = api

        client.create_virtualip(
            "vip-1",
            {"address": "203.0.113.10", "port": 80, "protocol": "TCP"},
            annotations={
                constants.ANNOTATION_LB_ID: "lb-1111",
                constants.ANNOTATION_LISTENER_ID: "listener-1111",
                constants.ANNOTATION_POOL_ID: "pool-1111",
            },
        )

        labels = api.create_body["metadata"]["labels"]
        self.assertEqual(
            labels[constants.LABEL_MANAGED_BY],
            constants.LABEL_MANAGED_BY_VALUE,
        )
        self.assertEqual(
            labels[constants.LABEL_OCTAVIA_LB_ID_HASH],
            client._identity_label_value("lb-1111"),
        )
        self.assertEqual(
            labels[constants.LABEL_OCTAVIA_LISTENER_ID_HASH],
            client._identity_label_value("listener-1111"),
        )
        self.assertEqual(
            labels[constants.LABEL_OCTAVIA_POOL_ID_HASH],
            client._identity_label_value("pool-1111"),
        )

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

    def test_update_virtualip_updates_octavia_identity_hash_labels(self):
        api = FakeCustomObjectsApi({
            "metadata": {
                "labels": {
                    constants.LABEL_OCTAVIA_LB_ID_HASH: "stale-lb",
                    constants.LABEL_OCTAVIA_POOL_ID_HASH: "stale-pool",
                    "example.com/keep": "value",
                },
                "annotations": {
                    constants.ANNOTATION_LB_ID: "old-lb",
                    constants.ANNOTATION_POOL_ID: "old-pool",
                },
            },
        })
        client = VirtualIPClient.__new__(VirtualIPClient)
        client._namespace = "arca-lb-system"
        client._api = api

        client.update_virtualip(
            "vip-1",
            {"address": "203.0.113.10", "port": 80, "protocol": "TCP"},
            annotations={constants.ANNOTATION_LB_ID: "lb-1111"},
        )

        labels = api.patch_body["metadata"]["labels"]
        self.assertEqual(
            labels[constants.LABEL_OCTAVIA_LB_ID_HASH],
            client._identity_label_value("lb-1111"),
        )
        self.assertIsNone(labels[constants.LABEL_OCTAVIA_POOL_ID_HASH])
        self.assertNotIn("example.com/keep", labels)

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

    def test_find_by_pool_uses_octavia_identity_label_selector(self):
        item = {
            "metadata": {
                "annotations": {
                    constants.ANNOTATION_POOL_ID: "pool-1111",
                },
            },
        }
        api = FakeCustomObjectsApi(items=[item])
        client = VirtualIPClient.__new__(VirtualIPClient)
        client._namespace = "arca-lb-system"
        client._api = api

        self.assertIs(client.find_by_pool("pool-1111"), item)

        selector = api.list_calls[0]["label_selector"]
        self.assertIn(
            f"{constants.LABEL_MANAGED_BY}={constants.LABEL_MANAGED_BY_VALUE}",
            selector,
        )
        self.assertIn(
            constants.LABEL_OCTAVIA_POOL_ID_HASH + "=" +
            client._identity_label_value("pool-1111"),
            selector,
        )
        self.assertEqual(len(api.list_calls), 1)

    def test_find_by_listener_falls_back_to_annotation_scan(self):
        item = {
            "metadata": {
                "annotations": {
                    constants.ANNOTATION_LISTENER_ID: "listener-1111",
                },
            },
        }
        api = FakeCustomObjectsApi(list_results=[
            {"items": []},
            {"items": [item]},
        ])
        client = VirtualIPClient.__new__(VirtualIPClient)
        client._namespace = "arca-lb-system"
        client._api = api

        self.assertIs(client.find_by_listener("listener-1111"), item)

        self.assertEqual(len(api.list_calls), 2)
        self.assertEqual(
            api.list_calls[1]["label_selector"],
            f"{constants.LABEL_MANAGED_BY}={constants.LABEL_MANAGED_BY_VALUE}",
        )


class TestVirtualIPStatusWatcher(unittest.TestCase):
    def _watcher_with_thread(self, thread):
        watcher = VirtualIPStatusWatcher.__new__(VirtualIPStatusWatcher)
        watcher._stop_event = threading.Event()
        watcher._thread = thread
        watcher._watch = FakeWatch()
        watcher._watch_lock = threading.Lock()
        watcher._namespace = "arca-lb-system"
        watcher._sync_interval = 10
        watcher._callback_retry_delays = []
        watcher._callback_failures_total = 0
        watcher._callback_failure_streak = 0
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

    def test_sync_current_emits_existing_virtualips(self):
        first = {"metadata": {"name": "vip-1"}}
        second = {"metadata": {"name": "vip-2"}}
        api = FakeCustomObjectsApi(items=[first, second])
        watcher = self._watcher_with_thread(FakeThread(alive=False))
        watcher._api = api
        events = []

        watcher._sync_current(lambda event_type, obj: events.append(
            (event_type, obj)
        ))

        self.assertEqual(events, [("SYNC", first), ("SYNC", second)])
        self.assertEqual(
            api.list_kwargs["label_selector"],
            f"{constants.LABEL_MANAGED_BY}={constants.LABEL_MANAGED_BY_VALUE}",
        )

    def test_sync_current_retries_callback_failures(self):
        obj = {"metadata": {"name": "vip-1"}}
        api = FakeCustomObjectsApi(items=[obj])
        watcher = self._watcher_with_thread(FakeThread(alive=False))
        watcher._api = api
        watcher._callback_retry_delays = [0]
        calls = []

        def callback(event_type, event_obj):
            calls.append((event_type, event_obj))
            if len(calls) == 1:
                raise RuntimeError("transient callback failure")

        watcher._sync_current(callback)

        self.assertEqual(calls, [("SYNC", obj), ("SYNC", obj)])
        self.assertEqual(watcher._callback_failures_total, 1)
        self.assertEqual(watcher._callback_failure_streak, 0)

    def test_sync_current_tracks_repeated_callback_failures(self):
        obj = {"metadata": {"name": "vip-1"}}
        api = FakeCustomObjectsApi(items=[obj])
        watcher = self._watcher_with_thread(FakeThread(alive=False))
        watcher._api = api
        watcher._callback_retry_delays = [0, 0]
        calls = []

        def callback(event_type, event_obj):
            calls.append((event_type, event_obj))
            raise RuntimeError("persistent callback failure")

        watcher._sync_current(callback)

        self.assertEqual(
            calls,
            [("SYNC", obj), ("SYNC", obj), ("SYNC", obj)],
        )
        self.assertEqual(watcher._callback_failures_total, 3)
        self.assertEqual(watcher._callback_failure_streak, 3)

    def test_watch_timeout_tracks_sync_interval(self):
        watcher = self._watcher_with_thread(FakeThread(alive=False))

        watcher._sync_interval = 10
        self.assertEqual(watcher._watch_timeout_seconds(), 10)

        watcher._sync_interval = 120
        self.assertEqual(watcher._watch_timeout_seconds(), 60)

        watcher._sync_interval = 0
        self.assertEqual(watcher._watch_timeout_seconds(), 60)


if __name__ == "__main__":
    unittest.main()
