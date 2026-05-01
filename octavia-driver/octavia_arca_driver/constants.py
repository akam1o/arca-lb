# Copyright 2025 ArcaLB Authors
# SPDX-License-Identifier: Apache-2.0

"""Constants and configuration options for the ArcaLB Octavia driver."""

from oslo_config import cfg

DRIVER_NAME = "arca"
DRIVER_DESCRIPTION = "ArcaLB VPP-based L3DSR Load Balancer driver"

VIRTUALIP_API_VERSION = "arca.io/v1alpha1"
VIRTUALIP_KIND = "VirtualIP"
VIRTUALIP_PLURAL = "virtualips"
VIRTUALIP_GROUP = "arca.io"

# Labels and annotations applied to VirtualIP resources created by this driver.
LABEL_MANAGED_BY = "app.kubernetes.io/managed-by"
LABEL_MANAGED_BY_VALUE = "octavia-arca-driver"
ANNOTATION_LB_ID = "arca.io/octavia-loadbalancer-id"
ANNOTATION_LISTENER_ID = "arca.io/octavia-listener-id"
ANNOTATION_POOL_ID = "arca.io/octavia-pool-id"
ANNOTATION_HM_ID = "arca.io/octavia-healthmonitor-id"
ANNOTATION_PROJECT_ID = "arca.io/octavia-project-id"
ANNOTATION_MEMBER_MAP = "arca.io/octavia-member-map"
ANNOTATION_DRAINING_MEMBER_IDS = "arca.io/octavia-draining-member-ids"

# Mapping from Octavia protocol strings to VirtualIP protocol values.
PROTOCOL_MAP = {
    "TCP": "TCP",
    "UDP": "UDP",
    "HTTP": "TCP",
    "HTTPS": "TCP",
}

# Mapping from Octavia health monitor types to VirtualIP health check types.
HEALTH_MONITOR_TYPE_MAP = {
    "HTTP": "http",
    "HTTPS": "https",
    "TCP": "tcp",
    "PING": "ping",
    "TLS-HELLO": "tls-hello",
}

# Supported flavor metadata keys.
FLAVOR_METADATA = {
    "encap_type": "Encapsulation type for return traffic (GRE4, GRE6, L3DSR, NAT4, NAT6)",
    "dscp": "DSCP marking value for L3DSR mode (1-63)",
}

VALID_ENCAP_TYPES = {"GRE4", "GRE6", "L3DSR", "NAT4", "NAT6"}

driver_opts = [
    cfg.StrOpt(
        "kubernetes_config",
        default="",
        help="Path to kubeconfig file. Empty uses in-cluster config.",
    ),
    cfg.StrOpt(
        "namespace",
        default="arca-system",
        help="Kubernetes namespace for VirtualIP resources.",
    ),
    cfg.StrOpt(
        "default_encap_type",
        default="L3DSR",
        help="Default encapsulation type when not specified in flavor.",
    ),
    cfg.IntOpt(
        "default_dscp",
        default=10,
        help="Default DSCP value for L3DSR mode.",
    ),
    cfg.IntOpt(
        "status_sync_interval",
        default=10,
        help="Interval in seconds for syncing VirtualIP status to Octavia.",
    ),
]


def register_opts(conf):
    conf.register_opts(driver_opts, group="driver_arca")


def list_opts():
    return [("driver_arca", driver_opts)]
