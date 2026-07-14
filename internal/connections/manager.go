// Package connections manages multiple object-store connection profiles
// (AWS/MinIO/S3 accounts). Exactly one is active; the manager hands the active
// s3.Client to the crawler, explorer, and catalog, and reconfigures the DuckDB
// preview engine whenever the active connection changes. It implements
// s3.Provider.
package connections

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/apexion/apexion/internal/config"
	"github.com/apexion/apexion/internal/crawler/s3"
	"github.com/apexion/apexion/internal/model"
	"github.com/apexion/apexion/internal/preview"
	"github.com/apexion/apexion/internal/storage"
)

// Manager owns connection profiles and the active client.
type Manager struct {
	store   *storage.Store
	preview *preview.Engine
	def     config.MinIOConfig
	log     zerolog.Logger

	mu       sync.RWMutex
	active   *model.Connection
	clients  map[string]*s3.Client // by connection id
	fallback *s3.Client
}

// New creates a connection manager. def is the connection seeded from the
// static config on first run.
func New(store *storage.Store, prev *preview.Engine, def config.MinIOConfig, log zerolog.Logger) *Manager {
	return &Manager{
		store: store, preview: prev, def: def,
		log:     log.With().Str("component", "connections").Logger(),
		clients: map[string]*s3.Client{},
	}
}

// Init seeds a default connection on first run and loads the active one.
func (m *Manager) Init(ctx context.Context) error {
	n, err := m.store.CountConnections(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		now := time.Now().UTC()
		seed := &model.Connection{
			ID: uuid.NewString(), Name: "Default", Provider: "minio",
			Endpoint: m.def.Endpoint, Region: m.def.Region,
			AccessKey: m.def.AccessKey, SecretKey: m.def.SecretKey,
			UseSSL: m.def.UseSSL, IsActive: true, CreatedAt: now, UpdatedAt: now,
		}
		if err := m.store.UpsertConnection(ctx, seed); err != nil {
			return err
		}
		m.log.Info().Str("name", seed.Name).Msg("seeded default connection")
	}
	// Ensure exactly one active connection exists.
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
	if err := m.preview.Reconfigure(previewConn(conn)); err != nil {
		m.log.Warn().Err(err).Msg("preview reconfigure failed")
	}
	m.log.Info().Str("connection", conn.Name).Str("endpoint", conn.Endpoint).Msg("active connection set")
	return nil
}

// Client returns the active S3 client, building and caching it as needed. It
// always returns a usable client (falling back to the static config).
func (m *Manager) Client() *s3.Client {
	m.mu.RLock()
	active := m.active
	m.mu.RUnlock()

	if active == nil {
		return m.fallbackClient()
	}
	m.mu.RLock()
	c := m.clients[active.ID]
	m.mu.RUnlock()
	if c != nil {
		return c
	}
	built, err := buildClient(active)
	if err != nil {
		m.log.Warn().Err(err).Str("connection", active.Name).Msg("build client failed, using fallback")
		return m.fallbackClient()
	}
	m.mu.Lock()
	m.clients[active.ID] = built
	m.mu.Unlock()
	return built
}

func (m *Manager) fallbackClient() *s3.Client {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fallback == nil {
		c, err := s3.New(s3.Config{
			Endpoint: m.def.Endpoint, AccessKey: m.def.AccessKey,
			SecretKey: m.def.SecretKey, UseSSL: m.def.UseSSL, Region: m.def.Region,
		})
		if err != nil {
			// s3.New only fails on malformed endpoint; construct a minimal client.
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
	if isAWS(c) {
		c.Provider = "aws"
		c.UseSSL = true // AWS S3 requires HTTPS
		if c.Endpoint == "" {
			c.Endpoint = "s3.amazonaws.com"
		}
		if c.AccessKey == "" {
			c.UseRole = true // no keys provided → service role
		}
	}
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
		return fmt.Errorf("connection not found")
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

// isAWS reports whether a connection targets real AWS S3 (by provider choice or
// an amazonaws.com endpoint), which needs HTTPS and per-bucket region handling.
func isAWS(c *model.Connection) bool {
	return c.Provider == "aws" || strings.Contains(strings.ToLower(c.Endpoint), "amazonaws.com")
}

// buildClient constructs an s3.Client for a connection profile.
//
// For AWS we (a) force HTTPS — plain HTTP to s3.amazonaws.com returns 307
// redirects — and (b) leave the signing region empty so the SDK auto-discovers
// each bucket's real region (a bucket outside us-east-1 otherwise fails with
// "Access Denied" when signed for the wrong region).
func buildClient(c *model.Connection) (*s3.Client, error) {
	endpoint := c.Endpoint
	useSSL := c.UseSSL
	region := c.Region
	useRole := c.UseRole
	if isAWS(c) {
		if endpoint == "" {
			endpoint = "s3.amazonaws.com"
		}
		useSSL = true
		region = "" // auto-discover per bucket
		if c.AccessKey == "" {
			useRole = true // no static keys → use the instance/service role
		}
	}
	return s3.New(s3.Config{
		Endpoint: endpoint, AccessKey: c.AccessKey, SecretKey: c.SecretKey,
		UseSSL: useSSL, Region: region, UseRole: useRole,
	})
}

// previewConn maps a connection to preview engine parameters.
func previewConn(c *model.Connection) preview.Conn {
	urlStyle := "path"
	endpoint := c.Endpoint
	useSSL := c.UseSSL
	region := c.Region
	useRole := c.UseRole
	if isAWS(c) {
		urlStyle = "vhost"
		useSSL = true
		if endpoint == "" {
			endpoint = "s3.amazonaws.com"
		}
		if region == "" {
			region = "us-east-1"
		}
		if c.AccessKey == "" {
			useRole = true
		}
		// For real AWS, the DuckDB secret should not pin a custom endpoint —
		// let it use AWS's regional endpoints.
		endpoint = ""
	}
	return preview.Conn{
		Endpoint: endpoint, Region: region, AccessKey: c.AccessKey,
		SecretKey: c.SecretKey, UseSSL: useSSL, URLStyle: urlStyle, UseRole: useRole,
	}
}
