package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rs/zerolog"

	"github.com/apexion/apexion/internal/events"
	"github.com/apexion/apexion/internal/model"
	"github.com/apexion/apexion/internal/storage"
)

// InferenceAgent reacts to InferenceCompleted events and writes a natural-
// language summary of the findings back into the inference run (via the LLM
// when available, otherwise a deterministic local summary).
type InferenceAgent struct {
	provider Provider
	store    *storage.Store
	log      zerolog.Logger
}

// NewInferenceAgent constructs the agent.
func NewInferenceAgent(p Provider, store *storage.Store, log zerolog.Logger) *InferenceAgent {
	return &InferenceAgent{provider: p, store: store, log: log.With().Str("agent", "inference").Logger()}
}

func (a *InferenceAgent) Name() string { return "InferenceAgent" }
func (a *InferenceAgent) Kind() Kind   { return KindInference }

// Subscribe wires the agent to InferenceCompleted events.
func (a *InferenceAgent) Subscribe(bus *events.Bus) {
	bus.Subscribe(events.TypeInferenceCompleted, events.HandlerFunc{
		NameStr: "InferenceAgent",
		Fn: func(ctx context.Context, e events.Event) error {
			var d events.InferenceCompletedData
			if err := e.Decode(&d); err != nil {
				return err
			}
			a.OnInferenceCompleted(d.RunID)
			return nil
		},
	})
}

// OnInferenceCompleted summarizes the inference findings.
func (a *InferenceAgent) OnInferenceCompleted(runID string) {
	ctx := context.Background()
	run, err := a.store.GetInferenceRun(ctx, runID)
	if err != nil || run == nil {
		return
	}
	var result model.InferenceResult
	if err := json.Unmarshal([]byte(run.Findings), &result); err != nil {
		return
	}

	summary := a.localSummary(run, result)
	if a.provider.Available() {
		if llm, err := a.provider.Complete(ctx, inferenceSystemPrompt, a.prompt(run, result)); err == nil && llm != "" {
			summary = llm
		} else if err != nil {
			a.log.Warn().Err(err).Msg("llm summary failed, using local")
		}
	}

	result.LLMSummary = summary
	blob, _ := json.Marshal(result)
	run.Findings = string(blob)
	if err := a.store.SaveInferenceRun(ctx, run); err != nil {
		a.log.Warn().Err(err).Msg("save inference summary")
		return
	}
	a.log.Debug().Str("dataset", run.DatasetName).Msg("inference summarized")
}

const inferenceSystemPrompt = "You are a data quality analyst. Summarize the inference " +
	"findings for a dataset in 2-3 sentences: highlight primary key, notable PII, and " +
	"overall data quality. Be specific and concise."

func (a *InferenceAgent) prompt(run *model.InferenceRun, r model.InferenceResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Dataset: %s\nRows sampled: %d\nQuality score: %.0f/100\n",
		run.DatasetName, r.SampleRows, r.QualityScore)
	if len(r.PrimaryKeys) > 0 {
		fmt.Fprintf(&b, "Primary key candidates: %s\n", strings.Join(r.PrimaryKeys, ", "))
	}
	if len(r.PIIColumns) > 0 {
		fmt.Fprintf(&b, "PII columns: %s\n", strings.Join(r.PIIColumns, ", "))
	}
	b.WriteString("Columns:\n")
	for _, c := range r.Columns {
		fmt.Fprintf(&b, "- %s: type=%s semantic=%s completeness=%.0f%% unique=%.0f%%\n",
			c.Name, c.DataType, c.SemanticType, c.Completeness*100, c.Uniqueness*100)
	}
	return b.String()
}

func (a *InferenceAgent) localSummary(run *model.InferenceRun, r model.InferenceResult) string {
	var parts []string
	parts = append(parts, fmt.Sprintf("Analyzed %d sampled rows across %d columns with an overall quality score of %.0f/100.",
		r.SampleRows, len(r.Columns), r.QualityScore))
	if len(r.PrimaryKeys) > 0 {
		parts = append(parts, fmt.Sprintf("Likely primary key: %s.", strings.Join(r.PrimaryKeys, ", ")))
	}
	if len(r.PIIColumns) > 0 {
		parts = append(parts, fmt.Sprintf("Detected PII in %d column(s): %s.", len(r.PIIColumns), strings.Join(r.PIIColumns, ", ")))
	} else {
		parts = append(parts, "No PII detected.")
	}
	if len(r.ForeignKeys) > 0 {
		parts = append(parts, fmt.Sprintf("Found %d foreign-key candidate(s).", len(r.ForeignKeys)))
	}
	return strings.Join(parts, " ")
}
