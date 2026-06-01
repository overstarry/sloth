package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/overstarry/sloth/internal/config"
)

const defaultClaudeURL = "https://api.anthropic.com/v1/messages"

// claudeProvider talks to the Anthropic Messages API. Tool-calling is wired
// through the standard tool_use/tool_result loop.
type claudeProvider struct {
	cfg    config.LLMConfig
	client *http.Client
	url    string
}

func newClaude(c config.LLMConfig) *claudeProvider {
	url := c.BaseURL
	if url == "" {
		url = defaultClaudeURL
	}
	return &claudeProvider{
		cfg:    c,
		client: &http.Client{Timeout: 60 * time.Second},
		url:    url,
	}
}

func (p *claudeProvider) Name() string { return "claude:" + p.cfg.Model }

// Complete runs a minimal tool-use loop against the Messages API. It is kept
// dependency-free (raw HTTP) so sloth has no heavy SDK requirement.
func (p *claudeProvider) Complete(ctx context.Context, req Request) (string, error) {
	if p.cfg.APIKey == "" {
		return "", fmt.Errorf("claude: api key not configured")
	}
	tools := make([]map[string]any, 0, len(req.Tools))
	toolByName := map[string]Tool{}
	for _, t := range req.Tools {
		toolByName[t.Name] = t
		tools = append(tools, map[string]any{
			"name":         t.Name,
			"description":  t.Description,
			"input_schema": map[string]any{"type": "object"},
		})
	}

	messages := []map[string]any{
		{"role": "user", "content": req.User},
	}

	// Bound the agent loop so a misbehaving model cannot spin forever.
	for turn := 0; turn < 8; turn++ {
		body := map[string]any{
			"model":      p.cfg.Model,
			"max_tokens": p.cfg.MaxTokens,
			"system":     req.System,
			"messages":   messages,
		}
		if len(tools) > 0 {
			body["tools"] = tools
		}
		resp, err := p.call(ctx, body)
		if err != nil {
			return "", err
		}

		toolResults, text, usedTool := p.handleContent(ctx, resp.Content, toolByName)
		if !usedTool {
			return text, nil
		}
		messages = append(messages,
			map[string]any{"role": "assistant", "content": resp.Content},
			map[string]any{"role": "user", "content": toolResults},
		)
	}
	return "", fmt.Errorf("claude: tool loop exceeded max turns")
}

type claudeResponse struct {
	Content []json.RawMessage `json:"content"`
}

func (p *claudeProvider) call(ctx context.Context, body map[string]any) (*claudeResponse, error) {
	buf, _ := json.Marshal(body)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("x-api-key", p.cfg.APIKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("claude request: %w", err)
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusOK {
		var b bytes.Buffer
		b.ReadFrom(httpResp.Body)
		return nil, fmt.Errorf("claude http %d: %s", httpResp.StatusCode, b.String())
	}
	var out claudeResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("claude decode: %w", err)
	}
	return &out, nil
}

// handleContent walks the response blocks, executing tool_use blocks and
// collecting any plain text. It returns tool_result blocks for the next turn.
func (p *claudeProvider) handleContent(ctx context.Context, content []json.RawMessage, tools map[string]Tool) (results []map[string]any, text string, usedTool bool) {
	for _, raw := range content {
		var block struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		}
		if err := json.Unmarshal(raw, &block); err != nil {
			continue
		}
		switch block.Type {
		case "text":
			text += block.Text
		case "tool_use":
			usedTool = true
			out := p.runTool(ctx, tools, block.Name, block.Input)
			results = append(results, map[string]any{
				"type":        "tool_result",
				"tool_use_id": block.ID,
				"content":     string(out),
			})
		}
	}
	return results, text, usedTool
}

func (p *claudeProvider) runTool(ctx context.Context, tools map[string]Tool, name string, input json.RawMessage) []byte {
	t, ok := tools[name]
	if !ok {
		return []byte(fmt.Sprintf(`{"error":"unknown tool %q"}`, name))
	}
	out, err := t.Handler(ctx, input)
	if err != nil {
		return []byte(fmt.Sprintf(`{"error":%q}`, err.Error()))
	}
	return out
}
