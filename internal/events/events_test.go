package events

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestBusDelivery(t *testing.T) {
	bus := NewBus(zerolog.Nop(), 16)
	defer bus.Close()

	var mu sync.Mutex
	var got []Type
	done := make(chan struct{}, 2)

	bus.Subscribe(TypeDatasetDiscovered, HandlerFunc{NameStr: "sub", Fn: func(_ context.Context, e Event) error {
		mu.Lock()
		got = append(got, e.Type)
		mu.Unlock()
		done <- struct{}{}
		return nil
	}})
	bus.SubscribeAll(HandlerFunc{NameStr: "all", Fn: func(_ context.Context, e Event) error {
		done <- struct{}{}
		return nil
	}})

	bus.Publish(New(TypeDatasetDiscovered, "d1", "e1", time.Now(),
		DatasetDiscoveredData{DatasetID: "d1", Name: "orders"}))

	// Expect both the type subscriber and the all-subscriber to fire.
	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("handler did not fire")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != TypeDatasetDiscovered {
		t.Fatalf("got %v", got)
	}
}

func TestEventDecode(t *testing.T) {
	e := New(TypeSchemaChanged, "d1", "e1", time.Now(), SchemaChangedData{DatasetID: "d1", Version: 3, Columns: 5})
	var d SchemaChangedData
	if err := e.Decode(&d); err != nil {
		t.Fatal(err)
	}
	if d.Version != 3 || d.Columns != 5 {
		t.Fatalf("decoded %+v", d)
	}
}
