package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

const (
	DefaultAnthropicModel = "claude-sonnet-5-5"
	anthropicURL          = "https://api.anthropic.com/v1/messages"
	// Server-side refusal fallback ("default" routing by refusal category). Not offered for Haiku.
	anthropicFallbackBeta = "server-side-fallback-2026-07-01"
	// Adaptive thinking is on by default on current Claude models and its tokens count against
	// max_tokens, so a tiny budget (e.g. 256 for the classifier) could end before any text.
	anthropicMinMaxTokens = 2048
)

// Anthropic calls the Claude Messages API (the TRD §3 default provider) over raw HTTP.
//
// Request shape for current models (claude-sonnet-5-5, claude-haiku-5-5): no temperature/top_p,
// no thinking budget (adaptive thinking is the default), effort in output_config, no assistant
// prefill. Responses may contain thinking blocks; only text blocks are returned.
type Anthropic struct {
	Key, Model string
	URL        string // tests only; default anthropicURL
	HTTP       *http.Client

	noFallback atomic.Bool // set after the API rejected the fallback beta for this key
}

func NewAnthropic(key, model string) *Anthropic {
	if model == "" {
		model = DefaultAnthropicModel
	}
	// No client-side timeout: callers set deadlines per call (chat replies ~14 s, persona
	// generation ~90 s). This cap only stops a connection that never ends.
	return &Anthropic{Key: key, Model: model, URL: anthropicURL, HTTP: &http.Client{Timeout: 10 * time.Minute}}
}

func (a *Anthropic) Name() string { return "anthropic/" + a.Model }

type aMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type aRequest struct {
	Model        string         `json:"model"`
	MaxTokens    int            `json:"max_tokens"`
	System       string         `json:"system,omitempty"`
	Messages     []aMessage     `json:"messages"`
	OutputConfig *aOutputConfig `json:"output_config,omitempty"`
	Fallbacks    string         `json:"fallbacks,omitempty"`
}

type aOutputConfig struct {
	Effort string `json:"effort,omitempty"`
}

type aResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason  string `json:"stop_reason"`
	StopDetails *struct {
		Category    *string `json:"category"`
		Explanation string  `json:"explanation"`
	} `json:"stop_details"`
}

// supportsEffort: output_config.effort exists on Claude 4.6+ / 5.x; Haiku 4.5 and older reject it.
func (a *Anthropic) supportsEffort() bool {
	for _, old := range []string{"haiku-4", "-3-", "sonnet-4-5", "opus-4-5", "opus-4-1"} {
		if strings.Contains(a.Model, old) {
			return false
		}
	}
	return true
}

// supportsFallback: server-side fallback is offered for the Sonnet/Opus/Fable lines, not Haiku.
func (a *Anthropic) supportsFallback() bool {
	return !strings.Contains(a.Model, "haiku") && !a.noFallback.Load()
}

func (a *Anthropic) buildRequest(req Request) (aRequest, error) {
	turns := Normalize(req.Turns)
	if len(turns) == 0 || turns[len(turns)-1].Role != RoleUser {
		return aRequest{}, errors.New("anthropic: conversation must end with a user turn")
	}
	msgs := make([]aMessage, 0, len(turns))
	for _, t := range turns {
		role := "user"
		if t.Role == RoleModel {
			role = "assistant"
		}
		msgs = append(msgs, aMessage{Role: role, Content: t.Text})
	}
	system := req.System
	if req.JSON {
		system += "\n\nRespond with JSON only, no prose and no code fences."
	}
	body := aRequest{Model: a.Model, MaxTokens: max(req.MaxOutputTokens, anthropicMinMaxTokens), System: system, Messages: msgs}
	if req.Effort != EffortDefault && a.supportsEffort() {
		body.OutputConfig = &aOutputConfig{Effort: string(req.Effort)}
	}
	if a.supportsFallback() {
		body.Fallbacks = "default"
	}
	return body, nil
}

func (a *Anthropic) Generate(ctx context.Context, req Request) (string, error) {
	body, err := a.buildRequest(req)
	if err != nil {
		return "", err
	}
	data, err := a.post(ctx, body)
	var u *UpstreamError
	if errors.As(err, &u) && u.Status == http.StatusBadRequest && body.Fallbacks != "" && mentionsFallback(u.Message) {
		// This key/org does not have the fallback beta: drop it for good and resend once.
		a.noFallback.Store(true)
		body.Fallbacks = ""
		data, err = a.post(ctx, body)
	}
	if err != nil {
		return "", err
	}
	return parseAnthropic(data, req.JSON)
}

func mentionsFallback(msg string) bool {
	m := strings.ToLower(msg)
	return strings.Contains(m, "fallback") || strings.Contains(m, "anthropic-beta")
}

func (a *Anthropic) post(ctx context.Context, body aRequest) ([]byte, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	url := a.URL
	if url == "" {
		url = anthropicURL
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("x-api-key", a.Key)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	if body.Fallbacks != "" {
		httpReq.Header.Set("anthropic-beta", anthropicFallbackBeta)
	}
	res, err := a.HTTP.Do(httpReq)
	if err != nil {
		return nil, transportError("anthropic", err)
	}
	defer func() { _ = res.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return nil, transportError("anthropic", err)
	}
	if res.StatusCode != http.StatusOK {
		return nil, httpError("anthropic", res, anthropicErrorMessage(data))
	}
	return data, nil
}

func parseAnthropic(data []byte, wantJSON bool) (string, error) {
	var out aResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("anthropic: decode: %w", err)
	}
	if out.StopReason == "refusal" {
		cat := "unspecified"
		if out.StopDetails != nil && out.StopDetails.Category != nil {
			cat = *out.StopDetails.Category
		}
		return "", &UpstreamError{Provider: "anthropic", Status: http.StatusOK, Kind: KindRefused,
			Message: "declined (category " + cat + ")", Err: ErrBlocked}
	}
	var b strings.Builder
	for _, c := range out.Content {
		if c.Type == "text" { // thinking blocks are never surfaced
			b.WriteString(c.Text)
		}
	}
	text := strings.TrimSpace(b.String())
	if wantJSON {
		text = stripFences(text)
	}
	if text == "" {
		if out.StopReason == "max_tokens" {
			return "", &UpstreamError{Provider: "anthropic", Status: http.StatusOK, Kind: KindTruncated, Message: "output limit reached before any text"}
		}
		return "", fmt.Errorf("anthropic: empty response (stop_reason %q)", out.StopReason)
	}
	return text, nil
}

// stripFences removes a ```json ... ``` wrapper the model may add despite the instruction.
func stripFences(s string) string {
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```")
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "```"))
}

// anthropicErrorMessage extracts error.type + error.message from an API error body, truncated.
func anthropicErrorMessage(data []byte) string {
	var e struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	msg := strings.TrimSpace(string(data))
	if json.Unmarshal(data, &e) == nil && e.Error.Message != "" {
		msg = e.Error.Type + ": " + e.Error.Message
	}
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	return msg
}
