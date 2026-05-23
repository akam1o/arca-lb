package etcd

import (
	"context"
	"errors"
	"testing"

	"github.com/akam1o/arca-lb/internal/common/datastore"
	"github.com/akam1o/arca-lb/internal/common/models"
)

func TestEtcdDataStoreRejectsNilMutationInputs(t *testing.T) {
	ctx := context.Background()
	ds := &EtcdDataStore{}

	tests := []struct {
		name string
		call func() error
	}{
		{name: "CreateVIP", call: func() error { return ds.CreateVIP(ctx, nil) }},
		{name: "UpdateVIP", call: func() error { return ds.UpdateVIP(ctx, nil) }},
		{name: "AddBackend", call: func() error { return ds.AddBackend(ctx, nil) }},
		{name: "UpdateBackend", call: func() error { return ds.UpdateBackend(ctx, nil) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, datastore.ErrInvalidInput) {
				t.Fatalf("error = %v, want %v", err, datastore.ErrInvalidInput)
			}
		})
	}
}

func TestEtcdDataStoreRejectsEmptyMutationIdentities(t *testing.T) {
	ctx := context.Background()
	ds := &EtcdDataStore{}

	tests := []struct {
		name string
		call func() error
	}{
		{name: "UpdateVIP", call: func() error { return ds.UpdateVIP(ctx, &models.VIP{}) }},
		{name: "DeleteVIP", call: func() error { return ds.DeleteVIP(ctx, "") }},
		{name: "AddBackend", call: func() error { return ds.AddBackend(ctx, &models.Backend{}) }},
		{name: "UpdateBackend", call: func() error { return ds.UpdateBackend(ctx, &models.Backend{}) }},
		{name: "DeleteBackend", call: func() error { return ds.DeleteBackend(ctx, "") }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, datastore.ErrInvalidInput) {
				t.Fatalf("error = %v, want %v", err, datastore.ErrInvalidInput)
			}
		})
	}
}

func TestEtcdDataStoreRejectsMalformedResourceIdentities(t *testing.T) {
	ctx := context.Background()
	ds := &EtcdDataStore{}

	tests := []struct {
		name string
		call func() error
	}{
		{name: "CreateVIP id", call: func() error {
			return ds.CreateVIP(ctx, &models.VIP{ID: "vip/1", VIP: "192.0.2.10", Port: 80, Protocol: models.ProtocolTCP})
		}},
		{name: "GetVIP id", call: func() error { _, err := ds.GetVIP(ctx, "vip/1"); return err }},
		{name: "UpdateVIP id", call: func() error {
			return ds.UpdateVIP(ctx, &models.VIP{ID: "vip/1", VIP: "192.0.2.10", Port: 80, Protocol: models.ProtocolTCP})
		}},
		{name: "DeleteVIP id", call: func() error { return ds.DeleteVIP(ctx, "vip/1") }},
		{name: "AddBackend id", call: func() error {
			return ds.AddBackend(ctx, &models.Backend{ID: "backend/1", VIPID: "vip-1", IP: "10.0.0.1", Weight: 1})
		}},
		{name: "AddBackend vip_id", call: func() error { return ds.AddBackend(ctx, &models.Backend{VIPID: "vip/1", IP: "10.0.0.1", Weight: 1}) }},
		{name: "GetBackend id", call: func() error { _, err := ds.GetBackend(ctx, "backend/1"); return err }},
		{name: "ListBackends vip_id", call: func() error { _, err := ds.ListBackends(ctx, "vip/1"); return err }},
		{name: "UpdateBackend id", call: func() error {
			return ds.UpdateBackend(ctx, &models.Backend{ID: "backend/1", VIPID: "vip-1", IP: "10.0.0.1", Weight: 1})
		}},
		{name: "DeleteBackend id", call: func() error { return ds.DeleteBackend(ctx, "backend/1") }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, datastore.ErrInvalidInput) {
				t.Fatalf("error = %v, want %v", err, datastore.ErrInvalidInput)
			}
		})
	}
}

