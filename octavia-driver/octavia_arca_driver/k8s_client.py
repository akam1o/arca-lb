# Copyright 2025 ArcaLB Authors
# SPDX-License-Identifier: Apache-2.0

"""Kubernetes client for managing VirtualIP custom resources."""

import hashlib
import logging
import threading
import time

from kubernetes import client as k8s_client
from kubernetes import config as k8s_config
from kubernetes import watch as k8s_watch

from octavia_arca_driver import constants

LOG = logging.getLogger(__name__)

DEFAULT_CALLBACK_RETRY_DELAYS = (0.25, 1.0, 2.0)


class VirtualIPClient:
    """Client for CRUD operations on VirtualIP custom resources."""

    def __init__(self, kubeconfig_path="", namespace="arca-lb-system"):
        self._namespace = namespace
        if kubeconfig_path:
            k8s_config.load_kube_config(config_file=kubeconfig_path)
        else:
            k8s_config.load_incluster_config()
        self._api = k8s_client.CustomObjectsApi()

    @property
    def namespace(self):
        return self._namespace

    def _resource_name(self, lb_id, listener_id):
        """Generate a deterministic VirtualIP resource name from Octavia IDs."""
        raw_name = f"{lb_id}:{listener_id}".encode("utf-8")
        return f"octavia-{hashlib.sha256(raw_name).hexdigest()[:40]}"

    def _identity_labels(self, annotations):
        labels = {}
        identity_annotations = (
            (constants.ANNOTATION_LB_ID, constants.LABEL_OCTAVIA_LB_ID_HASH),
            (
                constants.ANNOTATION_LISTENER_ID,
                constants.LABEL_OCTAVIA_LISTENER_ID_HASH,
            ),
            (
                constants.ANNOTATION_POOL_ID,
                constants.LABEL_OCTAVIA_POOL_ID_HASH,
            ),
        )
        for annotation_key, label_key in identity_annotations:
            value = (annotations or {}).get(annotation_key)
            if value:
                labels[label_key] = self._identity_label_value(value)
        return labels

    @staticmethod
    def _identity_label_value(value):
        return hashlib.sha256(value.encode("utf-8")).hexdigest()[:40]

    def _managed_label_patch(self, current, annotations, labels=None):
        patch = {
            constants.LABEL_MANAGED_BY: constants.LABEL_MANAGED_BY_VALUE,
            **(labels or {}),
            **self._identity_labels(annotations),
        }
        current_labels = (
            (current or {}).get("metadata", {}).get("labels") or {}
        )
        for key in constants.MANAGED_LABELS:
            if key in current_labels and key not in patch:
                patch[key] = None
        return patch

    def _selector_for_identity(self, label_key, value):
        return ",".join((
            f"{constants.LABEL_MANAGED_BY}={constants.LABEL_MANAGED_BY_VALUE}",
            f"{label_key}={self._identity_label_value(value)}",
        ))

    def create_virtualip(self, name, spec, annotations=None, labels=None):
        """Create a VirtualIP custom resource."""
        annotations = annotations or {}
        body = {
            "apiVersion": constants.VIRTUALIP_API_VERSION,
            "kind": constants.VIRTUALIP_KIND,
            "metadata": {
                "name": name,
                "namespace": self._namespace,
                "labels": {
                    constants.LABEL_MANAGED_BY: (
                        constants.LABEL_MANAGED_BY_VALUE
                    ),
                    **(labels or {}),
                    **self._identity_labels(annotations),
                },
                "annotations": annotations,
            },
            "spec": spec,
        }
        LOG.info("Creating VirtualIP %s/%s", self._namespace, name)
        return self._api.create_namespaced_custom_object(
            group=constants.VIRTUALIP_GROUP,
            version="v1alpha1",
            namespace=self._namespace,
            plural=constants.VIRTUALIP_PLURAL,
            body=body,
        )

    def update_virtualip(self, name, spec, annotations=None, labels=None,
                         resource_version=None, current=None):
        """Update an existing VirtualIP custom resource (patch)."""
        if annotations is not None and current is None:
            current = self.get_virtualip(name) or {}

        patch = {"spec": self._merge_patch_spec(spec)}
        if resource_version:
            patch.setdefault("metadata", {})["resourceVersion"] = (
                resource_version
            )
        if annotations is not None:
            patch.setdefault("metadata", {})["annotations"] = (
                self._merge_patch_annotations(current, annotations)
            )
            patch.setdefault("metadata", {})["labels"] = (
                self._managed_label_patch(current, annotations, labels)
            )
        elif labels is not None:
            patch.setdefault("metadata", {})["labels"] = labels

        LOG.info("Patching VirtualIP %s/%s", self._namespace, name)
        return self._api.patch_namespaced_custom_object(
            group=constants.VIRTUALIP_GROUP,
            version="v1alpha1",
            namespace=self._namespace,
            plural=constants.VIRTUALIP_PLURAL,
            name=name,
            body=patch,
        )

    def _merge_patch_spec(self, spec):
        """Build spec patch with explicit nulls for removed optional fields."""
        desired = dict(spec)
        for key in ("dscp", "healthCheck"):
            if key not in desired:
                desired[key] = None
        return desired

    def _merge_patch_annotations(self, current, annotations):
        """Build annotation patch with explicit nulls for deleted keys."""
        desired = dict(annotations)
        current_annotations = (
            (current or {}).get("metadata", {}).get("annotations") or {}
        )
        for key in constants.MANAGED_ANNOTATIONS:
            if key in current_annotations and key not in desired:
                desired[key] = None
        return desired

    def delete_virtualip(self, name):
        """Delete a VirtualIP custom resource."""
        LOG.info("Deleting VirtualIP %s/%s", self._namespace, name)
        try:
            self._api.delete_namespaced_custom_object(
                group=constants.VIRTUALIP_GROUP,
                version="v1alpha1",
                namespace=self._namespace,
                plural=constants.VIRTUALIP_PLURAL,
                name=name,
            )
        except k8s_client.exceptions.ApiException as e:
            if e.status == 404:
                LOG.warning("VirtualIP %s/%s not found, skipping delete",
                            self._namespace, name)
            else:
                raise

    def get_virtualip(self, name):
        """Get a VirtualIP custom resource."""
        try:
            return self._api.get_namespaced_custom_object(
                group=constants.VIRTUALIP_GROUP,
                version="v1alpha1",
                namespace=self._namespace,
                plural=constants.VIRTUALIP_PLURAL,
                name=name,
            )
        except k8s_client.exceptions.ApiException as e:
            if e.status == 404:
                return None
            raise

    def list_virtualips(self, label_selector=""):
        """List VirtualIP custom resources."""
        return self._api.list_namespaced_custom_object(
            group=constants.VIRTUALIP_GROUP,
            version="v1alpha1",
            namespace=self._namespace,
            plural=constants.VIRTUALIP_PLURAL,
            label_selector=label_selector,
        )

    def find_by_loadbalancer(self, lb_id):
        """Find all VirtualIP resources associated with a loadbalancer ID."""
        vips = []
        seen = set()

        def append_matching(items):
            for item in items:
                annotations = item.get("metadata", {}).get(
                    "annotations", {}
                )
                if annotations.get(constants.ANNOTATION_LB_ID) != lb_id:
                    continue
                identity = self._object_identity(item)
                if identity in seen:
                    continue
                seen.add(identity)
                vips.append(item)

        result = self.list_virtualips(
            label_selector=self._selector_for_identity(
                constants.LABEL_OCTAVIA_LB_ID_HASH, lb_id
            )
        )
        append_matching(result.get("items", []))

        result = self.list_virtualips(label_selector=self._managed_selector())
        append_matching(result.get("items", []))
        return vips

    def find_by_listener(self, listener_id):
        """Find a VirtualIP resource associated with a listener ID."""
        result = self.list_virtualips(
            label_selector=self._selector_for_identity(
                constants.LABEL_OCTAVIA_LISTENER_ID_HASH, listener_id
            )
        )
        for item in result.get("items", []):
            annotations = item.get("metadata", {}).get("annotations", {})
            if (
                annotations.get(constants.ANNOTATION_LISTENER_ID) ==
                listener_id
            ):
                return item

        result = self.list_virtualips(label_selector=self._managed_selector())
        for item in result.get("items", []):
            annotations = item.get("metadata", {}).get("annotations", {})
            if (
                annotations.get(constants.ANNOTATION_LISTENER_ID) ==
                listener_id
            ):
                return item
        return None

    def find_by_pool(self, pool_id):
        """Find a VirtualIP resource associated with a pool ID."""
        result = self.list_virtualips(
            label_selector=self._selector_for_identity(
                constants.LABEL_OCTAVIA_POOL_ID_HASH, pool_id
            )
        )
        for item in result.get("items", []):
            annotations = item.get("metadata", {}).get("annotations", {})
            if annotations.get(constants.ANNOTATION_POOL_ID) == pool_id:
                return item

        result = self.list_virtualips(label_selector=self._managed_selector())
        for item in result.get("items", []):
            annotations = item.get("metadata", {}).get("annotations", {})
            if annotations.get(constants.ANNOTATION_POOL_ID) == pool_id:
                return item
        return None

    @staticmethod
    def _managed_selector():
        return (
            f"{constants.LABEL_MANAGED_BY}="
            f"{constants.LABEL_MANAGED_BY_VALUE}"
        )

    @staticmethod
    def _object_identity(item):
        metadata = item.get("metadata", {})
        if metadata.get("uid"):
            return ("uid", metadata["uid"])
        return (
            "name",
            metadata.get("namespace", ""),
            metadata.get("name", ""),
        )


