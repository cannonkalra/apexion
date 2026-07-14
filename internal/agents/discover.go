package agents

import (
	"context"
	"fmt"
	"strings"

	"github.com/rs/zerolog"

	"github.com/apexion/apexion/internal/events"
	"github.com/apexion/apexion/internal/model"
	"github.com/apexion/apexion/internal/storage"
)

// DiscoverAgent reacts to DatasetDiscovered events and writes a concise
// human-readable description of each new dataset (via the LLM when available,
// otherwise a deterministic local summary).
type DiscoverAgent struct {
	provider Provider
	store    *storage.Store
	log      zerolog.Logger
}

// NewDiscoverAgent constructs the agent.
func NewDiscoverAgent(p Provider, store *storage.Store, log zerolog.Logger) *DiscoverAgent {
	return &DiscoverAgent{provider: p, store: store, log: log.With().Str("agent", "discover").Logger()}
}

func (a *DiscoverAgent) Name() string { return "DiscoverAgent" }
func (a *DiscoverAgent) Kind() Kind   { return KindDiscover }

// Subscribe wires the agent to DatasetDiscovered events.
func (a *DiscoverAgent) Subscribe(bus *events.Bus) {
	bus.Subscribe(events.TypeDatasetDiscovered, events.HandlerFunc{
		NameStr: "DiscoverAgent",
		Fn: func(ctx context.Context, e events.Event) error {
			a.OnDatasetDiscovered(e.Subject)
			return nil
		},
	})
}

// OnDatasetDiscovered generates and stores a dataset description.
func (a *DiscoverAgent) OnDatasetDiscovered(datasetID string) {
	ctx := context.Background()
	ds, err := a.store.GetDataset(ctx, datasetID)
	if err != nil || ds == nil {
		return
	}
	if strings.TrimSpace(ds.Description) != "" {
		return // already documented
	}
	schema, _ := a.store.LatestSchema(ctx, datasetID)

	desc := a.localDescription(ds, schema)
	if a.provider.Available() {
		if llm, err := a.provider.Complete(ctx, discoverSystemPrompt, a.prompt(ds, schema)); err == nil && llm != "" {
			desc = llm
		} else if err != nil {
			a.log.Warn().Err(err).Msg("llm description failed, using local")
		}
	}

	ds.Description = desc
	if err := a.store.UpsertDataset(ctx, ds); err != nil {
		a.log.Warn().Err(err).Msg("save description")
		return
	}
	a.log.Debug().Str("dataset", ds.Name).Msg("dataset described")
}

const discoverSystemPrompt = "You are a data catalog assistant. Given a dataset's " +
	"name, format and columns, write ONE concise sentence describing what the dataset " +
	"most likely contains. Do not restate the column list verbatim."

func (a *DiscoverAgent) prompt(ds *model.Dataset, schema *model.Schema) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Dataset: %s\nFormat: %s\nFiles: %d\n", ds.Name, ds.Format, ds.FileCount)
	if len(ds.PartitionKeys) > 0 {
		fmt.Fprintf(&b, "Partitioned by: %s\n", strings.Join(ds.PartitionKeys, ", "))
	}
	if schema != nil {
		b.WriteString("Columns: ")
		for i, c := range schema.Columns {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%s (%s)", c.Name, c.DataType)
		}
	}
	return b.String()
}

func (a *DiscoverAgent) localDescription(ds *model.Dataset, schema *model.Schema) string {
	cols := 0
	var names []string
	if schema != nil {
		cols = len(schema.Columns)
		for _, c := range schema.Columns {
			names = append(names, c.Name)
			if len(names) >= 5 {
				break
			}
		}
	}
	desc := fmt.Sprintf("A %s dataset with %d file(s) and %d column(s)", ds.Format, ds.FileCount, cols)
	if len(names) > 0 {
		desc += " including " + strings.Join(names, ", ")
	}
	if len(ds.PartitionKeys) > 0 {
		desc += fmt.Sprintf("; partitioned by %s", strings.Join(ds.PartitionKeys, "/"))
	}
	return desc + "."
}
