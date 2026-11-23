// package llm defines the provider-neutral request and response contract used by Citizen agents.
package llm

import (
	"context"
	"encoding/json"
)

// Client sends one model request and returns either a final answer or requested tool calls.
type Client interface {
	Chat(ctx context.Context, request Request) (Response, error)
}

// Request contains the system instructions, conversation, available tools, and optional output schema.
type Request struct {
	System       string
	Messages     []Message
	Tools        []ToolDefinition
	OutputSchema json.RawMessage
}

// Response contains the model's message and any requested tool calls.
type Response struct {
	Message   Message
	ToolCalls []ToolCall
}

// Message is one model or tool result in the current run.
type Message struct {
	Role       string
	Content    string
	ToolCallID string
	ToolName   string
	ToolCalls  []ToolCall
}

// ToolCall identifies one model-requested operation and its JSON arguments.
type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

// ToolDefinition describes a server-approved operation available to the model.
type ToolDefinition struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}