class VirtualIPStatusWatcher:
    """Watches VirtualIP status changes and invokes a callback."""

    def __init__(self, kubeconfig_path="", namespace="arca-lb-system",
                 sync_interval=0):
        self._namespace = namespace
        self._sync_interval = max(int(sync_interval or 0), 0)
        if kubeconfig_path:
            k8s_config.load_kube_config(config_file=kubeconfig_path)
        else:
            k8s_config.load_incluster_config()
        self._api = k8s_client.CustomObjectsApi()
        self._stop_event = threading.Event()
        self._thread = None
        self._watch = None
        self._watch_lock = threading.Lock()
        self._callback_retry_delays = DEFAULT_CALLBACK_RETRY_DELAYS
        self._callback_failures_total = 0
        self._callback_failure_streak = 0

    @property
    def callback_failures_total(self):
        return getattr(self, "_callback_failures_total", 0)

    @property
    def callback_failure_streak(self):
        return getattr(self, "_callback_failure_streak", 0)

    def health(self):
        return {
            "healthy": self.callback_failure_streak == 0,
            "running": (
                self._thread is not None and self._thread.is_alive()
            ),
            "callback_failures_total": self.callback_failures_total,
            "callback_failure_streak": self.callback_failure_streak,
        }

    def start(self, callback):
        """Start watching VirtualIP status changes in a background thread.

        Args:
            callback: A callable(event_type, virtualip_obj) invoked on changes.
        """
        self._stop_event.clear()
        self._thread = threading.Thread(
            target=self._watch_loop,
            args=(callback,),
            daemon=True,
            name="virtualip-status-watcher",
        )
        self._thread.start()
        LOG.info("VirtualIP status watcher started for namespace %s",
                 self._namespace)

    def stop(self):
        """Stop the status watcher."""
        self._stop_event.set()
        with self._watch_lock:
            watch = self._watch
        if watch is not None:
            watch.stop()
        if self._thread:
            self._thread.join(timeout=5)
            if self._thread.is_alive():
                LOG.warning(
                    "VirtualIP status watcher did not stop within timeout"
                )
            else:
                self._thread = None
        LOG.info("VirtualIP status watcher stopped")

    def _watch_loop(self, callback):
        selector = self._label_selector()
        last_sync = 0.0
        while not self._stop_event.is_set():
            w = None
            try:
                if self._sync_interval > 0:
                    now = time.monotonic()
                    if now - last_sync >= self._sync_interval:
                        self._sync_current(callback, selector=selector)
                        last_sync = time.monotonic()

                w = k8s_watch.Watch()
                with self._watch_lock:
                    self._watch = w
                stream = w.stream(
                    self._api.list_namespaced_custom_object,
                    group=constants.VIRTUALIP_GROUP,
                    version="v1alpha1",
                    namespace=self._namespace,
                    plural=constants.VIRTUALIP_PLURAL,
                    label_selector=selector,
                    timeout_seconds=self._watch_timeout_seconds(),
                )
                for event in stream:
                    if self._stop_event.is_set():
                        break
                    event_type = event.get("type", "")
                    obj = event.get("object", {})
                    self._invoke_callback(callback, event_type, obj, "watch")
            except Exception:
                if not self._stop_event.is_set():
                    LOG.exception(
                        "VirtualIP watch stream error, reconnecting..."
                    )
                    self._stop_event.wait(timeout=5)
            finally:
                if w is not None:
                    w.stop()
                    with self._watch_lock:
                        if self._watch is w:
                            self._watch = None

    def _sync_current(self, callback, selector=None):
        selector = selector or self._label_selector()
        result = self._api.list_namespaced_custom_object(
            group=constants.VIRTUALIP_GROUP,
            version="v1alpha1",
            namespace=self._namespace,
            plural=constants.VIRTUALIP_PLURAL,
            label_selector=selector,
        )
        for obj in result.get("items", []):
            self._invoke_callback(callback, "SYNC", obj, "sync")

    def _invoke_callback(self, callback, event_type, obj, source):
        name = obj.get("metadata", {}).get("name", "unknown")
        delays = getattr(
            self, "_callback_retry_delays", DEFAULT_CALLBACK_RETRY_DELAYS
        )
        for attempt in range(len(delays) + 1):
            try:
                callback(event_type, obj)
                self._callback_failure_streak = 0
                return True
            except Exception:
                self._callback_failures_total = (
                    getattr(self, "_callback_failures_total", 0) + 1
                )
                self._callback_failure_streak = (
                    getattr(self, "_callback_failure_streak", 0) + 1
                )
                attempts = len(delays) + 1
                if attempt >= len(delays) or self._stop_event.is_set():
                    LOG.exception(
                        "Status watcher %s callback for %s failed after "
                        "%d/%d attempts; failures_total=%d "
                        "failure_streak=%d",
                        source,
                        name,
                        attempt + 1,
                        attempts,
                        self._callback_failures_total,
                        self._callback_failure_streak,
                    )
                    return False

                delay = delays[attempt]
                LOG.exception(
                    "Status watcher %s callback for %s failed on attempt "
                    "%d/%d; retrying in %.2fs; failures_total=%d "
                    "failure_streak=%d",
                    source,
                    name,
                    attempt + 1,
                    attempts,
                    delay,
                    self._callback_failures_total,
                    self._callback_failure_streak,
                )
                if self._stop_event.wait(timeout=delay):
                    return False

        return False

    @staticmethod
    def _label_selector():
        return (
            f"{constants.LABEL_MANAGED_BY}={constants.LABEL_MANAGED_BY_VALUE}"
        )

    def _watch_timeout_seconds(self):
        if self._sync_interval <= 0:
            return 60
        return max(1, min(60, self._sync_interval))
