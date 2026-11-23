package llm

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

// OpenAICompatible calls a chat-completions endpoint such as OpenAI or OpenRouter.
type OpenAICompatible struct {
	apiKey  string
	model   string
	baseURL string
	client  *http.Client
}

func NewOpenAICompatible(apiKey, model, baseURL string) *OpenAICompatible {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return &OpenAICompatible{apiKey: apiKey, model: model, baseURL: strings.TrimRight(baseURL, "/"), client: &http.Client{Timeout: 60 * time.Second}}
}

func (c *OpenAICompatible) Chat(ctx context.Context, request Request) (Response, error) {
	type function struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters,omitempty"`
	}
	type tool struct {
		Type     string   `json:"type"`
		Function function `json:"function"`
	}
	type toolCall struct {
		ID       string `json:"id,omitempty"`
		Type     string `json:"type"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	}
	type message struct {
		Role       string     `json:"role"`
		Content    string     `json:"content,omitempty"`
		ToolCallID string     `json:"tool_call_id,omitempty"`
		ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	}
	type payload struct {
		Model           string    `json:"model"`
		ReasoningEffort string    `json:"reasoning_effort,omitempty"`
		Messages        []message `json:"messages"`
		Tools           []tool    `json:"tools,omitempty"`
		ResponseFormat  any       `json:"response_format,omitempty"`
	}
	p := payload{Model: c.model, ReasoningEffort: "none", Messages: make([]message, 0, len(request.Messages)+1)}
	if request.System != "" {
		p.Messages = append(p.Messages, message{Role: "system", Content: request.System})
	}
	for _, item := range request.Messages {
		m := message{Role: item.Role, Content: item.Content, ToolCallID: item.ToolCallID}
		for _, call := range item.ToolCalls {
			raw, err := json.Marshal(call.Arguments)
			if err != nil {
				return Response{}, err
			}
			m.ToolCalls = append(m.ToolCalls, toolCall{ID: call.ID, Type: "function", Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{call.Name, string(raw)}})
		}
		p.Messages = append(p.Messages, m)
	}
	for _, item := range request.Tools {
		p.Tools = append(p.Tools, tool{Type: "function", Function: function{Name: item.Name, Description: item.Description, Parameters: item.Parameters}})
	}
	if len(request.OutputSchema) > 0 {
		p.ResponseFormat = map[string]string{"type": "json_object"}
	}
	body, err := json.Marshal(p)
	if err != nil {
		return Response{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return Response{}, fmt.Errorf("send model request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return Response{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Response{}, fmt.Errorf("model endpoint returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var result struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string          `json:"name"`
						Arguments json.RawMessage `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return Response{}, fmt.Errorf("decode model response: %w", err)
	}
	if len(result.Choices) == 0 {
		return Response{}, fmt.Errorf("model endpoint returned no choices")
	}
	messageResult := Message{Role: "assistant", Content: result.Choices[0].Message.Content}
	for _, call := range result.Choices[0].Message.ToolCalls {
		arguments := call.Function.Arguments
		if len(arguments) > 0 && arguments[0] == '"' {
			var encoded string
			if err := json.Unmarshal(arguments, &encoded); err != nil {
				return Response{}, fmt.Errorf("decode tool arguments: %w", err)
			}
			arguments = json.RawMessage(encoded)
		}
		messageResult.ToolCalls = append(messageResult.ToolCalls, ToolCall{ID: call.ID, Name: call.Function.Name, Arguments: arguments})
	}
	return Response{Message: messageResult, ToolCalls: messageResult.ToolCalls}, nil
}
