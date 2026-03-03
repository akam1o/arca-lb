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
// +kubebuilder:validation:Enum=http;https;tcp;ping
type HCType string

const (
	HCTypeHTTP  HCType = "http"
	HCTypeHTTPS HCType = "https"
	HCTypeTCP   HCType = "tcp"
	HCTypePing  HCType = "ping"
)

// BackendSpec defines a real server backing a VIP.
type BackendSpec struct {
	// Address is the IP address of the backend server.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Format=ip
	Address string `json:"address"`

	// Weight controls the proportion of traffic sent to this backend (1-100).
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	// +kubebuilder:default=100
	Weight int `json:"weight,omitempty"`
}

// HealthCheckSpec configures health checking for backends.
type HealthCheckSpec struct {
	// Type is the probe type.
	// +kubebuilder:validation:Required
	Type HCType `json:"type"`

	// IntervalSeconds is the time between probes.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=5
	IntervalSeconds int `json:"intervalSeconds,omitempty"`

	// TimeoutSeconds is the maximum time to wait for a probe response.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=3
	TimeoutSeconds int `json:"timeoutSeconds,omitempty"`

	// RiseCount is the number of consecutive successes to mark a backend healthy.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=3
	RiseCount int `json:"riseCount,omitempty"`

	// FallCount is the number of consecutive failures to mark a backend unhealthy.
	// +kubebuilder:validation:Minimum=1
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
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int `json:"port"`

	// Path is the HTTP path to probe.
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
	ExpectedCodes []int `json:"expectedCodes,omitempty"`

	// SkipTLSVerify skips TLS certificate verification (HTTPS only).
	// +optional
	SkipTLSVerify bool `json:"skipTLSVerify,omitempty"`
}

// TCPHealthCheck configures TCP probes.
type TCPHealthCheck struct {
	// Port is the target port for the TCP connection.
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

	// DSCP is the DSCP value for L3DSR mode (1-63).
	// Required when encapType is L3DSR.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=63
	// +optional
	DSCP *uint8 `json:"dscp,omitempty"`

	// Backends is the list of real servers.
	// +optional
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
	Backends []BackendStatus `json:"backends,omitempty"`

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

	Spec   VirtualIPSpec   `json:"spec,omitempty"`
	Status VirtualIPStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// VirtualIPList contains a list of VirtualIP resources.
type VirtualIPList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VirtualIP `json:"items"`
}
