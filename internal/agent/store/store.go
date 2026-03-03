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

// hcKey produces a deterministic key from VIP name and backend address.
func hcKey(vipName, backendAddr string) []byte {
	return []byte(vipName + "/" + backendAddr)
}

// SaveHealthState persists a backend's health record.
func (s *Store) SaveHealthState(vipName, backendAddr string, rec *BackendHealthRecord) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketHCState).Put(hcKey(vipName, backendAddr), data)
	})
}

// LoadHealthState loads a backend's health record, returning nil if not found.
func (s *Store) LoadHealthState(vipName, backendAddr string) (*BackendHealthRecord, error) {
	var rec BackendHealthRecord
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(bucketHCState).Get(hcKey(vipName, backendAddr))
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

// LoadAllHealthStates returns all persisted health records keyed by "vip/backend".
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
func (s *Store) DeleteHealthState(vipName, backendAddr string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketHCState).Delete(hcKey(vipName, backendAddr))
	})
}

// DeleteHealthStatesForVIP removes all health records for a VIP.
func (s *Store) DeleteHealthStatesForVIP(vipName string) error {
	prefix := []byte(vipName + "/")
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
func (s *Store) SaveLastConfig(vipName string, data []byte) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketLastConfig).Put([]byte(vipName), data)
	})
}

// LoadLastConfig loads the last-applied config for a VIP.
func (s *Store) LoadLastConfig(vipName string) ([]byte, error) {
	var result []byte
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(bucketLastConfig).Get([]byte(vipName))
		if v != nil {
			result = make([]byte, len(v))
			copy(result, v)
		}
		return nil
	})
	return result, err
}

// DeleteLastConfig removes the last-applied config for a VIP.
func (s *Store) DeleteLastConfig(vipName string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketLastConfig).Delete([]byte(vipName))
	})
}
