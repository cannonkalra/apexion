package agents

import (
	"github.com/rs/zerolog"

	"github.com/apexion/apexion/internal/events"
	"github.com/apexion/apexion/internal/storage"
)

// Kind categorizes an agent's role.
type Kind string

const (
	KindDiscover      Kind = "discover"
	KindInference     Kind = "inference"
	KindQuality       Kind = "quality"
	KindLineage       Kind = "lineage"
	KindDocumentation Kind = "documentation"
)

// Agent is the base interface every AI agent implements. Agents subscribe to
// the event bus and enrich the catalog. This is the platform's primary
// extension seam for AI.
type Agent interface {
	Name() string
	Kind() Kind
	Subscribe(bus *events.Bus)
}

// The following role interfaces exist so future agents can be discovered and
// wired generically. DiscoverAgent and InferenceAgent are implemented today;
// the others are defined for the roadmap (Quality, Lineage, Documentation).

// Discoverer reacts to discovery events (new datasets/partitions).
type Discoverer interface {
	Agent
	OnDatasetDiscovered(datasetID string)
}

// Inferencer reacts to completed inference runs.
type Inferencer interface {
	Agent
	OnInferenceCompleted(runID string)
}

// QualityAgent (roadmap) scores data quality and raises issues.
type QualityAgent interface{ Agent }

// LineageAgent (roadmap) enriches lineage with cross-dataset relationships.
type LineageAgent interface{ Agent }

// DocumentationAgent (roadmap) writes catalog documentation.
type DocumentationAgent interface{ Agent }

// Registry holds the active agents (for introspection by the API/UI).
type Registry struct {
	provider Provider
	store    *storage.Store
	log      zerolog.Logger
	agents   []Agent
}

// NewRegistry builds an agent registry with the default enabled agents.
func NewRegistry(provider Provider, store *storage.Store, bus *events.Bus, log zerolog.Logger) *Registry {
	r := &Registry{provider: provider, store: store, log: log.With().Str("component", "agents").Logger()}
	r.register(NewDiscoverAgent(provider, store, r.log), bus)
	r.register(NewInferenceAgent(provider, store, r.log), bus)
	return r
}

func (r *Registry) register(a Agent, bus *events.Bus) {
	a.Subscribe(bus)
	r.agents = append(r.agents, a)
	r.log.Info().Str("agent", a.Name()).Str("kind", string(a.Kind())).Msg("agent registered")
}

// Agents returns the active agents.
func (r *Registry) Agents() []Agent { return r.agents }

// Provider returns the configured LLM provider.
func (r *Registry) Provider() Provider { return r.provider }

// AgentInfo is a UI-facing summary of an agent.
type AgentInfo struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Provider string `json:"provider"`
	Enabled  bool   `json:"enabled"`
}

// Info returns summaries of all registered agents.
func (r *Registry) Info() []AgentInfo {
	out := make([]AgentInfo, 0, len(r.agents))
	for _, a := range r.agents {
		out = append(out, AgentInfo{
			Name: a.Name(), Kind: string(a.Kind()),
			Provider: r.provider.Name(), Enabled: true,
		})
	}
	return out
}
