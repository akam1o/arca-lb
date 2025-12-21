package etcd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/akam1o/arca-lb/internal/common/datastore"
	"github.com/akam1o/arca-lb/internal/common/models"
	clientv3 "go.etcd.io/etcd/client/v3"
)

// Watch watches for changes in VIPs and Backends
func (ds *EtcdDataStore) Watch(ctx context.Context) (<-chan datastore.WatchEvent, error) {
	eventChan := make(chan datastore.WatchEvent, 100)

	go func() {
		defer close(eventChan)

		// Watch VIPs
		vipWatchChan := ds.client.Watch(ctx, ds.vipPrefix(), clientv3.WithPrefix())
		// Watch Backends
		backendWatchChan := ds.client.Watch(ctx, fmt.Sprintf("%s/backends/", ds.keyPrefix), clientv3.WithPrefix())

		for {
			select {
			case <-ctx.Done():
				return

			case watchResp, ok := <-vipWatchChan:
				if !ok {
					eventChan <- datastore.WatchEvent{
						Type:  datastore.EventTypeError,
						Error: fmt.Errorf("VIP watch channel closed"),
					}
					return
				}

				if watchResp.Err() != nil {
					eventChan <- datastore.WatchEvent{
						Type:  datastore.EventTypeError,
						Error: fmt.Errorf("VIP watch error: %w", watchResp.Err()),
					}
					continue
				}

				for _, event := range watchResp.Events {
					if err := ds.handleVIPEvent(event, eventChan); err != nil {
						eventChan <- datastore.WatchEvent{
							Type:  datastore.EventTypeError,
							Error: fmt.Errorf("failed to handle VIP event: %w", err),
						}
					}
				}

			case watchResp, ok := <-backendWatchChan:
				if !ok {
					eventChan <- datastore.WatchEvent{
						Type:  datastore.EventTypeError,
						Error: fmt.Errorf("backend watch channel closed"),
					}
					return
				}

				if watchResp.Err() != nil {
					eventChan <- datastore.WatchEvent{
						Type:  datastore.EventTypeError,
						Error: fmt.Errorf("backend watch error: %w", watchResp.Err()),
					}
					continue
				}

				for _, event := range watchResp.Events {
					if err := ds.handleBackendEvent(event, eventChan); err != nil {
						eventChan <- datastore.WatchEvent{
							Type:  datastore.EventTypeError,
							Error: fmt.Errorf("failed to handle backend event: %w", err),
						}
					}
				}
			}
		}
	}()

	return eventChan, nil
}

// handleVIPEvent handles VIP watch events
func (ds *EtcdDataStore) handleVIPEvent(event *clientv3.Event, eventChan chan<- datastore.WatchEvent) error {
	var vip models.VIP

	switch event.Type {
	case clientv3.EventTypePut:
		if err := json.Unmarshal(event.Kv.Value, &vip); err != nil {
			return fmt.Errorf("failed to unmarshal VIP: %w", err)
		}

		// Determine if it's a create or update by checking if it's a new key
		eventType := datastore.EventTypeVIPUpdated
		if event.IsCreate() {
			eventType = datastore.EventTypeVIPCreated
		}

		// Get current revision
		revision, _ := ds.GetRevision(context.Background())

		eventChan <- datastore.WatchEvent{
			Type:     eventType,
			Revision: revision,
			VIP:      &vip,
		}

	case clientv3.EventTypeDelete:
		// Extract VIP ID from key
		vipID := extractVIPIDFromKey(string(event.Kv.Key), ds.vipPrefix())

		// Get current revision
		revision, _ := ds.GetRevision(context.Background())

		eventChan <- datastore.WatchEvent{
			Type:     datastore.EventTypeVIPDeleted,
			Revision: revision,
			VIP:      &models.VIP{ID: vipID},
		}
	}

	return nil
}

// handleBackendEvent handles Backend watch events
func (ds *EtcdDataStore) handleBackendEvent(event *clientv3.Event, eventChan chan<- datastore.WatchEvent) error {
	var backend models.Backend

	switch event.Type {
	case clientv3.EventTypePut:
		if err := json.Unmarshal(event.Kv.Value, &backend); err != nil {
			return fmt.Errorf("failed to unmarshal backend: %w", err)
		}

		// Determine if it's an add or update
		eventType := datastore.EventTypeBackendUpdated
		if event.IsCreate() {
			eventType = datastore.EventTypeBackendAdded
		}

		// Get current revision
		revision, _ := ds.GetRevision(context.Background())

		eventChan <- datastore.WatchEvent{
			Type:     eventType,
			Revision: revision,
			Backend:  &backend,
		}

	case clientv3.EventTypeDelete:
		// Extract backend info from key
		vipID, backendID := extractBackendIDsFromKey(string(event.Kv.Key), ds.keyPrefix)

		// Get current revision
		revision, _ := ds.GetRevision(context.Background())

		eventChan <- datastore.WatchEvent{
			Type:     datastore.EventTypeBackendDeleted,
			Revision: revision,
			Backend:  &models.Backend{ID: backendID, VIPID: vipID},
		}
	}

	return nil
}

// extractVIPIDFromKey extracts VIP ID from etcd key
func extractVIPIDFromKey(key, prefix string) string {
	return strings.TrimPrefix(key, prefix)
}

// extractBackendIDsFromKey extracts VIP ID and Backend ID from etcd key
// Expected format: /arca-lb/backends/{vipID}/{backendID}
func extractBackendIDsFromKey(key, keyPrefix string) (vipID, backendID string) {
	prefix := fmt.Sprintf("%s/backends/", keyPrefix)
	path := strings.TrimPrefix(key, prefix)
	parts := strings.Split(path, "/")

	if len(parts) >= 2 {
		return parts[0], parts[1]
	}

	return "", ""
}
