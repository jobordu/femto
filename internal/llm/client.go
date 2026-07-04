// Package llm is femto's dependency-free OpenAI-compatible chat client. It talks to
// any /v1/chat/completions endpoint (NIM, Flynn, OpenAI-shaped providers), in text
// or native tool-calling mode, with exponential backoff on rate-limit/transient
// errors — ported from the flynn micro-agent's nim_llm_fn.
package llm

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Message is one chat turn. ToolCalls/ToolCallID are only set on native turns.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ToolCall is a native function call as returned/echoed in the OpenAI schema.
type ToolCall struct {
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// Reply is one assistant response: Content for text mode, ToolCalls for native.
type Reply struct {
	Content   string
	ToolCalls []ToolCall
}

// Client calls a single model on an OpenAI-compatible endpoint.
type Client struct {
	BaseURL     string // e.g. https://integrate.api.nvidia.com/v1
	APIKey      string
	Model       string
	Temperature float64
	MaxTokens   int
	Tools       []any // function-calling schemas; non-nil => native mode
	http        *http.Client
	maxRetries  int
}

// retryStatus: 429 = rate limit, 502/503/504 = transient gateway/overload. 500 is
// EXCLUDED — on the NIM free tier it's usually deterministic (broken-model
// EngineCore 500 / system-role), so retrying just wastes backoff.
var retryStatus = map[int]bool{429: true, 502: true, 503: true, 504: true}

// New builds a Client. timeout bounds each HTTP call; proxy (may be "") routes
// through an HTTP CONNECT proxy like the grind's.
func New(baseURL, apiKey, model string, temperature float64, maxTokens int,
	timeout time.Duration, proxy string, tools []any) (*Client, error) {
	tr := &http.Transport{}
	if proxy != "" {
		u, err := url.Parse(proxy)
		if err != nil {
			return nil, err
		}
		tr.Proxy = http.ProxyURL(u)
	}
	return &Client{
		BaseURL:     strings.TrimRight(baseURL, "/"),
		APIKey:      apiKey,
		Model:       model,
		Temperature: temperature,
		MaxTokens:   maxTokens,
		Tools:       tools,
		http:        &http.Client{Timeout: timeout, Transport: tr},
		maxRetries:  6,
	}, nil
}

// Call sends the conversation and returns the assistant Reply.
func (c *Client) Call(messages []Message) (Reply, error) {
	body := map[string]any{
		"model":       c.Model,
		"messages":    messages,
		"temperature": c.Temperature,
		"max_tokens":  c.MaxTokens,
	}
	if c.Tools != nil {
		body["tools"] = c.Tools
		body["tool_choice"] = "auto"
	}
	raw, _ := json.Marshal(body)

	resp, err := c.postRetry(c.BaseURL+"/chat/completions", raw)
	if err != nil {
		return Reply{}, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return Reply{}, errors.New("llm http " + strconv.Itoa(resp.StatusCode) + ": " +
			truncate(string(data), 200))
	}
	return parseReply(data)
}

// postRetry POSTs with exponential backoff on rate-limit/transient/timeout errors.
// Honors a numeric Retry-After header; else 2**attempt (capped 30s) + jitter.
func (c *Client) postRetry(fullURL string, raw []byte) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequest(http.MethodPost, fullURL, bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.http.Do(req)
		if err != nil { // timeout / transport error
			if attempt >= c.maxRetries {
				return nil, err
			}
			c.sleep(attempt, "")
			continue
		}
		if retryStatus[resp.StatusCode] && attempt < c.maxRetries {
			ra := resp.Header.Get("Retry-After")
			resp.Body.Close()
			c.sleep(attempt, ra)
			continue
		}
		return resp, nil
	}
}

// sleep waits before a retry: Retry-After seconds if numeric, else exp backoff+jitter.
func (c *Client) sleep(attempt int, retryAfter string) {
	delay := math.Min(math.Pow(2, float64(attempt)), 30)
	if retryAfter != "" {
		if v, err := strconv.ParseFloat(strings.TrimSpace(retryAfter), 64); err == nil {
			delay = v
		}
	}
	time.Sleep(time.Duration((delay + rand.Float64()) * float64(time.Second)))
}

// parseReply extracts text or tool_calls from a chat-completions response. Reasoning
// models put output in reasoning_content/reasoning when content is empty — fall back
// so we don't silently discard the whole response.
func parseReply(data []byte) (Reply, error) {
	var r struct {
		Choices []struct {
			Message struct {
				Content          string     `json:"content"`
				ReasoningContent string     `json:"reasoning_content"`
				Reasoning        string     `json:"reasoning"`
				ToolCalls        []ToolCall `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return Reply{}, err
	}
	if len(r.Choices) == 0 {
		return Reply{}, nil
	}
	m := r.Choices[0].Message
	content := m.Content
	if content == "" {
		content = firstNonEmpty(m.ReasoningContent, m.Reasoning)
	}
	return Reply{Content: content, ToolCalls: m.ToolCalls}, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
