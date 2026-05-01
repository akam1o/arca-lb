package webhook

import (
	"testing"

	v1alpha1 "github.com/akam1o/arca-lb/api/v1alpha1"
)

func ptr[T any](v T) *T { return &v }

func validVIP() *v1alpha1.VirtualIP {
	return &v1alpha1.VirtualIP{
		Spec: v1alpha1.VirtualIPSpec{
			Address:   "203.0.113.10",
			Port:      80,
			Protocol:  v1alpha1.ProtocolTCP,
			EncapType: v1alpha1.EncapTypeNAT4,
			Backends: []v1alpha1.BackendSpec{
				{Address: "10.0.1.1", Weight: 100},
				{Address: "10.0.1.2", Weight: 50},
			},
		},
	}
}

func TestValidateVirtualIP_Valid(t *testing.T) {
	vip := validVIP()
	if err := validateVirtualIP(vip); err != nil {
		t.Errorf("expected valid VIP to pass, got: %v", err)
	}
}

func TestValidateVirtualIP_InvalidAddress(t *testing.T) {
	vip := validVIP()
	vip.Spec.Address = "not-an-ip"
	if err := validateVirtualIP(vip); err == nil {
		t.Error("expected error for invalid address")
	}
}

func TestValidateVirtualIP_InvalidPort(t *testing.T) {
	for _, port := range []int{0, -1, 99999} {
		vip := validVIP()
		vip.Spec.Port = port
		if err := validateVirtualIP(vip); err == nil {
			t.Errorf("expected error for port %d", port)
		}
	}
}

func TestValidateVirtualIP_InvalidProtocol(t *testing.T) {
	vip := validVIP()
	vip.Spec.Protocol = "SCTP"
	if err := validateVirtualIP(vip); err == nil {
		t.Error("expected error for invalid protocol")
	}
}

func TestValidateVirtualIP_InvalidEncapType(t *testing.T) {
	vip := validVIP()
	vip.Spec.EncapType = "INVALID"
	if err := validateVirtualIP(vip); err == nil {
		t.Error("expected error for invalid encap type")
	}
}

func TestValidateVirtualIP_L3DSR_AllowsMissingDSCP(t *testing.T) {
	vip := validVIP()
	vip.Spec.EncapType = v1alpha1.EncapTypeL3DSR
	vip.Spec.DSCP = nil
	if err := validateVirtualIP(vip); err != nil {
		t.Errorf("expected L3DSR without per-VIP DSCP to be valid, got: %v", err)
	}
}

func TestValidateVirtualIP_L3DSR_InvalidDSCP(t *testing.T) {
	vip := validVIP()
	vip.Spec.EncapType = v1alpha1.EncapTypeL3DSR
	vip.Spec.DSCP = ptr(uint8(0))
	if err := validateVirtualIP(vip); err == nil {
		t.Error("expected error for DSCP=0")
	}

	vip.Spec.DSCP = ptr(uint8(64))
	if err := validateVirtualIP(vip); err == nil {
		t.Error("expected error for DSCP=64")
	}
}

func TestValidateVirtualIP_L3DSR_ValidDSCP(t *testing.T) {
	vip := validVIP()
	vip.Spec.EncapType = v1alpha1.EncapTypeL3DSR
	vip.Spec.DSCP = ptr(uint8(10))
	if err := validateVirtualIP(vip); err != nil {
		t.Errorf("expected L3DSR with DSCP=10 to be valid, got: %v", err)
	}
}

func TestValidateVirtualIP_DuplicateBackend(t *testing.T) {
	vip := validVIP()
	vip.Spec.Backends = []v1alpha1.BackendSpec{
		{Address: "10.0.1.1", Weight: 100},
		{Address: "10.0.1.1", Weight: 50},
	}
	if err := validateVirtualIP(vip); err == nil {
		t.Error("expected error for duplicate backend address")
	}
}

func TestValidateVirtualIP_InvalidBackendAddress(t *testing.T) {
	vip := validVIP()
	vip.Spec.Backends = []v1alpha1.BackendSpec{
		{Address: "bad-ip", Weight: 100},
	}
	if err := validateVirtualIP(vip); err == nil {
		t.Error("expected error for invalid backend address")
	}
}

