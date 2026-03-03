package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func tempStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%s): %v", path, err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return st
}

func TestOpenAndClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// File should exist
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("database file not created")
	}
}

func TestSaveAndLoadHealthState(t *testing.T) {
	st := tempStore(t)

	rec := &BackendHealthRecord{
		State:           "up",
		ConsecutiveUp:   3,
		ConsecutiveDown: 0,
		LastProbeTime:   time.Now().Truncate(time.Millisecond),
		LastStateChange: time.Now().Add(-5 * time.Minute).Truncate(time.Millisecond),
	}

	if err := st.SaveHealthState("web-vip", "10.0.1.1", rec); err != nil {
		t.Fatalf("SaveHealthState: %v", err)
	}

	loaded, err := st.LoadHealthState("web-vip", "10.0.1.1")
	if err != nil {
		t.Fatalf("LoadHealthState: %v", err)
	}
	if loaded == nil {
		t.Fatal("LoadHealthState returned nil")
	}
	if loaded.State != rec.State {
		t.Errorf("State = %q, want %q", loaded.State, rec.State)
	}
	if loaded.ConsecutiveUp != rec.ConsecutiveUp {
		t.Errorf("ConsecutiveUp = %d, want %d", loaded.ConsecutiveUp, rec.ConsecutiveUp)
	}
	if loaded.ConsecutiveDown != rec.ConsecutiveDown {
		t.Errorf("ConsecutiveDown = %d, want %d", loaded.ConsecutiveDown, rec.ConsecutiveDown)
	}
}

func TestLoadHealthState_NotFound(t *testing.T) {
	st := tempStore(t)

	rec, err := st.LoadHealthState("nonexistent-vip", "1.2.3.4")
	if err != nil {
		t.Fatalf("LoadHealthState: %v", err)
	}
	if rec != nil {
		t.Fatal("expected nil for non-existent record")
	}
}

func TestDeleteHealthState(t *testing.T) {
	st := tempStore(t)

	rec := &BackendHealthRecord{State: "down"}
	if err := st.SaveHealthState("vip1", "10.0.0.1", rec); err != nil {
		t.Fatal(err)
	}

	if err := st.DeleteHealthState("vip1", "10.0.0.1"); err != nil {
		t.Fatal(err)
	}

	loaded, err := st.LoadHealthState("vip1", "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded != nil {
		t.Fatal("expected nil after delete")
	}
}

func TestDeleteHealthStatesForVIP(t *testing.T) {
	st := tempStore(t)

	// Save multiple backends for same VIP
	if err := st.SaveHealthState("vip1", "10.0.0.1", &BackendHealthRecord{State: "up"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveHealthState("vip1", "10.0.0.2", &BackendHealthRecord{State: "down"}); err != nil {
		t.Fatal(err)
	}
	// Save backend for different VIP
	if err := st.SaveHealthState("vip2", "10.0.1.1", &BackendHealthRecord{State: "up"}); err != nil {
		t.Fatal(err)
	}

	if err := st.DeleteHealthStatesForVIP("vip1"); err != nil {
		t.Fatal(err)
	}

	// vip1 backends should be gone
	for _, addr := range []string{"10.0.0.1", "10.0.0.2"} {
		rec, _ := st.LoadHealthState("vip1", addr)
		if rec != nil {
			t.Errorf("vip1/%s should be deleted", addr)
		}
	}

	// vip2 backend should remain
	rec, _ := st.LoadHealthState("vip2", "10.0.1.1")
	if rec == nil {
		t.Error("vip2/10.0.1.1 should not be deleted")
	}
}

func TestLoadAllHealthStates(t *testing.T) {
	st := tempStore(t)

	if err := st.SaveHealthState("vip1", "10.0.0.1", &BackendHealthRecord{State: "up"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveHealthState("vip1", "10.0.0.2", &BackendHealthRecord{State: "down"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveHealthState("vip2", "10.0.1.1", &BackendHealthRecord{State: "up"}); err != nil {
		t.Fatal(err)
	}

	all, err := st.LoadAllHealthStates()
	if err != nil {
		t.Fatal(err)
	}

	if len(all) != 3 {
		t.Errorf("got %d records, want 3", len(all))
	}
}

func TestSaveAndLoadLastConfig(t *testing.T) {
	st := tempStore(t)

	config := []byte(`{"address":"10.0.0.1","port":80}`)
	if err := st.SaveLastConfig("vip1", config); err != nil {
		t.Fatal(err)
	}

	loaded, err := st.LoadLastConfig("vip1")
	if err != nil {
		t.Fatal(err)
	}
	if string(loaded) != string(config) {
		t.Errorf("LoadLastConfig = %q, want %q", loaded, config)
	}
}

func TestLoadLastConfig_NotFound(t *testing.T) {
	st := tempStore(t)

	loaded, err := st.LoadLastConfig("nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if loaded != nil {
		t.Fatal("expected nil for non-existent config")
	}
}

func TestDeleteLastConfig(t *testing.T) {
	st := tempStore(t)

	if err := st.SaveLastConfig("vip1", []byte("data")); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteLastConfig("vip1"); err != nil {
		t.Fatal(err)
	}
	loaded, err := st.LoadLastConfig("vip1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded != nil {
		t.Fatal("expected nil after delete")
	}
}
