# Copyright 2025 ArcaLB Authors
# SPDX-License-Identifier: Apache-2.0

"""Kubernetes client for managing VirtualIP custom resources."""

import logging
import threading

from kubernetes import client as k8s_client
from kubernetes import config as k8s_config
from kubernetes import watch as k8s_watch

from octavia_arca_driver import constants

LOG = logging.getLogger(__name__)


class VirtualIPClient:
    """Client for CRUD operations on VirtualIP custom resources."""

    def __init__(self, kubeconfig_path="", namespace="arca-system"):
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
        return f"octavia-{lb_id[:8]}-{listener_id[:8]}"

    def create_virtualip(self, name, spec, annotations=None, labels=None):
        """Create a VirtualIP custom resource."""
        body = {
            "apiVersion": constants.VIRTUALIP_API_VERSION,
            "kind": constants.VIRTUALIP_KIND,
            "metadata": {
                "name": name,
                "namespace": self._namespace,
                "labels": {
                    constants.LABEL_MANAGED_BY: constants.LABEL_MANAGED_BY_VALUE,
                    **(labels or {}),
                },
                "annotations": annotations or {},
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

    def update_virtualip(self, name, spec, annotations=None, labels=None):
        """Update an existing VirtualIP custom resource (patch)."""
        patch = {"spec": spec}
        if annotations is not None:
            patch.setdefault("metadata", {})["annotations"] = (
                self._merge_patch_annotations(name, annotations)
            )
        if labels is not None:
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

    def _merge_patch_annotations(self, name, annotations):
        """Build annotation patch with explicit nulls for deleted keys."""
        desired = dict(annotations)
        current = self.get_virtualip(name) or {}
        current_annotations = current.get("metadata", {}).get("annotations") or {}
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
        selector = f"{constants.LABEL_MANAGED_BY}={constants.LABEL_MANAGED_BY_VALUE}"
        result = self.list_virtualips(label_selector=selector)
        vips = []
        for item in result.get("items", []):
            annotations = item.get("metadata", {}).get("annotations", {})
            if annotations.get(constants.ANNOTATION_LB_ID) == lb_id:
                vips.append(item)
        return vips

    def find_by_listener(self, listener_id):
        """Find a VirtualIP resource associated with a listener ID."""
        selector = f"{constants.LABEL_MANAGED_BY}={constants.LABEL_MANAGED_BY_VALUE}"
        result = self.list_virtualips(label_selector=selector)
        for item in result.get("items", []):
            annotations = item.get("metadata", {}).get("annotations", {})
            if annotations.get(constants.ANNOTATION_LISTENER_ID) == listener_id:
                return item
        return None

    def find_by_pool(self, pool_id):
        """Find a VirtualIP resource associated with a pool ID."""
        selector = f"{constants.LABEL_MANAGED_BY}={constants.LABEL_MANAGED_BY_VALUE}"
        result = self.list_virtualips(label_selector=selector)
        for item in result.get("items", []):
            annotations = item.get("metadata", {}).get("annotations", {})
            if annotations.get(constants.ANNOTATION_POOL_ID) == pool_id:
                return item
        return None


class VirtualIPStatusWatcher:
    """Watches VirtualIP status changes and invokes a callback."""

    def __init__(self, kubeconfig_path="", namespace="arca-system"):
        self._namespace = namespace
        if kubeconfig_path:
            k8s_config.load_kube_config(config_file=kubeconfig_path)
        else:
            k8s_config.load_incluster_config()
        self._api = k8s_client.CustomObjectsApi()
        self._stop_event = threading.Event()
        self._thread = None

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
        if self._thread:
            self._thread.join(timeout=5)
            self._thread = None
        LOG.info("VirtualIP status watcher stopped")

    def _watch_loop(self, callback):
        w = k8s_watch.Watch()
        selector = (
            f"{constants.LABEL_MANAGED_BY}={constants.LABEL_MANAGED_BY_VALUE}"
        )
        while not self._stop_event.is_set():
            try:
                stream = w.stream(
                    self._api.list_namespaced_custom_object,
                    group=constants.VIRTUALIP_GROUP,
                    version="v1alpha1",
                    namespace=self._namespace,
                    plural=constants.VIRTUALIP_PLURAL,
                    label_selector=selector,
                    timeout_seconds=60,
                )
                for event in stream:
                    if self._stop_event.is_set():
                        break
                    event_type = event.get("type", "")
                    obj = event.get("object", {})
                    try:
                        callback(event_type, obj)
                    except Exception:
                        LOG.exception(
                            "Error in status watcher callback for %s",
                            obj.get("metadata", {}).get("name", "unknown"),
                        )
            except Exception:
                if not self._stop_event.is_set():
                    LOG.exception(
                        "VirtualIP watch stream error, reconnecting..."
                    )
                    self._stop_event.wait(timeout=5)
