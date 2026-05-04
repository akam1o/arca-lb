package healthcheck

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/akam1o/arca-lb/internal/common/models"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestTCPProberUsesIPv6SafeAddress(t *testing.T) {
	prober, err := NewTCPProber(&models.HealthCheck{
		Type:       models.HCTypeTCP,
		TimeoutSec: 5,
		Config: models.HCConfig{
			"port": 8080,
		},
	}, logrus.New())
	require.NoError(t, err)

	require.Equal(t, "[2001:db8::1]:8080", tcpProbeAddress("2001:db8::1", prober.port))
}

func TestTCPProberProbeWithIPv6Target(t *testing.T) {
	listener, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback is not available: %v", err)
	}
	defer func() { _ = listener.Close() }()

	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		conn, err := listener.Accept()
		if err == nil {
			_ = conn.Close()
		}
	}()

	_, portStr, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	prober, err := NewTCPProber(&models.HealthCheck{
		Type:       models.HCTypeTCP,
		TimeoutSec: 5,
		Config: models.HCConfig{
			"port": port,
		},
	}, logrus.New())
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	result := prober.Probe(ctx, "::1")
	_ = listener.Close()

	require.True(t, result.Success, "Expected success with IPv6 target: %v", result.Error)
	require.NoError(t, result.Error)

	select {
	case <-acceptDone:
	case <-time.After(time.Second):
		t.Fatal("TCP server did not accept the probe connection")
	}
}
