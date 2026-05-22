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
