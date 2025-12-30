package healthcheck

import (
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// BackendState represents the health state of a backend
type BackendState string

const (
	// StateUnknown indicates the backend health is unknown (initial state)
	StateUnknown BackendState = "unknown"

	// StateUp indicates the backend is healthy
	StateUp BackendState = "up"

	// StateDown indicates the backend is unhealthy
	StateDown BackendState = "down"
)

// BackendHealthState tracks the health state of a single backend
type BackendHealthState struct {
	// State is the current health state
	State BackendState

	// ConsecutiveUp is the count of consecutive successful probes
	ConsecutiveUp int

	// ConsecutiveDown is the count of consecutive failed probes
	ConsecutiveDown int

	// LastProbeResult is the result of the most recent probe
	LastProbeResult ProbeResult

	// LastStateChange is when the state last changed
	LastStateChange time.Time
}

// StateTracker tracks health state for all backends across all VIPs
type StateTracker struct {
	mu     sync.RWMutex
	logger *logrus.Logger

	// states maps "vipID:backendID" to BackendHealthState
	states map[string]*BackendHealthState
}

// NewStateTracker creates a new StateTracker
func NewStateTracker(logger *logrus.Logger) *StateTracker {
	return &StateTracker{
		logger: logger,
		states: make(map[string]*BackendHealthState),
	}
}

// getKey returns the map key for a VIP-backend pair
func getKey(vipID, backendID string) string {
	return vipID + ":" + backendID
}

// GetState returns the current state of a backend
func (st *StateTracker) GetState(vipID, backendID string) (BackendState, bool) {
	st.mu.RLock()
	defer st.mu.RUnlock()

	key := getKey(vipID, backendID)
	state, exists := st.states[key]
	if !exists {
		return StateUnknown, false
	}
	return state.State, true
}

// GetHealthState returns the full health state of a backend
func (st *StateTracker) GetHealthState(vipID, backendID string) (*BackendHealthState, bool) {
	st.mu.RLock()
	defer st.mu.RUnlock()

	key := getKey(vipID, backendID)
	state, exists := st.states[key]
	if !exists {
		return nil, false
	}

	// Return a copy to avoid race conditions
	stateCopy := *state
	return &stateCopy, true
}

// UpdateProbeResult updates the state based on a probe result
// Returns: (previousState, newState, stateChanged)
func (st *StateTracker) UpdateProbeResult(
	vipID, backendID string,
	result ProbeResult,
	riseCount, fallCount int,
) (BackendState, BackendState, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()

	key := getKey(vipID, backendID)
	state, exists := st.states[key]
	if !exists {
		// Initialize new state
		state = &BackendHealthState{
			State:           StateUnknown,
			ConsecutiveUp:   0,
			ConsecutiveDown: 0,
			LastStateChange: time.Now(),
		}
		st.states[key] = state
	}

	prevState := state.State
	state.LastProbeResult = result

	// Update consecutive counts based on probe result
	if result.Success {
		state.ConsecutiveUp++
		state.ConsecutiveDown = 0
	} else {
		state.ConsecutiveDown++
		state.ConsecutiveUp = 0
	}

	// Determine new state based on rise/fall thresholds
	newState := prevState
	switch prevState {
	case StateUnknown:
		// From Unknown, need riseCount successes to go Up
		if state.ConsecutiveUp >= riseCount {
			newState = StateUp
		} else if state.ConsecutiveDown >= fallCount {
			// Can also go directly to Down if we hit fallCount failures
			newState = StateDown
		}

	case StateUp:
		// From Up, need fallCount failures to go Down
		if state.ConsecutiveDown >= fallCount {
			newState = StateDown
		}

	case StateDown:
		// From Down, need riseCount successes to go Up
		if state.ConsecutiveUp >= riseCount {
			newState = StateUp
		}
	}

	// Update state if changed
	stateChanged := newState != prevState
	if stateChanged {
		st.logger.WithFields(logrus.Fields{
			"vip_id":      vipID,
			"backend_id":  backendID,
			"prev_state":  prevState,
			"new_state":   newState,
			"consec_up":   state.ConsecutiveUp,
			"consec_down": state.ConsecutiveDown,
		}).Info("Backend health state changed")

		state.State = newState
		state.LastStateChange = time.Now()

		// Reset consecutive counts after state change
		// This prevents immediate re-transition
		state.ConsecutiveUp = 0
		state.ConsecutiveDown = 0
	}

	return prevState, newState, stateChanged
}

// RemoveBackend removes a backend from tracking
func (st *StateTracker) RemoveBackend(vipID, backendID string) {
	st.mu.Lock()
	defer st.mu.Unlock()

	key := getKey(vipID, backendID)
	delete(st.states, key)

	st.logger.WithFields(logrus.Fields{
		"vip_id":     vipID,
		"backend_id": backendID,
	}).Debug("Removed backend from health state tracking")
}

// RemoveVIP removes all backends for a VIP from tracking
func (st *StateTracker) RemoveVIP(vipID string) {
	st.mu.Lock()
	defer st.mu.Unlock()

	// Find and remove all backends for this VIP
	keysToRemove := []string{}
	for key := range st.states {
		// Keys are in format "vipID:backendID"
		// We need to check if the key starts with vipID:
		prefix := vipID + ":"
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			keysToRemove = append(keysToRemove, key)
		}
	}

	for _, key := range keysToRemove {
		delete(st.states, key)
	}

	st.logger.WithFields(logrus.Fields{
		"vip_id":           vipID,
		"backends_removed": len(keysToRemove),
	}).Debug("Removed VIP from health state tracking")
}

// GetAllStates returns a snapshot of all current states
func (st *StateTracker) GetAllStates() map[string]BackendHealthState {
	st.mu.RLock()
	defer st.mu.RUnlock()

	// Return a deep copy to avoid race conditions
	snapshot := make(map[string]BackendHealthState, len(st.states))
	for key, state := range st.states {
		snapshot[key] = *state
	}

	return snapshot
}
