package healthcheck

import (
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func TestStateTracker_InitialState(t *testing.T) {
	logger := logrus.New()
	logger.SetOutput(logrus.StandardLogger().Out)
	st := NewStateTracker(logger)

	// Initially, backend should not exist
	state, exists := st.GetState("vip1", "backend1")
	assert.False(t, exists)
	assert.Equal(t, StateUnknown, state)
}

func TestStateTracker_UnknownToUp(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)
	st := NewStateTracker(logger)

	vipID := "vip1"
	backendID := "backend1"
	riseCount := 3
	fallCount := 2

	// First success - should stay Unknown
	result := ProbeResult{Success: true, Timestamp: time.Now()}
	prevState, newState, changed := st.UpdateProbeResult(vipID, backendID, result, riseCount, fallCount)
	assert.Equal(t, StateUnknown, prevState)
	assert.Equal(t, StateUnknown, newState)
	assert.False(t, changed)

	// Second success - should stay Unknown
	prevState, newState, changed = st.UpdateProbeResult(vipID, backendID, result, riseCount, fallCount)
	assert.Equal(t, StateUnknown, prevState)
	assert.Equal(t, StateUnknown, newState)
	assert.False(t, changed)

	// Third success - should transition to Up
	prevState, newState, changed = st.UpdateProbeResult(vipID, backendID, result, riseCount, fallCount)
	assert.Equal(t, StateUnknown, prevState)
	assert.Equal(t, StateUp, newState)
	assert.True(t, changed)

	// Verify state is now Up
	state, exists := st.GetState(vipID, backendID)
	assert.True(t, exists)
	assert.Equal(t, StateUp, state)
}

func TestStateTracker_UnknownToDown(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)
	st := NewStateTracker(logger)

	vipID := "vip1"
	backendID := "backend1"
	riseCount := 3
	fallCount := 2

	// First failure - should stay Unknown
	result := ProbeResult{Success: false, Timestamp: time.Now()}
	prevState, newState, changed := st.UpdateProbeResult(vipID, backendID, result, riseCount, fallCount)
	assert.Equal(t, StateUnknown, prevState)
	assert.Equal(t, StateUnknown, newState)
	assert.False(t, changed)

	// Second failure - should transition to Down
	prevState, newState, changed = st.UpdateProbeResult(vipID, backendID, result, riseCount, fallCount)
	assert.Equal(t, StateUnknown, prevState)
	assert.Equal(t, StateDown, newState)
	assert.True(t, changed)

	// Verify state is now Down
	state, exists := st.GetState(vipID, backendID)
	assert.True(t, exists)
	assert.Equal(t, StateDown, state)
}

func TestStateTracker_UpToDown(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)
	st := NewStateTracker(logger)

	vipID := "vip1"
	backendID := "backend1"
	riseCount := 3
	fallCount := 2

	// First transition to Up (3 successes)
	successResult := ProbeResult{Success: true, Timestamp: time.Now()}
	for i := 0; i < riseCount; i++ {
		st.UpdateProbeResult(vipID, backendID, successResult, riseCount, fallCount)
	}

	state, _ := st.GetState(vipID, backendID)
	assert.Equal(t, StateUp, state)

	// Now send failures
	failResult := ProbeResult{Success: false, Timestamp: time.Now()}

	// First failure - should stay Up
	prevState, newState, changed := st.UpdateProbeResult(vipID, backendID, failResult, riseCount, fallCount)
	assert.Equal(t, StateUp, prevState)
	assert.Equal(t, StateUp, newState)
	assert.False(t, changed)

	// Second failure - should transition to Down
	prevState, newState, changed = st.UpdateProbeResult(vipID, backendID, failResult, riseCount, fallCount)
	assert.Equal(t, StateUp, prevState)
	assert.Equal(t, StateDown, newState)
	assert.True(t, changed)

	// Verify state is now Down
	state, exists := st.GetState(vipID, backendID)
	assert.True(t, exists)
	assert.Equal(t, StateDown, state)
}

func TestStateTracker_DownToUp(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)
	st := NewStateTracker(logger)

	vipID := "vip1"
	backendID := "backend1"
	riseCount := 3
	fallCount := 2

	// First transition to Down (2 failures from Unknown)
	failResult := ProbeResult{Success: false, Timestamp: time.Now()}
	for i := 0; i < fallCount; i++ {
		st.UpdateProbeResult(vipID, backendID, failResult, riseCount, fallCount)
	}

	state, _ := st.GetState(vipID, backendID)
	assert.Equal(t, StateDown, state)

	// Now send successes
	successResult := ProbeResult{Success: true, Timestamp: time.Now()}

	// First two successes - should stay Down
	for i := 0; i < riseCount-1; i++ {
		prevState, newState, changed := st.UpdateProbeResult(vipID, backendID, successResult, riseCount, fallCount)
		assert.Equal(t, StateDown, prevState)
		assert.Equal(t, StateDown, newState)
		assert.False(t, changed)
	}

	// Third success - should transition to Up
	prevState, newState, changed := st.UpdateProbeResult(vipID, backendID, successResult, riseCount, fallCount)
	assert.Equal(t, StateDown, prevState)
	assert.Equal(t, StateUp, newState)
	assert.True(t, changed)

	// Verify state is now Up
	state, exists := st.GetState(vipID, backendID)
	assert.True(t, exists)
	assert.Equal(t, StateUp, state)
}

