// Package llm is the provider-agnostic LLM boundary for the persona engine, extraction and the
// persona generator. TRD §3 names the Claude API, which is the default; Gemini is still supported
// behind the same interface (SOCKABUS_LLM_PROVIDER=gemini) and "fake" serves CI and the monitor.
//
// Prompt-injection rule (TRD §7): scammer text only ever goes into Turns with RoleUser.
// It is never concatenated into System.
package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type Role string

const (
	RoleUser  Role = "user"  // the other party (scammer), or the input document for extraction
	RoleModel Role = "model" // what the persona already said
)

type Turn struct {
	Role Role
	Text string
}

// Effort maps to the provider's reasoning setting (Gemini thinkingLevel; ignored by Claude here).
type Effort string

const (
	EffortDefault Effort = ""
	EffortLow     Effort = "low"
	EffortMedium  Effort = "medium"
	EffortHigh    Effort = "high"
)

type Request struct {
	System          string
	Turns           []Turn
	JSON            bool // ask for a JSON response body (callers must still validate it)
	MaxOutputTokens int
	Effort          Effort
}

type Client interface {
	// Generate returns the model's text. It returns ErrBlocked when the provider refused to answer.
	Generate(ctx context.Context, req Request) (string, error)
	// Name identifies provider and model for logs, e.g. "gemini/gemini-3.8-flash".
	Name() string
}

var (
	ErrBlocked  = errors.New("llm: response blocked by provider")
	ErrNoClient = errors.New("llm: no provider configured")
)

// Normalize merges consecutive same-role turns, drops empty ones, and drops leading model turns,
// because both providers expect the conversation to start with a user turn.
func Normalize(turns []Turn) []Turn {
	out := make([]Turn, 0, len(turns))
	for _, t := range turns {
		text := strings.TrimSpace(t.Text)
		if text == "" {
			continue
		}
		if len(out) == 0 && t.Role != RoleUser {
			continue
		}
		if n := len(out); n > 0 && out[n-1].Role == t.Role {
			out[n-1].Text += "\n" + text
			continue
		}
		out = append(out, Turn{Role: t.Role, Text: text})
	}
	return out
}

// Config selects and configures a provider.
type Config struct {
	Provider      string // anthropic (default) | gemini | openai (any OpenAI-compatible API) | fake | none
	Model         string
	GeminiKey     string
	AnthropicKey  string
	ThinkingLevel string // gemini only: low | medium | high
	OpenAIBaseURL string // openai only: e.g. https://<provider>/v1 (ILMU, MaLLaM, SEA-LION, vLLM, Ollama)
	OpenAIKey     string // openai only; may be empty for a local server
}

// New returns a Client for cfg, or (nil, nil) when Provider is "none" or the key is missing, so
// callers can fall back to playbook lines. An unknown provider is an error.
func New(cfg Config) (Client, error) {
	switch strings.ToLower(cfg.Provider) {
	case "gemini":
		if cfg.GeminiKey == "" {
			return nil, nil
		}
		return NewGemini(cfg.GeminiKey, cfg.Model, cfg.ThinkingLevel), nil
	case "", "anthropic", "claude":
		if cfg.AnthropicKey == "" {
			return nil, nil
		}
		return NewAnthropic(cfg.AnthropicKey, cfg.Model), nil
	case "openai", "openai-compat":
		if cfg.OpenAIBaseURL == "" || cfg.Model == "" {
			return nil, errors.New("llm: provider openai needs SOCKABUS_OPENAI_BASE_URL and a model")
		}
		return NewOpenAICompat(cfg.OpenAIBaseURL, cfg.OpenAIKey, cfg.Model), nil
	case "fake":
		return NewFake(), nil
	case "none", "off":
		return nil, nil
	default:
		return nil, fmt.Errorf("llm: unknown provider %q", cfg.Provider)
	}
}
