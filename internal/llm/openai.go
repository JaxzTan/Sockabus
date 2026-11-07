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

// OpenAICompat calls any OpenAI-compatible /chat/completions endpoint. It exists so persona
// replies can run on a Malaysian-trained model (YTL ILMU, Mesolitica MaLLaM, AI Singapore
// SEA-LION, or a self-hosted vLLM/Ollama) while extraction stays on Claude:
//
//	SOCKABUS_PERSONA_PROVIDER=openai
//	SOCKABUS_OPENAI_BASE_URL=https://<provider>/v1   # from the provider's dashboard/docs
//	SOCKABUS_OPENAI_API_KEY=...
//	SOCKABUS_PERSONA_MODEL=<model id>
//
// No base URL is built in: take it from the provider's own docs.
type OpenAICompat struct {
	BaseURL, Key, Model string
	HTTP                *http.Client
}

func NewOpenAICompat(baseURL, key, model string) *OpenAICompat {
	return &OpenAICompat{BaseURL: strings.TrimRight(baseURL, "/"), Key: key, Model: model, HTTP: &http.Client{Timeout: 10 * time.Minute}}
}

func (o *OpenAICompat) Name() string { return "openai-compat/" + o.Model }

type oaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type oaRequest struct {
	Model     string      `json:"model"`
	Messages  []oaMessage `json:"messages"`
	MaxTokens int         `json:"max_tokens,omitempty"`
}

type oaResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (o *OpenAICompat) buildRequest(req Request) (oaRequest, error) {
	turns := Normalize(req.Turns)
	if len(turns) == 0 || turns[len(turns)-1].Role != RoleUser {
		return oaRequest{}, errors.New("openai-compat: conversation must end with a user turn")
	}
	system := req.System
	if req.JSON {
		system += "\n\nRespond with JSON only, no prose and no code fences."
	}
	msgs := make([]oaMessage, 0, len(turns)+1)
	if strings.TrimSpace(system) != "" {
		msgs = append(msgs, oaMessage{Role: "system", Content: system})
	}
	for _, t := range turns {
		role := "user"
		if t.Role == RoleModel {
			role = "assistant"
		}
		msgs = append(msgs, oaMessage{Role: role, Content: t.Text})
	}
	return oaRequest{Model: o.Model, Messages: msgs, MaxTokens: req.MaxOutputTokens}, nil
}

func (o *OpenAICompat) Generate(ctx context.Context, req Request) (string, error) {
	body, err := o.buildRequest(req)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.BaseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("content-type", "application/json")
	if o.Key != "" {
		httpReq.Header.Set("authorization", "Bearer "+o.Key)
	}
	res, err := o.HTTP.Do(httpReq)
	if err != nil {
		return "", transportError("openai-compat", err)
	}
	defer func() { _ = res.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return "", transportError("openai-compat", err)
	}
	var out oaResponse
	decodeErr := json.Unmarshal(data, &out)
	if res.StatusCode != http.StatusOK {
		msg := ""
		if decodeErr == nil && out.Error != nil {
			msg = out.Error.Message
		}
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return "", httpError("openai-compat", res, msg)
	}
	if decodeErr != nil {
		return "", fmt.Errorf("openai-compat: decode: %w", decodeErr)
	}
	if len(out.Choices) == 0 {
		return "", &UpstreamError{Provider: "openai-compat", Status: http.StatusOK, Kind: KindUnavailable, Message: "no choices"}
	}
	c := out.Choices[0]
	if c.FinishReason == "content_filter" {
		return "", &UpstreamError{Provider: "openai-compat", Status: http.StatusOK, Kind: KindRefused, Message: "content_filter", Err: ErrBlocked}
	}
	text := stripThink(c.Message.Content)
	if text == "" {
		kind := KindUnavailable
		if c.FinishReason == "length" {
			kind = KindTruncated
		}
		return "", &UpstreamError{Provider: "openai-compat", Status: http.StatusOK, Kind: kind, Message: "empty output"}
	}
	return text, nil
}

// stripThink drops a leading <think>...</think> block that reasoning models served through
// vLLM/Ollama put in the content; the persona must never send its reasoning to the scammer.
func stripThink(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "<think>") {
		if i := strings.Index(s, "</think>"); i >= 0 {
			return strings.TrimSpace(s[i+len("</think>"):])
		}
		return "" // reasoning never finished: no usable answer
	}
	return s
}
