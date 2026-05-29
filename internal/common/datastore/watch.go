package datastore

import "context"

// SendWatchEvent sends a watch event unless the watch context is cancelled.
func SendWatchEvent(ctx context.Context, eventChan chan<- WatchEvent, event WatchEvent) bool {
	select {
	case eventChan <- event:
		return true
	case <-ctx.Done():
		return false
	}
}
