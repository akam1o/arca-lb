package healthcheck

import (
	"github.com/akam1o/arca-lb/internal/common/models"
	"github.com/sirupsen/logrus"
)

// VPPSyncer is the interface for VPP synchronization operations
// This is used to add/remove backends from VPP based on health status
type VPPSyncer interface {
	AddBackend(vipID string, backend *models.Backend) error
	DeleteBackend(vipID string, backendID string) error
}

// StateChangeCallback is called when a backend health state changes
// vipAddress parameter added for FRR integration (BGP route management)
type StateChangeCallback func(vipID, backendID string, oldState, newState BackendState, backend *models.Backend, vipAddress string)

// VPPStateChangeCallback creates a callback that integrates with VPP
// When a backend transitions to UP, it's added to VPP
// When a backend transitions to DOWN, it's removed from VPP
func VPPStateChangeCallback(vppSyncer VPPSyncer, logger *logrus.Logger) StateChangeCallback {
	return func(vipID, backendID string, oldState, newState BackendState, backend *models.Backend, vipAddress string) {
		logger.WithFields(logrus.Fields{
			"vip_id":     vipID,
			"backend_id": backendID,
			"old_state":  oldState,
			"new_state":  newState,
		}).Info("Backend health state changed")

		switch newState {
		case StateUp:
			// Backend is now healthy - add to VPP
			if oldState != StateUp {
				logger.WithFields(logrus.Fields{
					"vip_id":     vipID,
					"backend_id": backendID,
					"backend_ip": backend.IP,
				}).Info("Adding healthy backend to VPP")

				if err := vppSyncer.AddBackend(vipID, backend); err != nil {
					logger.WithFields(logrus.Fields{
						"vip_id":     vipID,
						"backend_id": backendID,
						"error":      err,
					}).Error("Failed to add backend to VPP")
				} else {
					logger.WithFields(logrus.Fields{
						"vip_id":     vipID,
						"backend_id": backendID,
					}).Info("Successfully added backend to VPP")
				}
			}

		case StateDown:
			// Backend is now unhealthy - remove from VPP
			if oldState == StateUp {
				logger.WithFields(logrus.Fields{
					"vip_id":     vipID,
					"backend_id": backendID,
					"backend_ip": backend.IP,
				}).Info("Removing unhealthy backend from VPP")

				if err := vppSyncer.DeleteBackend(vipID, backendID); err != nil {
					logger.WithFields(logrus.Fields{
						"vip_id":     vipID,
						"backend_id": backendID,
						"error":      err,
					}).Error("Failed to remove backend from VPP")
				} else {
					logger.WithFields(logrus.Fields{
						"vip_id":     vipID,
						"backend_id": backendID,
					}).Info("Successfully removed backend from VPP")
				}
			}

		case StateUnknown:
			// Backend state is unknown - typically shouldn't happen after initial probe
			logger.WithFields(logrus.Fields{
				"vip_id":     vipID,
				"backend_id": backendID,
			}).Warn("Backend state changed to unknown")
		}
	}
}

// FRRManager interface for FRR BGP route management
type FRRManager interface {
	UpdateBackendHealth(vipID, backendID, vipAddress string, oldState, newState BackendState) error
}

// CompositeStateChangeCallback creates a callback that dispatches to both VPP and FRR handlers
// This allows independent management of data plane (VPP) and control plane (FRR BGP)
func CompositeStateChangeCallback(vppSyncer VPPSyncer, frrManager FRRManager, logger *logrus.Logger) StateChangeCallback {
	return func(vipID, backendID string, oldState, newState BackendState, backend *models.Backend, vipAddress string) {
		logger.WithFields(logrus.Fields{
			"vip_id":      vipID,
			"backend_id":  backendID,
			"old_state":   oldState,
			"new_state":   newState,
			"vip_address": vipAddress,
		}).Debug("Backend state changed, updating VPP and FRR")

		// Handler 1: VPP (data plane) - only if VPP is enabled
		// Must execute first to ensure packet forwarding is ready before BGP announcement
		if vppSyncer != nil {
			if newState == StateUp && oldState != StateUp {
				// Backend became healthy - add to VPP
				if err := vppSyncer.AddBackend(vipID, backend); err != nil {
					logger.WithError(err).WithFields(logrus.Fields{
						"vip_id":     vipID,
						"backend_id": backendID,
					}).Error("Failed to add backend to VPP")
					// Continue to FRR handler despite VPP failure
				} else {
					logger.WithFields(logrus.Fields{
						"vip_id":     vipID,
						"backend_id": backendID,
					}).Info("Backend added to VPP")
				}
			} else if newState == StateDown && oldState == StateUp {
				// Backend became unhealthy - remove from VPP
				if err := vppSyncer.DeleteBackend(vipID, backendID); err != nil {
					logger.WithError(err).WithFields(logrus.Fields{
						"vip_id":     vipID,
						"backend_id": backendID,
					}).Error("Failed to delete backend from VPP")
					// Continue to FRR handler despite VPP failure
				} else {
					logger.WithFields(logrus.Fields{
						"vip_id":     vipID,
						"backend_id": backendID,
					}).Info("Backend removed from VPP")
				}
			}
		}

		// Handler 2: FRR (control plane)
		// Execute after VPP to ensure data plane is ready before control plane announces
		if frrManager != nil {
			if err := frrManager.UpdateBackendHealth(vipID, backendID, vipAddress, oldState, newState); err != nil {
				logger.WithError(err).WithFields(logrus.Fields{
					"vip_id":      vipID,
					"backend_id":  backendID,
					"vip_address": vipAddress,
				}).Error("Failed to update FRR route announcement")
				// Log but don't propagate error - FRR failure shouldn't stop health checks
			}
		}
	}
}
