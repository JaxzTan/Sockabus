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
	"time"
)

const (
	DefaultGeminiModel   = "gemini-3.8-flash"
	defaultGeminiBaseURL = "https://generativelanguage.googleapis.com/v1beta"
)

// Gemini calls the Gemini API generateContent endpoint.
//
// Gemini 3.8 Flash notes (ai.google.dev, "What's new in Gemini 3.8 Flash"): thinking is set with
// generationConfig.thinkingConfig.thinkingLevel (low | medium | high; "minimal" is rejected);
// temperature/top_p/top_k are deprecated, so they are not sent; the final turn must be a
// non-empty user turn and prefilled model turns are not allowed.
type Gemini struct {
	Key           string
	Model         string
	BaseURL       string
	ThinkingLevel string
	HTTP          *http.Client
}

func NewGemini(key, model, thinkingLevel string) *Gemini {
	if model == "" {
		model = DefaultGeminiModel
	}
	return &Gemini{Key: key, Model: model, BaseURL: defaultGeminiBaseURL, ThinkingLevel: thinkingLevel,
		HTTP: &http.Client{Timeout: 90 * time.Second}}
}

func (g *Gemini) Name() string { return "gemini/" + g.Model }

type gPart struct {
	Text    string `json:"text,omitempty"`
	Thought bool   `json:"thought,omitempty"`
}

type gContent struct {
	Role  string  `json:"role,omitempty"`
	Parts []gPart `json:"parts"`
}

type gRequest struct {
	SystemInstruction *gContent  `json:"systemInstruction,omitempty"`
	Contents          []gContent `json:"contents"`
	GenerationConfig  gGenConfig `json:"generationConfig"`
}

type gGenConfig struct {
	MaxOutputTokens  int              `json:"maxOutputTokens,omitempty"`
	ResponseMimeType string           `json:"responseMimeType,omitempty"`
	ThinkingConfig   *gThinkingConfig `json:"thinkingConfig,omitempty"`
}

type gThinkingConfig struct {
	ThinkingLevel string `json:"thinkingLevel,omitempty"`
}

type gResponse struct {
	Candidates []struct {
		Content      gContent `json:"content"`
		FinishReason string   `json:"finishReason"`
	} `json:"candidates"`
	PromptFeedback struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
}

// buildRequest is split out so it can be unit tested without the network.
func (g *Gemini) buildRequest(req Request) (gRequest, error) {
	turns := Normalize(req.Turns)
	if len(turns) == 0 || turns[len(turns)-1].Role != RoleUser {
		return gRequest{}, errors.New("gemini: conversation must end with a non-empty user turn")
	}
	body := gRequest{GenerationConfig: gGenConfig{MaxOutputTokens: req.MaxOutputTokens}}
	if req.System != "" {
		body.SystemInstruction = &gContent{Parts: []gPart{{Text: req.System}}}
	}
	for _, t := range turns {
		role := "user"
		if t.Role == RoleModel {
			role = "model"
		}
		body.Contents = append(body.Contents, gContent{Role: role, Parts: []gPart{{Text: t.Text}}})
	}
	if req.JSON {
		body.GenerationConfig.ResponseMimeType = "application/json"
	}
	level := string(req.Effort)
	if level == "" {
		level = g.ThinkingLevel
	}
	if level != "" {
		body.GenerationConfig.ThinkingConfig = &gThinkingConfig{ThinkingLevel: level}
	}
	return body, nil
}

func (g *Gemini) Generate(ctx context.Context, req Request) (string, error) {
	body, err := g.buildRequest(req)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf("%s/models/%s:generateContent", strings.TrimRight(g.BaseURL, "/"), g.Model)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", g.Key) // header, never a query param, so keys stay out of logs

	res, err := g.HTTP.Do(httpReq)
	if err != nil {
		return "", transportError("gemini", err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return "", transportError("gemini", err)
	}
	if res.StatusCode != http.StatusOK {
		return "", httpError("gemini", res, apiErrorMessage(data))
	}
	return parseGemini(data)
}

func parseGemini(data []byte) (string, error) {
	var out gResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("gemini: decode: %w", err)
	}
	if out.PromptFeedback.BlockReason != "" {
		return "", fmt.Errorf("%w (%s)", ErrBlocked, out.PromptFeedback.BlockReason)
	}
	if len(out.Candidates) == 0 {
		return "", errors.New("gemini: no candidates")
	}
	c := out.Candidates[0]
	switch c.FinishReason {
	case "SAFETY", "PROHIBITED_CONTENT", "BLOCKLIST", "SPII", "RECITATION":
		return "", fmt.Errorf("%w (%s)", ErrBlocked, c.FinishReason)
	}
	var b strings.Builder
	for _, p := range c.Content.Parts {
		if p.Thought {
			continue // never surface the model's reasoning to a scammer
		}
		b.WriteString(p.Text)
	}
	text := strings.TrimSpace(b.String())
	if text == "" {
		return "", fmt.Errorf("gemini: empty response (finish reason %q)", c.FinishReason)
	}
	return text, nil
}

// apiErrorMessage extracts error.message from a Google API error body, truncated.
func apiErrorMessage(data []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
	}
	msg := strings.TrimSpace(string(data))
	if json.Unmarshal(data, &e) == nil && e.Error.Message != "" {
		msg = e.Error.Status + ": " + e.Error.Message
	}
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	return msg
}