func TestStateTracker_ConsecutiveCountsResetOnMixedResults(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)
	st := NewStateTracker(logger)

	vipID := "vip1"
	backendID := "backend1"
	riseCount := 3
	fallCount := 2

	// Two successes
	successResult := ProbeResult{Success: true, Timestamp: time.Now()}
	st.UpdateProbeResult(vipID, backendID, successResult, riseCount, fallCount)
	st.UpdateProbeResult(vipID, backendID, successResult, riseCount, fallCount)

	// One failure - this should reset consecutive up count
	failResult := ProbeResult{Success: false, Timestamp: time.Now()}
	st.UpdateProbeResult(vipID, backendID, failResult, riseCount, fallCount)

	// Should still be Unknown (need 3 consecutive successes or 2 consecutive failures)
	state, _ := st.GetState(vipID, backendID)
	assert.Equal(t, StateUnknown, state)

	// Now need 3 more consecutive successes to transition to Up
	for i := 0; i < riseCount; i++ {
		st.UpdateProbeResult(vipID, backendID, successResult, riseCount, fallCount)
	}

	state, _ = st.GetState(vipID, backendID)
	assert.Equal(t, StateUp, state)
}

func TestStateTracker_ConsecutiveCountsResetAfterStateChange(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)
	st := NewStateTracker(logger)

	vipID := "vip1"
	backendID := "backend1"
	riseCount := 3
	fallCount := 2

	// Transition to Up
	successResult := ProbeResult{Success: true, Timestamp: time.Now()}
	for i := 0; i < riseCount; i++ {
		st.UpdateProbeResult(vipID, backendID, successResult, riseCount, fallCount)
	}

	// Get health state to check consecutive counts
	healthState, _ := st.GetHealthState(vipID, backendID)
	assert.Equal(t, StateUp, healthState.State)
	// After state change, consecutive counts should be reset to 0
	assert.Equal(t, 0, healthState.ConsecutiveUp)
	assert.Equal(t, 0, healthState.ConsecutiveDown)
}

func TestStateTracker_RemoveBackend(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)
	st := NewStateTracker(logger)

	vipID := "vip1"
	backendID := "backend1"
	riseCount := 3
	fallCount := 2

	// Add backend state
	result := ProbeResult{Success: true, Timestamp: time.Now()}
	st.UpdateProbeResult(vipID, backendID, result, riseCount, fallCount)

	// Verify it exists
	_, exists := st.GetState(vipID, backendID)
	assert.True(t, exists)

	// Remove backend
	st.RemoveBackend(vipID, backendID)

	// Verify it's gone
	_, exists = st.GetState(vipID, backendID)
	assert.False(t, exists)
}

func TestStateTracker_RemoveVIP(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)
	st := NewStateTracker(logger)

	vipID := "vip1"
	riseCount := 3
	fallCount := 2

	// Add multiple backends for the VIP
	result := ProbeResult{Success: true, Timestamp: time.Now()}
	st.UpdateProbeResult(vipID, "backend1", result, riseCount, fallCount)
	st.UpdateProbeResult(vipID, "backend2", result, riseCount, fallCount)
	st.UpdateProbeResult(vipID, "backend3", result, riseCount, fallCount)

	// Add backends for a different VIP
	st.UpdateProbeResult("vip2", "backend4", result, riseCount, fallCount)

	// Verify all backends exist
	_, exists1 := st.GetState(vipID, "backend1")
	_, exists2 := st.GetState(vipID, "backend2")
	_, exists3 := st.GetState(vipID, "backend3")
	_, exists4 := st.GetState("vip2", "backend4")
	assert.True(t, exists1)
	assert.True(t, exists2)
	assert.True(t, exists3)
	assert.True(t, exists4)

	// Remove VIP1
	st.RemoveVIP(vipID)

	// Verify VIP1 backends are gone
	_, exists1 = st.GetState(vipID, "backend1")
	_, exists2 = st.GetState(vipID, "backend2")
	_, exists3 = st.GetState(vipID, "backend3")
	assert.False(t, exists1)
	assert.False(t, exists2)
	assert.False(t, exists3)

	// Verify VIP2 backend still exists
	_, exists4 = st.GetState("vip2", "backend4")
	assert.True(t, exists4)
}

func TestStateTracker_GetAllStates(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)
	st := NewStateTracker(logger)

	riseCount := 3
	fallCount := 2

	// Add multiple backends
	result := ProbeResult{Success: true, Timestamp: time.Now()}
	st.UpdateProbeResult("vip1", "backend1", result, riseCount, fallCount)
	st.UpdateProbeResult("vip1", "backend2", result, riseCount, fallCount)
	st.UpdateProbeResult("vip2", "backend3", result, riseCount, fallCount)

	// Get all states
	allStates := st.GetAllStates()

	// Should have 3 entries
	assert.Equal(t, 3, len(allStates))

	// Verify keys exist
	_, exists1 := allStates["vip1:backend1"]
	_, exists2 := allStates["vip1:backend2"]
	_, exists3 := allStates["vip2:backend3"]
	assert.True(t, exists1)
	assert.True(t, exists2)
	assert.True(t, exists3)
}

func TestStateTracker_ConcurrentAccess(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.WarnLevel) // Reduce log noise for concurrent test
	st := NewStateTracker(logger)

	vipID := "vip1"
	backendID := "backend1"
	riseCount := 3
	fallCount := 2

	// Run concurrent updates and reads
	done := make(chan bool)

	// Writer goroutine
	go func() {
		for i := 0; i < 100; i++ {
			result := ProbeResult{Success: i%2 == 0, Timestamp: time.Now()}
			st.UpdateProbeResult(vipID, backendID, result, riseCount, fallCount)
		}
		done <- true
	}()

	// Reader goroutine
	go func() {
		for i := 0; i < 100; i++ {
			st.GetState(vipID, backendID)
			st.GetHealthState(vipID, backendID)
		}
		done <- true
	}()

	// Wait for both to complete
	<-done
	<-done

	// If we got here without a race condition panic, the test passes
}
