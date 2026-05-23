package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Protocol specifies the transport protocol.
// +kubebuilder:validation:Enum=TCP;UDP
type Protocol string

const (
	ProtocolTCP Protocol = "TCP"
	ProtocolUDP Protocol = "UDP"
)

// EncapType specifies the encapsulation type for return traffic.
// +kubebuilder:validation:Enum=GRE4;GRE6;L3DSR;NAT4;NAT6
type EncapType string

const (
	EncapTypeGRE4  EncapType = "GRE4"
	EncapTypeGRE6  EncapType = "GRE6"
	EncapTypeL3DSR EncapType = "L3DSR"
	EncapTypeNAT4  EncapType = "NAT4"
	EncapTypeNAT6  EncapType = "NAT6"
)

// HCType specifies the type of health check probe.
// +kubebuilder:validation:Enum=http;https;tcp;ping;tls-hello
type HCType string

const (
	HCTypeHTTP     HCType = "http"
	HCTypeHTTPS    HCType = "https"
	HCTypeTCP      HCType = "tcp"
	HCTypePing     HCType = "ping"
	HCTypeTLSHello HCType = "tls-hello"
)

// DefaultBackendWeight is the appliance-compatible default backend weight.
const DefaultBackendWeight = 1

const (
	// MaxVirtualIPStatusBackends caps per-backend status details retained on a
	// VirtualIP status object. Aggregate backend counts continue to report the
	// full configured backend set.
	MaxVirtualIPStatusBackends = 1024

	// MaxVirtualIPStatusAgentStatuses caps per-agent observations retained on a
	// VirtualIP status object.
	MaxVirtualIPStatusAgentStatuses = 256
)

// BackendSpec defines a real server backing a VIP.
type BackendSpec struct {
	// Address is the IP address of the backend server.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Format=ip
	Address string `json:"address"`

	// MonitorAddress is an optional alternate IP address used only for health checks.
	// When omitted, health checks probe Address.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Format=ip
	MonitorAddress string `json:"monitorAddress,omitempty"`

	// Weight records the desired proportion of traffic sent to this backend (1-100).
	// Defaults to 1 when omitted.
	// The VPP LB plugin path currently stores this as metadata; weighted AS
	// programming will take effect once the VPP LB API exposes backend weights.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	// +kubebuilder:default=1
	Weight int `json:"weight,omitempty"`
}

// HealthCheckSpec configures health checking for backends.
// +kubebuilder:validation:XValidation:rule="(self.type != 'http' && self.type != 'https') || has(self.http)",message="spec.healthCheck.http is required for type http or https"
// +kubebuilder:validation:XValidation:rule="(self.type != 'tcp' && self.type != 'tls-hello') || has(self.tcp)",message="spec.healthCheck.tcp is required for type tcp or tls-hello"
// +kubebuilder:validation:XValidation:rule="(has(self.timeoutSeconds) ? self.timeoutSeconds : 3) < (has(self.intervalSeconds) ? self.intervalSeconds : 5)",message="spec.healthCheck.timeoutSeconds must be less than intervalSeconds"
type HealthCheckSpec struct {
	// Type is the probe type.
	// +kubebuilder:validation:Required
	Type HCType `json:"type"`

	// IntervalSeconds is the time between probes.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=2147483647
	// +kubebuilder:default=5
	IntervalSeconds int `json:"intervalSeconds,omitempty"`

	// TimeoutSeconds is the maximum time to wait for a probe response.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=2147483647
	// +kubebuilder:default=3
	TimeoutSeconds int `json:"timeoutSeconds,omitempty"`

	// RiseCount is the number of consecutive successes to mark a backend healthy.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=2147483647
	// +kubebuilder:default=3
	RiseCount int `json:"riseCount,omitempty"`

	// FallCount is the number of consecutive failures to mark a backend unhealthy.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=2147483647
	// +kubebuilder:default=2
	FallCount int `json:"fallCount,omitempty"`

	// HTTP contains HTTP/HTTPS-specific probe settings.
	// +optional
	HTTP *HTTPHealthCheck `json:"http,omitempty"`

	// TCP contains TCP-specific probe settings.
	// +optional
	TCP *TCPHealthCheck `json:"tcp,omitempty"`
}

// HTTPHealthCheck configures HTTP/HTTPS probes.
type HTTPHealthCheck struct {
	// Port is the target port for the HTTP request.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int `json:"port"`

	// Path is the HTTP path to probe.
	// +kubebuilder:validation:Pattern=`^/($|[^/].*)`
	// +kubebuilder:default="/"
	Path string `json:"path,omitempty"`

	// Method is the HTTP method.
	// +kubebuilder:validation:Enum=GET;HEAD;POST
	// +kubebuilder:default="GET"
	Method string `json:"method,omitempty"`

	// Host sets the Host header for the probe request.
	// +optional
	Host string `json:"host,omitempty"`

	// Headers are additional HTTP headers to send.
	// +optional
	Headers map[string]string `json:"headers,omitempty"`

	// ExpectedCodes is the set of HTTP status codes that indicate success.
	// +optional
	// +kubebuilder:validation:items:Minimum=100
	// +kubebuilder:validation:items:Maximum=599
	ExpectedCodes []int `json:"expectedCodes,omitempty"`

	// SkipTLSVerify skips TLS certificate verification (HTTPS only).
	// +optional
	SkipTLSVerify bool `json:"skipTLSVerify,omitempty"`
}

// TCPHealthCheck configures TCP probes.
type TCPHealthCheck struct {
	// Port is the target port for the TCP connection.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int `json:"port"`

	// Send is optional data to send after connection.
	// +optional
	Send string `json:"send,omitempty"`

	// ExpectedResponse is an optional substring expected in the response.
	// +optional
	ExpectedResponse string `json:"expectedResponse,omitempty"`
}

