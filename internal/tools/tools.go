// package tools defines server-owned tool contracts and the executor that limits model requests to registered operations.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"citizen/core/internal/documents"
	"citizen/core/internal/llm"
)

// Tool is one model-callable operation with a server-side handler.
type Tool struct {
	Definition llm.ToolDefinition
	Handler    func(context.Context, json.RawMessage) (json.RawMessage, error)
}

// Executor holds the exact tools an agent is allowed to invoke.
type Executor struct {
	registered  map[string]Tool
	definitions []llm.ToolDefinition
}

// NewExecutor creates an executor from a fixed set of registered tools.
func NewExecutor(registered ...Tool) *Executor {
	executor := &Executor{registered: make(map[string]Tool, len(registered)), definitions: make([]llm.ToolDefinition, 0, len(registered))}
	for _, tool := range registered {
		executor.registered[tool.Definition.Name] = tool
		executor.definitions = append(executor.definitions, tool.Definition)
	}
	return executor
}

// Definitions returns the model-visible descriptions for registered tools.
func (executor *Executor) Definitions() []llm.ToolDefinition {
	return append([]llm.ToolDefinition(nil), executor.definitions...)
}

// Execute calls a registered handler and rejects every unregistered model request.
func (executor *Executor) Execute(ctx context.Context, call llm.ToolCall) (json.RawMessage, error) {
	tool, exists := executor.registered[call.Name]
	if !exists {
		return nil, fmt.Errorf("tool %q is not available to this agent", call.Name)
	}
	if tool.Handler == nil {
		return nil, fmt.Errorf("tool %q has no handler", call.Name)
	}
	return tool.Handler(ctx, call.Arguments)
}

type documentSearchArguments struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

// NewDocumentSearch creates the chat agent's only knowledge tool; it can query the approved Citizen document library.
func NewDocumentSearch(searcher documents.Searcher) Tool {
	return Tool{
		Definition: llm.ToolDefinition{
			Name:        "search_documents",
			Description: "Find passages in the approved Citizen.ng public document library. Use this tool for every factual civic answer. It does not search the open web or news.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"A focused phrase describing the civic question"},"limit":{"type":"integer","minimum":1,"maximum":4}},"required":["query"],"additionalProperties":false}`),
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
			if searcher == nil {
				return nil, fmt.Errorf("document search is not configured")
			}
			var arguments documentSearchArguments
			if err := json.Unmarshal(raw, &arguments); err != nil {
				return nil, fmt.Errorf("decode document search arguments: %w", err)
			}
			arguments.Query = strings.TrimSpace(arguments.Query)
			if arguments.Query == "" {
				return nil, fmt.Errorf("document search query cannot be empty")
			}
			if arguments.Limit < 1 || arguments.Limit > 4 {
				arguments.Limit = 4
			}
			results, err := searcher.Search(ctx, arguments.Query, arguments.Limit)
			if err != nil {
				return nil, fmt.Errorf("search approved documents: %w", err)
			}
			for i := range results {
				if len(results[i].Excerpt) > 1400 {
					results[i].Excerpt = results[i].Excerpt[:1400] + "…"
				}
			}
			encoded, err := json.Marshal(results)
			if err != nil {
				return nil, fmt.Errorf("encode document passages: %w", err)
			}
			return encoded, nil
		},
	}
}