func TestValidateVirtualIP_InvalidBackendMonitorAddress(t *testing.T) {
	vip := validVIP()
	vip.Spec.Backends = []v1alpha1.BackendSpec{
		{Address: "10.0.1.1", MonitorAddress: "bad-ip", Weight: 100},
	}
	if err := validateVirtualIP(vip); err == nil {
		t.Error("expected error for invalid backend monitor address")
	}
}

func TestValidateVirtualIP_InvalidBackendWeight(t *testing.T) {
	for _, w := range []int{0, -1, 101} {
		vip := validVIP()
		vip.Spec.Backends = []v1alpha1.BackendSpec{
			{Address: "10.0.1.1", Weight: w},
		}
		if err := validateVirtualIP(vip); err == nil {
			t.Errorf("expected error for weight %d", w)
		}
	}
}

func TestValidateVirtualIP_HTTPHealthCheck_Valid(t *testing.T) {
	vip := validVIP()
	vip.Spec.HealthCheck = &v1alpha1.HealthCheckSpec{
		Type:            v1alpha1.HCTypeHTTP,
		IntervalSeconds: 5,
		TimeoutSeconds:  3,
		RiseCount:       3,
		FallCount:       2,
		HTTP: &v1alpha1.HTTPHealthCheck{
			Port: 8080,
			Path: "/healthz",
		},
	}
	if err := validateVirtualIP(vip); err != nil {
		t.Errorf("expected valid HTTP health check, got: %v", err)
	}
}

func TestValidateVirtualIP_TLSHelloHealthCheck_Valid(t *testing.T) {
	vip := validVIP()
	vip.Spec.HealthCheck = &v1alpha1.HealthCheckSpec{
		Type:            v1alpha1.HCTypeTLSHello,
		IntervalSeconds: 5,
		TimeoutSeconds:  3,
		RiseCount:       3,
		FallCount:       2,
		TCP: &v1alpha1.TCPHealthCheck{
			Port: 8443,
		},
	}
	if err := validateVirtualIP(vip); err != nil {
		t.Errorf("expected valid TLS hello health check, got: %v", err)
	}
}

func TestValidateVirtualIP_HTTPHealthCheck_MissingHTTPConfig(t *testing.T) {
	vip := validVIP()
	vip.Spec.HealthCheck = &v1alpha1.HealthCheckSpec{
		Type:            v1alpha1.HCTypeHTTP,
		IntervalSeconds: 5,
		TimeoutSeconds:  3,
		RiseCount:       3,
		FallCount:       2,
	}
	if err := validateVirtualIP(vip); err == nil {
		t.Error("expected error when HTTP config is missing")
	}
}

func TestValidateVirtualIP_TCPHealthCheck_MissingTCPConfig(t *testing.T) {
	vip := validVIP()
	vip.Spec.HealthCheck = &v1alpha1.HealthCheckSpec{
		Type:            v1alpha1.HCTypeTCP,
		IntervalSeconds: 5,
		TimeoutSeconds:  3,
		RiseCount:       3,
		FallCount:       2,
	}
	if err := validateVirtualIP(vip); err == nil {
		t.Error("expected error when TCP config is missing")
	}
}

func TestValidateVirtualIP_HealthCheck_TimeoutGEInterval(t *testing.T) {
	vip := validVIP()
	vip.Spec.HealthCheck = &v1alpha1.HealthCheckSpec{
		Type:            v1alpha1.HCTypePing,
		IntervalSeconds: 3,
		TimeoutSeconds:  3,
		RiseCount:       3,
		FallCount:       2,
	}
	if err := validateVirtualIP(vip); err == nil {
		t.Error("expected error when timeout >= interval")
	}
}

func TestValidateVirtualIP_EmptyEncapType(t *testing.T) {
	vip := validVIP()
	vip.Spec.EncapType = ""
	if err := validateVirtualIP(vip); err != nil {
		t.Errorf("expected empty encap type to be valid, got: %v", err)
	}
}
