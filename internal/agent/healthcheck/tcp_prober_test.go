package healthcheck

import (
	"context"
	"errors"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/akam1o/arca-lb/internal/common/models"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
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

func TestNewTCPProberAcceptsValidatedIntegerPortShapes(t *testing.T) {
	tests := []struct {
		name string
		port any
		want int
	}{
		{name: "int32", port: int32(8080), want: 8080},
		{name: "int64", port: int64(8081), want: 8081},
		{name: "float64 integer", port: float64(8082), want: 8082},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prober, err := NewTCPProber(&models.HealthCheck{
				Type:       models.HCTypeTCP,
				TimeoutSec: 5,
				Config: models.HCConfig{
					"port": tt.port,
				},
			}, logrus.New())

			require.NoError(t, err)
			assert.Equal(t, tt.want, prober.port)
		})
	}
}

func TestNewTCPProberRejectsInvalidRuntimeConfig(t *testing.T) {
	tests := []struct {
		name   string
		config models.HCConfig
	}{
		{
			name: "fractional port",
			config: models.HCConfig{
				"port": 8080.5,
			},
		},
		{
			name: "out of range port",
			config: models.HCConfig{
				"port": 70000,
			},
		},
		{
			name: "non string send",
			config: models.HCConfig{
				"port": 8080,
				"send": []byte("PING"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prober, err := NewTCPProber(&models.HealthCheck{
				Type:       models.HCTypeTCP,
				TimeoutSec: 5,
				Config:     tt.config,
			}, logrus.New())

			require.Error(t, err)
			assert.Nil(t, prober)
			assert.Contains(t, err.Error(), "invalid TCP health check config")
		})
	}
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

func TestTCPProberProbeReadHonorsContextDeadline(t *testing.T) {
	target, port := startSilentTCPServer(t)
	prober, err := NewTCPProber(&models.HealthCheck{
		Type:       models.HCTypeTCP,
		TimeoutSec: 5,
		Config: models.HCConfig{
			"port":   port,
			"expect": "OK",
		},
	}, logrus.New())
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	result := prober.Probe(ctx, target)
	elapsed := time.Since(start)

	require.False(t, result.Success)
	require.Error(t, result.Error)
	require.True(t, errors.Is(result.Error, context.DeadlineExceeded), "error = %v", result.Error)
	require.Less(t, elapsed, time.Second)
}

func TestTCPProberProbeReadHonorsContextCancel(t *testing.T) {
	target, port := startSilentTCPServer(t)
	prober, err := NewTCPProber(&models.HealthCheck{
		Type:       models.HCTypeTCP,
		TimeoutSec: 5,
		Config: models.HCConfig{
			"port":   port,
			"expect": "OK",
		},
	}, logrus.New())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	timer := time.AfterFunc(50*time.Millisecond, cancel)
	defer timer.Stop()

	start := time.Now()
	result := prober.Probe(ctx, target)
	elapsed := time.Since(start)

	require.False(t, result.Success)
	require.Error(t, result.Error)
	require.True(t, errors.Is(result.Error, context.Canceled), "error = %v", result.Error)
	require.Less(t, elapsed, time.Second)
}
