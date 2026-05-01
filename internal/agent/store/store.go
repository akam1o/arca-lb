// Package store provides local persistent state for the agent using bbolt.
// It stores health check state and the last-applied VIP configuration
// so the agent can recover gracefully after restart.
package store

import (
	"encoding/json"
	"fmt"
	"time"

	bolt "go.etcd.io/bbolt"
)

var (
	bucketHCState    = []byte("hc_state")
	bucketLastConfig = []byte("last_config")
)

// BackendHealthRecord is the persistent health state of a single backend.
type BackendHealthRecord struct {
	State           string    `json:"state"` // "up", "down", "unknown"
	ConsecutiveUp   int       `json:"consecutive_up"`
	ConsecutiveDown int       `json:"consecutive_down"`
	LastProbeTime   time.Time `json:"last_probe_time"`
	LastStateChange time.Time `json:"last_state_change"`
}

// Store wraps bbolt for local agent state persistence.
type Store struct {
	db *bolt.DB
}

// Open creates or opens a bbolt database at path.
func Open(path string) (*Store, error) {
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("failed to open store at %s: %w", path, err)
	}

	// Ensure buckets exist
	if err := db.Update(func(tx *bolt.Tx) error {
		for _, b := range [][]byte{bucketHCState, bucketLastConfig} {
			if _, err := tx.CreateBucketIfNotExists(b); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		_ = db.Close()
		return nil, err
	}

	return &Store{db: db}, nil
}

// Close closes the bbolt database.
func (s *Store) Close() error {
	return s.db.Close()
}

// --- Health Check State ---

// hcKey produces a deterministic key from namespaced VIP key and backend address.
func hcKey(vipKey, backendAddr string) []byte {
	return []byte(vipKey + "/" + backendAddr)
}

// SaveHealthState persists a backend's health record.
func (s *Store) SaveHealthState(vipKey, backendAddr string, rec *BackendHealthRecord) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketHCState).Put(hcKey(vipKey, backendAddr), data)
	})
}

// LoadHealthState loads a backend's health record, returning nil if not found.
func (s *Store) LoadHealthState(vipKey, backendAddr string) (*BackendHealthRecord, error) {
	var rec BackendHealthRecord
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(bucketHCState).Get(hcKey(vipKey, backendAddr))
		if v == nil {
			return nil
		}
		return json.Unmarshal(v, &rec)
	})
	if err != nil {
		return nil, err
	}
	if rec.State == "" {
		return nil, nil // not found
	}
	return &rec, nil
}

// LoadAllHealthStates returns all persisted health records keyed by "namespace/vip/backend".
func (s *Store) LoadAllHealthStates() (map[string]*BackendHealthRecord, error) {
	result := make(map[string]*BackendHealthRecord)
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketHCState)
		return b.ForEach(func(k, v []byte) error {
			var rec BackendHealthRecord
			if err := json.Unmarshal(v, &rec); err != nil {
				return err
			}
			result[string(k)] = &rec
			return nil
		})
	})
	return result, err
}

// DeleteHealthState removes a backend's health record.
func (s *Store) DeleteHealthState(vipKey, backendAddr string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketHCState).Delete(hcKey(vipKey, backendAddr))
	})
}

// DeleteHealthStatesForVIP removes all health records for a VIP.
func (s *Store) DeleteHealthStatesForVIP(vipKey string) error {
	prefix := []byte(vipKey + "/")
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketHCState)
		c := b.Cursor()
		for k, _ := c.Seek(prefix); k != nil && len(k) >= len(prefix) && string(k[:len(prefix)]) == string(prefix); k, _ = c.Next() {
			if err := b.Delete(k); err != nil {
				return err
			}
		}
		return nil
	})
}

// --- Last Applied Config ---

// SaveLastConfig persists the raw JSON of the last-applied VIP config.
func (s *Store) SaveLastConfig(vipKey string, data []byte) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketLastConfig).Put([]byte(vipKey), data)
	})
}

// LoadLastConfig loads the last-applied config for a VIP.
func (s *Store) LoadLastConfig(vipKey string) ([]byte, error) {
	var result []byte
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(bucketLastConfig).Get([]byte(vipKey))
		if v != nil {
			result = make([]byte, len(v))
			copy(result, v)
		}
		return nil
	})
	return result, err
}

// LoadAllLastConfigs returns all persisted last-applied VIP configs keyed by
// "namespace/name".
func (s *Store) LoadAllLastConfigs() (map[string][]byte, error) {
	result := make(map[string][]byte)
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketLastConfig)
		return b.ForEach(func(k, v []byte) error {
			value := make([]byte, len(v))
			copy(value, v)
			result[string(k)] = value
			return nil
		})
	})
	return result, err
}

// DeleteLastConfig removes the last-applied config for a VIP.
func (s *Store) DeleteLastConfig(vipKey string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketLastConfig).Delete([]byte(vipKey))
	})
}
