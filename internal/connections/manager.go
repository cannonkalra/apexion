// Package connections manages multiple object-store connection profiles
// (AWS/MinIO/S3 accounts). Exactly one is active; the manager hands the active
// objstore.ObjectStore to the crawler, explorer, and catalog, and reconfigures
// the DuckDB preview engine whenever the active connection changes. It
// implements objstore.Provider and builds clients through the storage-provider
// registry, so it never imports a storage SDK directly.
package connections

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/apexion/apexion/internal/config"
	"github.com/apexion/apexion/internal/duckdb"
	"github.com/apexion/apexion/internal/features"
	"github.com/apexion/apexion/internal/model"
	"github.com/apexion/apexion/internal/objstore"
	"github.com/apexion/apexion/internal/storage"
)

// ErrConnectionNotFound is returned when a connection id does not resolve to a
// stored profile. Callers should compare with errors.Is, never by string.
var ErrConnectionNotFound = errors.New("connection not found")

// Manager owns connection profiles and the active client.
type Manager struct {
	store   *storage.Store
	preview *duckdb.Engine
	def     config.MinIOConfig // fallback object-store defaults; never a persisted connection
	log     zerolog.Logger

	mu       sync.RWMutex
	active   *model.Connection
	clients  map[string]objstore.ObjectStore // by connection id
	fallback objstore.ObjectStore
}

var _ objstore.Provider = (*Manager)(nil)

// New creates a connection manager. def holds the static-config object-store
// defaults used only for the fallback store (CLI/headless use before any
// connection is active); it is never persisted as a connection.
func New(store *storage.Store, prev *duckdb.Engine, def config.MinIOConfig, log zerolog.Logger) *Manager {
	return &Manager{
		store: store, preview: prev, def: def,
		log:     log.With().Str("component", "connections").Logger(),
		clients: map[string]objstore.ObjectStore{},
	}
}

// Init loads the active connection. No connection is created automatically: a
// fresh install starts with zero configured connections and an empty state in
// the UI, and the user explicitly creates their first connection. Existing
// installs keep whatever connections they have already saved.
func (m *Manager) Init(ctx context.Context) error {
	// Ensure exactly one active connection exists when connections are present.
	// On a fresh install this leaves the manager with no active connection.
	active, err := m.store.GetActiveConnection(ctx)
	if err != nil {
		return err
	}
	if active == nil {
		list, err := m.store.ListConnections(ctx)
		if err != nil {
			return err
		}
		if len(list) > 0 {
			if err := m.store.SetActiveConnection(ctx, list[0].ID); err != nil {
				return err
			}
			active = &list[0]
		}
	}
	return m.activate(active)
}

// activate sets the in-memory active connection and reconfigures the preview
// engine. conn may be nil (no connections configured).
func (m *Manager) activate(conn *model.Connection) error {
	m.mu.Lock()
	m.active = conn
	m.mu.Unlock()
	if conn == nil {
		return nil
	}
	// The store owns all provider-specific preview configuration; the manager
	// just forwards it to the query engine.
	if qc, ok := m.storeFor(conn).(objstore.QueryConfigurer); ok {
		if err := m.preview.Reconfigure(qc.PreviewConfig()); err != nil {
			m.log.Warn().Err(err).Msg("preview reconfigure failed")
		}
	}
	m.log.Info().Str("connection", conn.Name).Str("endpoint", conn.Endpoint).Msg("active connection set")
	return nil
}

// Store returns the active object store (for connection-level / global
// operations like ListBuckets). It always returns a usable store.
func (m *Manager) Store() objstore.ObjectStore {
	m.mu.RLock()
	active := m.active
	m.mu.RUnlock()
	if active == nil {
		return m.fallbackStore()
	}
	return m.storeFor(active)
}

// StoreFor returns the active store. Per-bucket addressing (path-style vs
// virtual-hosted) is handled inside the store by the provider, so the bucket
// name is not needed here.
func (m *Manager) StoreFor(string) objstore.ObjectStore { return m.Store() }

// storeFor builds/caches the store for a connection.
func (m *Manager) storeFor(active *model.Connection) objstore.ObjectStore {
	m.mu.RLock()
	c := m.clients[active.ID]
	m.mu.RUnlock()
	if c != nil {
		return c
	}
	built, err := buildClient(active)
	if err != nil {
		m.log.Warn().Err(err).Str("connection", active.Name).Msg("build client failed, using fallback")
		return m.fallbackStore()
	}
	m.mu.Lock()
	m.clients[active.ID] = built
	m.mu.Unlock()
	return built
}

