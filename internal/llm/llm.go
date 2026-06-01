// Package llm abstracts the diagnosis model provider behind a single interface
// so Claude, OpenAI, or a local/mock model can be swapped via config.
package llm

import (
	"context"
	"fmt"

	"github.com/overstarry/sloth/internal/config"
)

// Tool is a function the model may call to fetch additional context on demand
// (e.g. get_explain_plan, get_table_schema). The agent loop resolves calls.
type Tool struct {
	Name        string
	Description string
	// Handler executes the tool call with raw JSON args and returns raw JSON.
	Handler func(ctx context.Context, args []byte) ([]byte, error)
}

// Request is a single diagnosis prompt.
type Request struct {
	System string
	User   string
	Tools  []Tool
}

// Provider is implemented by each model backend.
type Provider interface {
	// Name identifies the backend.
	Name() string
	// Complete runs the prompt (resolving any tool calls) and returns text.
	Complete(ctx context.Context, req Request) (string, error)
}

// New constructs a Provider from config.
func New(c config.LLMConfig) (Provider, error) {
	switch c.Provider {
	case "mock", "":
		return &mockProvider{}, nil
	case "claude":
		return newClaude(c), nil
	case "openai":
		return nil, fmt.Errorf("openai provider not yet implemented")
	default:
		return nil, fmt.Errorf("unknown llm provider: %q", c.Provider)
	}
}
