// Package agents is the AI Agent SDK. Agents are event subscribers that react
// to catalog events and enrich the catalog. The LLM backend is pluggable: a
// no-op local provider, or any OpenAI-compatible / Anthropic / Ollama endpoint,
// selected by configuration — the crawler and inference engine never change.
package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Provider abstracts a large language model. Implementations may be local
// (Ollama), cloud (OpenAI/Anthropic), or a no-op used when no model is set.
type Provider interface {
	// Available reports whether a real model is configured.
	Available() bool
	// Complete returns a completion for the system+user prompt.
	Complete(ctx context.Context, system, user string) (string, error)
	// Name identifies the backend.
	Name() string
}

// Config configures a provider.
type Config struct {
	Provider string // noop|openai|anthropic|ollama
	BaseURL  string
	APIKey   string
	Model    string
}

// NewProvider builds a Provider from configuration.
func NewProvider(cfg Config) Provider {
	switch strings.ToLower(cfg.Provider) {
	case "openai":
		base := orDefault(cfg.BaseURL, "https://api.openai.com/v1")
		return &openAIProvider{base: base, key: cfg.APIKey, model: orDefault(cfg.Model, "gpt-4o-mini"), name: "openai"}
	case "ollama":
		base := orDefault(cfg.BaseURL, "http://localhost:11434/v1")
		return &openAIProvider{base: base, key: cfg.APIKey, model: orDefault(cfg.Model, "llama3.1"), name: "ollama"}
	case "groq":
		// Groq exposes an OpenAI-compatible API, so it reuses the same client.
		base := orDefault(cfg.BaseURL, "https://api.groq.com/openai/v1")
		return &openAIProvider{base: base, key: cfg.APIKey, model: orDefault(cfg.Model, "llama-3.3-70b-versatile"), name: "groq"}
	case "anthropic":
		return &anthropicProvider{base: orDefault(cfg.BaseURL, "https://api.anthropic.com"), key: cfg.APIKey, model: orDefault(cfg.Model, "claude-3-5-haiku-latest")}
	default:
		return noopProvider{}
	}
}

// noopProvider is used when no LLM is configured; agents fall back to local
// deterministic text generation.
type noopProvider struct{}

func (noopProvider) Available() bool                                          { return false }
func (noopProvider) Name() string                                             { return "noop" }
func (noopProvider) Complete(context.Context, string, string) (string, error) { return "", nil }

// openAIProvider speaks the OpenAI /chat/completions API (also served by
// Ollama's OpenAI-compatible endpoint).
type openAIProvider struct {
	base, key, model, name string
}

func (p *openAIProvider) Available() bool { return true }
func (p *openAIProvider) Name() string    { return p.name }

func (p *openAIProvider) Complete(ctx context.Context, system, user string) (string, error) {
	body := map[string]any{
		"model": p.model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"temperature": 0.2,
		"max_tokens":  400,
	}
	raw, err := postJSON(ctx, p.base+"/chat/completions", p.key, "", body)
	if err != nil {
		return "", err
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", nil
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}

// anthropicProvider speaks the Anthropic Messages API.
type anthropicProvider struct {
	base, key, model string
}

func (p *anthropicProvider) Available() bool { return true }
func (p *anthropicProvider) Name() string    { return "anthropic" }

func (p *anthropicProvider) Complete(ctx context.Context, system, user string) (string, error) {
	body := map[string]any{
		"model":      p.model,
		"max_tokens": 400,
		"system":     system,
		"messages":   []map[string]string{{"role": "user", "content": user}},
	}
	raw, err := postJSON(ctx, p.base+"/v1/messages", "", p.key, body)
	if err != nil {
		return "", err
	}
	var out struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	if len(out.Content) == 0 {
		return "", nil
	}
	return strings.TrimSpace(out.Content[0].Text), nil
}

// postJSON posts a JSON body. bearer sets Authorization: Bearer; xapikey sets
// the Anthropic x-api-key header.
func postJSON(ctx context.Context, url, bearer, xapikey string, body any) ([]byte, error) {
	buf, _ := json.Marshal(body)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if xapikey != "" {
		req.Header.Set("x-api-key", xapikey)
		req.Header.Set("anthropic-version", "2023-06-01")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("llm http %d: %s", resp.StatusCode, string(data))
	}
	return data, nil
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