// VirtualIPSpec defines the desired state of a VirtualIP.
// +kubebuilder:validation:XValidation:rule="!has(self.encapType) || (self.encapType != 'L3DSR' && self.encapType != 'NAT4') || self.address.matches('^((25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])[.]){3}(25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])$')",message="spec.encapType L3DSR/NAT4 requires an IPv4 spec.address"
// +kubebuilder:validation:XValidation:rule="!has(self.encapType) || self.encapType != 'NAT6' || !self.address.matches('^((25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])[.]){3}(25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])$')",message="spec.encapType NAT6 requires an IPv6 spec.address"
// +kubebuilder:validation:XValidation:rule="!has(self.encapType) || (self.encapType != 'GRE4' && self.encapType != 'L3DSR' && self.encapType != 'NAT4') || !has(self.backends) || self.backends.all(be, be.address.matches('^((25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])[.]){3}(25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])$'))",message="spec.backends addresses must be IPv4 for GRE4/L3DSR/NAT4 encapType"
// +kubebuilder:validation:XValidation:rule="!has(self.encapType) || (self.encapType != 'GRE6' && self.encapType != 'NAT6') || !has(self.backends) || self.backends.all(be, !be.address.matches('^((25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])[.]){3}(25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])$'))",message="spec.backends addresses must be IPv6 for GRE6/NAT6 encapType"
type VirtualIPSpec struct {
	// Address is the virtual IP address.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Format=ip
	Address string `json:"address"`

	// Port is the virtual port.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int `json:"port"`

	// Protocol is the transport protocol.
	// +kubebuilder:validation:Required
	Protocol Protocol `json:"protocol"`

	// EncapType is the encapsulation type for return traffic.
	// +kubebuilder:default="L3DSR"
	EncapType EncapType `json:"encapType,omitempty"`

	// DSCP is an optional DSCP override for DSCP-based L3DSR steering.
	// When omitted, the agent's dataplane.vpp.dscp default is used.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=63
	// +optional
	DSCP *uint8 `json:"dscp,omitempty"`

	// Backends is the list of real servers.
	// +optional
	// +kubebuilder:validation:XValidation:rule="self.all(b1, self.exists_one(b2, b2.address == b1.address))",message="spec.backends addresses must be unique"
	Backends []BackendSpec `json:"backends,omitempty"`

	// HealthCheck configures health checking for the backends.
	// +optional
	HealthCheck *HealthCheckSpec `json:"healthCheck,omitempty"`
}

// BackendStatus reports the health state of a single backend.
type BackendStatus struct {
	// Address is the IP address of the backend.
	Address string `json:"address"`
	// Healthy indicates whether the backend is currently healthy.
	Healthy bool `json:"healthy"`
	// LastProbeTime is the timestamp of the most recent probe.
	// +optional
	LastProbeTime *metav1.Time `json:"lastProbeTime,omitempty"`
	// Message provides human-readable details.
	// +optional
	Message string `json:"message,omitempty"`
}

// AgentStatus reports one agent's observation of a VirtualIP.
type AgentStatus struct {
	// AgentID identifies the reporting agent, typically the Kubernetes node name.
	AgentID string `json:"agentID"`

	// ObservedGeneration is the VirtualIP generation observed by this agent.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// HealthyBackends is the number of healthy backends observed by this agent.
	HealthyBackends int `json:"healthyBackends"`

	// TotalBackends is the total number of configured backends observed by this agent.
	TotalBackends int `json:"totalBackends"`

	// Backends reports per-backend health status observed by this agent.
	// +optional
	// +kubebuilder:validation:MaxItems=1024
	Backends []BackendStatus `json:"backends,omitempty"`

	// Conditions represent this agent's latest observations.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// LastUpdateTime is when this agent observation was written.
	// +optional
	LastUpdateTime *metav1.Time `json:"lastUpdateTime,omitempty"`

	// TTLSeconds is how long this observation should remain valid.
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=3600
	TTLSeconds int64 `json:"ttlSeconds,omitempty"`
}

// VirtualIPStatus defines the observed state of a VirtualIP.
type VirtualIPStatus struct {
	// ObservedGeneration is the most recent generation observed by the operator.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// HealthyBackends is the number of healthy backends.
	HealthyBackends int `json:"healthyBackends"`

	// TotalBackends is the total number of configured backends.
	TotalBackends int `json:"totalBackends"`

	// Backends reports per-backend health status.
	// +optional
	// +kubebuilder:validation:MaxItems=1024
	Backends []BackendStatus `json:"backends,omitempty"`

	// AgentStatuses contains per-agent observations used to build the aggregate status.
	// +optional
	// +kubebuilder:validation:MaxItems=256
	// +listType=map
	// +listMapKey=agentID
	AgentStatuses []AgentStatus `json:"agentStatuses,omitempty"`

	// Conditions represent the latest available observations.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=vip
// +kubebuilder:printcolumn:name="Address",type=string,JSONPath=`.spec.address`
// +kubebuilder:printcolumn:name="Port",type=integer,JSONPath=`.spec.port`
// +kubebuilder:printcolumn:name="Protocol",type=string,JSONPath=`.spec.protocol`
// +kubebuilder:printcolumn:name="Healthy",type=integer,JSONPath=`.status.healthyBackends`
// +kubebuilder:printcolumn:name="Total",type=integer,JSONPath=`.status.totalBackends`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// VirtualIP is a Layer 4 virtual IP managed by arca-lb.
type VirtualIP struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec   VirtualIPSpec   `json:"spec"`
	Status VirtualIPStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// VirtualIPList contains a list of VirtualIP resources.
type VirtualIPList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VirtualIP `json:"items"`
}