func (m *Manager) fallbackStore() objstore.ObjectStore {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fallback == nil {
		conn, err := connectorFor("")
		if err != nil {
			m.log.Error().Err(err).Msg("no storage provider for fallback")
			return nil
		}
		c, err := conn.Connect(objstore.Config{
			Endpoint: m.def.Endpoint, AccessKey: m.def.AccessKey,
			SecretKey: m.def.SecretKey, UseSSL: m.def.UseSSL, Region: m.def.Region,
		})
		if err != nil {
			// Connect only fails on malformed endpoint; keep behavior minimal.
			m.log.Error().Err(err).Msg("fallback client build failed")
		}
		m.fallback = c
	}
	return m.fallback
}

// Active returns the active connection (nil if none).
func (m *Manager) Active() *model.Connection {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.active
}

// List returns all connection profiles.
func (m *Manager) List(ctx context.Context) ([]model.Connection, error) {
	return m.store.ListConnections(ctx)
}

// Create stores a new connection. The first connection created becomes active.
func (m *Manager) Create(ctx context.Context, c *model.Connection) error {
	now := time.Now().UTC()
	c.ID = uuid.NewString()
	c.CreatedAt = now
	c.UpdatedAt = now
	if c.Provider == "" {
		c.Provider = "minio"
	}
	if c.Region == "" {
		c.Region = "us-east-1"
	}
	// Provider-specific normalization (e.g. AWS endpoint/SSL/role defaults) is
	// applied by the provider when the store is built — the manager stores the
	// profile as entered.
	if err := m.store.UpsertConnection(ctx, c); err != nil {
		return err
	}
	if m.Active() == nil {
		return m.SetActive(ctx, c.ID)
	}
	return nil
}

// SetActive switches the active connection.
func (m *Manager) SetActive(ctx context.Context, id string) error {
	conn, err := m.store.GetConnection(ctx, id)
	if err != nil {
		return err
	}
	if conn == nil {
		return ErrConnectionNotFound
	}
	if err := m.store.SetActiveConnection(ctx, id); err != nil {
		return err
	}
	conn.IsActive = true
	return m.activate(conn)
}

// Delete removes a connection, activating another if the active one is deleted.
func (m *Manager) Delete(ctx context.Context, id string) error {
	if err := m.store.DeleteConnection(ctx, id); err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.clients, id)
	wasActive := m.active != nil && m.active.ID == id
	m.mu.Unlock()
	if !wasActive {
		return nil
	}
	list, err := m.store.ListConnections(ctx)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		return m.activate(nil)
	}
	return m.SetActive(ctx, list[0].ID)
}

// TestConnection validates credentials by listing buckets.
func (m *Manager) TestConnection(ctx context.Context, c *model.Connection) error {
	client, err := buildClient(c)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err = client.ListBuckets(ctx)
	return err
}

// connectorFor resolves the storage connector for a provider name (or alias),
// falling back to the S3-compatible provider. It is how the manager builds
// clients without importing any storage SDK.
func connectorFor(name string) (objstore.Connector, error) {
	try := func(n string) objstore.Connector {
		if sp, ok := features.LookupStorageProvider(n); ok && sp.Connector != nil {
			return sp.Connector
		}
		return nil
	}
	if c := try(name); c != nil {
		return c, nil
	}
	if c := try("s3"); c != nil {
		return c, nil
	}
	return nil, fmt.Errorf("no storage provider registered for %q", name)
}

// buildClient constructs an ObjectStore for a connection profile via the
// registered provider connector. All provider-specific normalization (AWS
// addressing, region handling, credentials) happens inside the provider's
// store; the manager only forwards the raw profile.
func buildClient(c *model.Connection) (objstore.ObjectStore, error) {
	conn, err := connectorFor(c.Provider)
	if err != nil {
		return nil, err
	}
	return conn.Connect(objstore.Config{
		Provider: c.Provider, Endpoint: c.Endpoint, AccessKey: c.AccessKey,
		SecretKey: c.SecretKey, UseSSL: c.UseSSL, Region: c.Region,
		UseRole: c.UseRole, PathStyle: c.PathStyle,
	})
}
