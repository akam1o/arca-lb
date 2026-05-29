package datastore

import (
	"context"
	"testing"
	"time"
)

func TestSendWatchEventSendsWhenChannelReady(t *testing.T) {
	eventChan := make(chan WatchEvent, 1)
	event := WatchEvent{Type: EventTypeVIPCreated, Revision: 10}

	if ok := SendWatchEvent(context.Background(), eventChan, event); !ok {
		t.Fatal("expected send to succeed")
	}

	got := <-eventChan
	if got.Type != event.Type || got.Revision != event.Revision {
		t.Fatalf("unexpected event: got %+v, want %+v", got, event)
	}
}

func TestSendWatchEventReturnsFalseWhenContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if ok := SendWatchEvent(ctx, make(chan WatchEvent), WatchEvent{}); ok {
		t.Fatal("expected send to stop after context cancellation")
	}
}

func TestSendWatchEventUnblocksOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	eventChan := make(chan WatchEvent)
	done := make(chan bool, 1)

	go func() {
		done <- SendWatchEvent(ctx, eventChan, WatchEvent{})
	}()

	cancel()

	select {
	case ok := <-done:
		if ok {
			t.Fatal("expected send to stop after context cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for SendWatchEvent to unblock")
	}
}
