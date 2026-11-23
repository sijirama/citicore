package runtime

import (
	"citizen/core/internal/agents"
	"citizen/core/internal/llm"
	"citizen/core/internal/tools"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type budgetClient struct{ calls int }

func (c *budgetClient) Chat(_ context.Context, request llm.Request) (llm.Response, error) {
	c.calls++
	if c.calls == 1 {
		return llm.Response{Message: llm.Message{Role: "assistant"}, ToolCalls: []llm.ToolCall{
			{ID: "one", Name: "search", Arguments: json.RawMessage(`{"query":"first"}`)},
			{ID: "two", Name: "search", Arguments: json.RawMessage(`{"query":"second"}`)},
		}}, nil
	}
	if len(request.Tools) != 0 {
		panic("tools should be unavailable after the budget is spent")
	}
	return llm.Response{Message: llm.Message{Role: "assistant", Content: "done"}}, nil
}

func TestRunReturnsEvidenceWhenModelRequestsTooManyTools(t *testing.T) {
	client := &budgetClient{}
	executed := 0
	agent := agents.Agent{
		Name: "test", Client: client, MaxSteps: 3, MaxToolCalls: 1,
		Executor: tools.NewExecutor(tools.Tool{
			Definition: llm.ToolDefinition{Name: "search"},
			Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
				executed++
				return json.RawMessage(`[{"id":"passage"}]`), nil
			},
		}),
	}
	transcript, err := Run(context.Background(), agent, "question")
	if err != nil {
		t.Fatal(err)
	}
	if executed != 1 || client.calls != 2 || transcript.FinalMessage.Content != "done" {
		t.Fatalf("unexpected run: executed=%d calls=%d transcript=%+v", executed, client.calls, transcript)
	}
	if len(transcript.ToolResults) != 2 || !strings.Contains(string(transcript.ToolResults[1].Output), "search limit reached") {
		t.Fatalf("missing budget response: %+v", transcript.ToolResults)
	}
	if string(transcript.ToolResults[0].Arguments) != `{"query":"first"}` {
		t.Fatalf("search query was not recorded: %s", transcript.ToolResults[0].Arguments)
	}
}
