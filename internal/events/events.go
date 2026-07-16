// Package events implements the in-process, event-driven backbone of Apexion.
//
// Every meaningful thing the crawler and inference engine do is published as a
// structured Event. Subscribers (persistence, the UI activity feed, and AI
// agents) consume these events without the producers knowing they exist. This
// is what makes the platform extensible: an agent is just another subscriber.
package events

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// Type enumerates the structured event types emitted by the platform.
type Type string

const (
	TypeCrawlStarted      Type = "CrawlStarted"
	TypeCrawlCompleted    Type = "CrawlCompleted"
	TypeDatasetDiscovered Type = "DatasetDiscovered"
	TypeSchemaChanged     Type = "SchemaChanged"
	TypeNewPartition      Type = "NewPartition"
	TypeCatalogUpdated    Type = "CatalogUpdated"
)

// Event is a single structured domain event.
//
// It is intentionally a concrete struct (not an interface) so it can be
// trivially serialized to the event store and to any external transport later
// (Kafka, NATS, webhook) without reflection.
type Event struct {
	ID      string          `json:"id"`
	Type    Type            `json:"type"`
	Subject string          `json:"subject"` // id of the entity the event concerns
	Time    time.Time       `json:"time"`
	Data    json.RawMessage `json:"data"`
}

// New builds an Event, marshaling data into the payload. now is injected so
// the bus stays deterministic/testable.
func New(t Type, subject string, id string, now time.Time, data any) Event {
	raw, _ := json.Marshal(data)
	return Event{ID: id, Type: t, Subject: subject, Time: now, Data: raw}
}

// Decode unmarshals the event payload into v.
func (e Event) Decode(v any) error { return json.Unmarshal(e.Data, v) }

// Handler consumes events. Handlers must be non-blocking or fast; long work
// should be dispatched to a worker/job. A handler error is logged, never fatal.
type Handler interface {
	Name() string
	Handle(ctx context.Context, e Event) error
}

// HandlerFunc adapts a function to Handler.
type HandlerFunc struct {
	NameStr string
	Fn      func(ctx context.Context, e Event) error
}

func (h HandlerFunc) Name() string                              { return h.NameStr }
func (h HandlerFunc) Handle(ctx context.Context, e Event) error { return h.Fn(ctx, e) }

// Bus is an in-process publish/subscribe event bus with an ordered dispatch
// loop and buffered backpressure.
type Bus struct {
	log     zerolog.Logger
	mu      sync.RWMutex
	subs    map[Type][]Handler
	all     []Handler
	queue   chan Event
	wg      sync.WaitGroup
	closed  bool
	closeMu sync.Mutex
}

// NewBus creates a bus with the given buffer size and starts its dispatcher.
func NewBus(log zerolog.Logger, buffer int) *Bus {
	if buffer <= 0 {
		buffer = 1024
	}
	b := &Bus{
		log:   log.With().Str("component", "eventbus").Logger(),
		subs:  make(map[Type][]Handler),
		queue: make(chan Event, buffer),
	}
	b.wg.Add(1)
	go b.dispatch()
	return b
}

// Subscribe registers a handler for a specific event type.
func (b *Bus) Subscribe(t Type, h Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs[t] = append(b.subs[t], h)
}

// SubscribeAll registers a handler that receives every event (e.g. persistence
// and the activity feed).
func (b *Bus) SubscribeAll(h Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.all = append(b.all, h)
}

// Publish enqueues an event for asynchronous, ordered delivery. If the bus is
// closed the event is dropped (with a log line).
func (b *Bus) Publish(e Event) {
	b.closeMu.Lock()
	closed := b.closed
	b.closeMu.Unlock()
	if closed {
		b.log.Warn().Str("type", string(e.Type)).Msg("publish on closed bus, dropped")
		return
	}
	select {
	case b.queue <- e:
	default:
		// Buffer full: log and block briefly rather than lose the event.
		b.log.Warn().Str("type", string(e.Type)).Msg("event buffer full, blocking")
		b.queue <- e
	}
}

func (b *Bus) dispatch() {
	defer b.wg.Done()
	for e := range b.queue {
		b.mu.RLock()
		handlers := make([]Handler, 0, len(b.all)+len(b.subs[e.Type]))
		handlers = append(handlers, b.all...)
		handlers = append(handlers, b.subs[e.Type]...)
		b.mu.RUnlock()

		for _, h := range handlers {
			func(h Handler) {
				defer func() {
					if r := recover(); r != nil {
						b.log.Error().Interface("panic", r).Str("handler", h.Name()).Msg("handler panicked")
					}
				}()
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if err := h.Handle(ctx, e); err != nil {
					b.log.Error().Err(err).Str("handler", h.Name()).Str("type", string(e.Type)).Msg("handler failed")
				}
			}(h)
		}
	}
}

// Close drains and stops the dispatcher. Safe to call once.
func (b *Bus) Close() {
	b.closeMu.Lock()
	if b.closed {
		b.closeMu.Unlock()
		return
	}
	b.closed = true
	b.closeMu.Unlock()
	close(b.queue)
	b.wg.Wait()
}

// ---------------------------------------------------------------------------
// Typed payloads. These are the `data` bodies for each event type; keeping
// them here gives producers and consumers a shared, compile-checked contract.
// ---------------------------------------------------------------------------

type CrawlStartedData struct {
	RunID    string `json:"run_id"`
	BucketID string `json:"bucket_id"`
	Bucket   string `json:"bucket"`
	Mode     string `json:"mode"`
}

type CrawlCompletedData struct {
	RunID          string `json:"run_id"`
	BucketID       string `json:"bucket_id"`
	Bucket         string `json:"bucket"`
	ObjectsScanned int64  `json:"objects_scanned"`
	DatasetsFound  int64  `json:"datasets_found"`
	Status         string `json:"status"`
}

type DatasetDiscoveredData struct {
	DatasetID string `json:"dataset_id"`
	Name      string `json:"name"`
	Bucket    string `json:"bucket"`
	Path      string `json:"path"`
	Format    string `json:"format"`
	FileCount int64  `json:"file_count"`
}

type SchemaChangedData struct {
	DatasetID  string `json:"dataset_id"`
	SchemaID   string `json:"schema_id"`
	Version    int    `json:"version"`
	OldVersion int    `json:"old_version"`
	Columns    int    `json:"columns"`
}

type NewPartitionData struct {
	DatasetID   string            `json:"dataset_id"`
	PartitionID string            `json:"partition_id"`
	Path        string            `json:"path"`
	Values      map[string]string `json:"values"`
}

type CatalogUpdatedData struct {
	Entity string `json:"entity"` // dataset|schema|column|bucket
	RefID  string `json:"ref_id"`
	Action string `json:"action"` // created|updated|deleted
}
