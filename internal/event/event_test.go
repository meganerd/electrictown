package event

import (
	"sync"
	"testing"
	"time"
)

func TestBusPublishSubscribe(t *testing.T) {
	bus := NewBus()
	sub := bus.Subscribe()

	bus.Emit(TaskStarted, TaskStartedData{Task: "test task"})

	select {
	case e := <-sub:
		if e.Type != TaskStarted {
			t.Fatalf("expected TaskStarted, got %s", e.Type)
		}
		data, ok := e.Data.(TaskStartedData)
		if !ok {
			t.Fatal("expected TaskStartedData payload")
		}
		if data.Task != "test task" {
			t.Fatalf("expected 'test task', got %q", data.Task)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestMultipleSubscribers(t *testing.T) {
	bus := NewBus()
	sub1 := bus.Subscribe()
	sub2 := bus.Subscribe()
	sub3 := bus.Subscribe()

	bus.Emit(PhaseStarted, PhaseData{Name: "decompose"})

	for i, sub := range []Subscriber{sub1, sub2, sub3} {
		select {
		case e := <-sub:
			if e.Type != PhaseStarted {
				t.Fatalf("subscriber %d: expected PhaseStarted, got %s", i, e.Type)
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d: timeout", i)
		}
	}
}

func TestSlowSubscriberDoesNotBlock(t *testing.T) {
	bus := NewBus()
	_ = bus.Subscribe() // subscriber that never reads

	// Publishing should not block even with a non-reading subscriber.
	done := make(chan struct{})
	go func() {
		for i := 0; i < defaultBufSize+100; i++ {
			bus.Emit(WorkerProgress, WorkerData{Index: i})
		}
		close(done)
	}()

	select {
	case <-done:
		// Success — publisher was not blocked.
	case <-time.After(5 * time.Second):
		t.Fatal("publisher blocked by slow subscriber")
	}
}

func TestBusClose(t *testing.T) {
	bus := NewBus()
	sub := bus.Subscribe()

	bus.Emit(TaskStarted, nil)
	bus.Close()

	// Should be able to drain remaining events.
	drained := 0
	for range sub {
		drained++
	}
	if drained != 1 {
		t.Fatalf("expected 1 drained event, got %d", drained)
	}

	// Publishing after close should be a no-op.
	bus.Emit(RunCompleted, nil)
}

func TestConcurrentPublish(t *testing.T) {
	bus := NewBus()
	sub := bus.Subscribe()

	const goroutines = 10
	const eventsPerGoroutine = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < eventsPerGoroutine; i++ {
				bus.Emit(WorkerProgress, WorkerData{Index: g*eventsPerGoroutine + i})
			}
		}(g)
	}
	wg.Wait()
	bus.Close()

	// Count received events (some may have been dropped due to buffer).
	received := 0
	for range sub {
		received++
	}
	if received == 0 {
		t.Fatal("expected at least some events")
	}
	// With buffer size 256 and 500 events, we should get at least 256.
	if received < defaultBufSize {
		t.Fatalf("expected at least %d events, got %d", defaultBufSize, received)
	}
}

func TestNewEventTimestamp(t *testing.T) {
	before := time.Now()
	e := New(TaskStarted, nil)
	after := time.Now()

	if e.Timestamp.Before(before) || e.Timestamp.After(after) {
		t.Fatalf("timestamp %v not between %v and %v", e.Timestamp, before, after)
	}
}