func TestEtcdDataStoreRejectsInvalidMutationModels(t *testing.T) {
	ctx := context.Background()
	ds := &EtcdDataStore{}
	invalidDSCP := uint8(64)

	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "CreateVIP invalid address",
			call: func() error {
				return ds.CreateVIP(ctx, &models.VIP{
					VIP:      "invalid-ip",
					Port:     80,
					Protocol: models.ProtocolTCP,
				})
			},
		},
		{
			name: "CreateVIP invalid port",
			call: func() error {
				return ds.CreateVIP(ctx, &models.VIP{
					VIP:      "192.0.2.10",
					Port:     65536,
					Protocol: models.ProtocolTCP,
				})
			},
		},
		{
			name: "CreateVIP invalid protocol",
			call: func() error {
				return ds.CreateVIP(ctx, &models.VIP{
					VIP:      "192.0.2.10",
					Port:     80,
					Protocol: "SCTP",
				})
			},
		},
		{
			name: "CreateVIP invalid dscp",
			call: func() error {
				return ds.CreateVIP(ctx, &models.VIP{
					VIP:      "192.0.2.10",
					Port:     80,
					Protocol: models.ProtocolTCP,
					DSCP:     &invalidDSCP,
				})
			},
		},
		{
			name: "CreateVIP invalid health check",
			call: func() error {
				return ds.CreateVIP(ctx, &models.VIP{
					VIP:      "192.0.2.10",
					Port:     80,
					Protocol: models.ProtocolTCP,
					HealthCheck: &models.HealthCheck{
						Type:        models.HCTypeHTTP,
						IntervalSec: 10,
						TimeoutSec:  5,
						RiseCount:   3,
						FallCount:   2,
					},
				})
			},
		},
		{
			name: "UpdateVIP invalid model",
			call: func() error {
				return ds.UpdateVIP(ctx, &models.VIP{
					ID:       "vip-1",
					VIP:      "",
					Port:     80,
					Protocol: models.ProtocolTCP,
				})
			},
		},
		{
			name: "AddBackend invalid address",
			call: func() error {
				return ds.AddBackend(ctx, &models.Backend{
					VIPID:  "vip-1",
					IP:     "invalid-ip",
					Weight: 1,
				})
			},
		},
		{
			name: "AddBackend invalid weight",
			call: func() error {
				return ds.AddBackend(ctx, &models.Backend{
					VIPID:  "vip-1",
					IP:     "10.0.0.1",
					Weight: 101,
				})
			},
		},
		{
			name: "UpdateBackend invalid model",
			call: func() error {
				return ds.UpdateBackend(ctx, &models.Backend{
					ID:     "backend-1",
					VIPID:  "vip-1",
					IP:     "",
					Weight: 1,
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, datastore.ErrInvalidInput) {
				t.Fatalf("error = %v, want %v", err, datastore.ErrInvalidInput)
			}
		})
	}
}

func TestEtcdTransactionRejectsNilMutationInputs(t *testing.T) {
	ctx := context.Background()
	tx := &EtcdTransaction{}

	tests := []struct {
		name string
		call func() error
	}{
		{name: "CreateVIP", call: func() error { return tx.CreateVIP(ctx, nil) }},
		{name: "UpdateVIP", call: func() error { return tx.UpdateVIP(ctx, nil) }},
		{name: "AddBackend", call: func() error { return tx.AddBackend(ctx, nil) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, datastore.ErrInvalidInput) {
				t.Fatalf("error = %v, want %v", err, datastore.ErrInvalidInput)
			}
		})
	}
}

func TestEtcdTransactionRejectsEmptyMutationIdentities(t *testing.T) {
	ctx := context.Background()
	tx := &EtcdTransaction{}

	tests := []struct {
		name string
		call func() error
	}{
		{name: "UpdateVIP", call: func() error { return tx.UpdateVIP(ctx, &models.VIP{}) }},
		{name: "DeleteVIP", call: func() error { return tx.DeleteVIP(ctx, "") }},
		{name: "AddBackend", call: func() error { return tx.AddBackend(ctx, &models.Backend{}) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, datastore.ErrInvalidInput) {
				t.Fatalf("error = %v, want %v", err, datastore.ErrInvalidInput)
			}
		})
	}
}

func TestEtcdTransactionRejectsMalformedResourceIdentities(t *testing.T) {
	ctx := context.Background()
	tx := &EtcdTransaction{}

	tests := []struct {
		name string
		call func() error
	}{
		{name: "CreateVIP id", call: func() error {
			return tx.CreateVIP(ctx, &models.VIP{ID: "vip/1", VIP: "192.0.2.10", Port: 80, Protocol: models.ProtocolTCP})
		}},
		{name: "UpdateVIP id", call: func() error {
			return tx.UpdateVIP(ctx, &models.VIP{ID: "vip/1", VIP: "192.0.2.10", Port: 80, Protocol: models.ProtocolTCP})
		}},
		{name: "DeleteVIP id", call: func() error { return tx.DeleteVIP(ctx, "vip/1") }},
		{name: "AddBackend id", call: func() error {
			return tx.AddBackend(ctx, &models.Backend{ID: "backend/1", VIPID: "vip-1", IP: "10.0.0.1", Weight: 1})
		}},
		{name: "AddBackend vip_id", call: func() error { return tx.AddBackend(ctx, &models.Backend{VIPID: "vip/1", IP: "10.0.0.1", Weight: 1}) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, datastore.ErrInvalidInput) {
				t.Fatalf("error = %v, want %v", err, datastore.ErrInvalidInput)
			}
		})
	}
}

func TestEtcdTransactionRejectsInvalidMutationModels(t *testing.T) {
	ctx := context.Background()
	tx := &EtcdTransaction{}

	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "CreateVIP invalid model",
			call: func() error {
				return tx.CreateVIP(ctx, &models.VIP{
					VIP:      "invalid-ip",
					Port:     80,
					Protocol: models.ProtocolTCP,
				})
			},
		},
		{
			name: "UpdateVIP invalid model",
			call: func() error {
				return tx.UpdateVIP(ctx, &models.VIP{
					ID:       "vip-1",
					VIP:      "192.0.2.10",
					Port:     0,
					Protocol: models.ProtocolTCP,
				})
			},
		},
		{
			name: "AddBackend invalid model",
			call: func() error {
				return tx.AddBackend(ctx, &models.Backend{
					VIPID:  "vip-1",
					IP:     "10.0.0.1",
					Weight: 0,
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, datastore.ErrInvalidInput) {
				t.Fatalf("error = %v, want %v", err, datastore.ErrInvalidInput)
			}
		})
	}
}
