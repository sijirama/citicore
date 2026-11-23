// package runtime executes one bounded agent run without knowing model-provider or tool implementation details.
package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"citizen/core/internal/agents"
	"citizen/core/internal/llm"
)

// ToolResult records the output returned to the model for one tool invocation.
type ToolResult struct {
	Name      string
	Arguments json.RawMessage
	Output    json.RawMessage
}

// Transcript contains the final model response and the search evidence used during the run.
type Transcript struct {
	FinalMessage llm.Message
	ToolResults  []ToolResult
	Steps        int
}

// Run performs model calls and registered tool calls until the agent returns a final response or reaches its limits.
func Run(ctx context.Context, agent agents.Agent, input string) (Transcript, error) {
	if agent.Client == nil || agent.Executor == nil || agent.MaxSteps < 1 {
		return Transcript{}, fmt.Errorf("agent %q is not configured", agent.Name)
	}
	messages := []llm.Message{{Role: "user", Content: input}}
	toolResults := make([]ToolResult, 0)
	toolCallsUsed := 0
	for step := 0; step < agent.MaxSteps; step++ {
		definitions := agent.Executor.Definitions()
		if toolCallsUsed >= agent.MaxToolCalls {
			definitions = nil
		}
		modelStarted := time.Now()
		response, err := agent.Client.Chat(ctx, llm.Request{
			System: agent.SystemPrompt, Messages: messages, Tools: definitions, OutputSchema: agent.OutputSchema,
		})
		log.Printf("agent model_call step=%d duration=%s tool_calls=%d", step+1, time.Since(modelStarted).Round(time.Millisecond), len(response.ToolCalls))
		if err != nil {
			return Transcript{}, fmt.Errorf("call chat model: %w", err)
		}
		messages = append(messages, response.Message)
		if len(response.ToolCalls) == 0 {
			return Transcript{FinalMessage: response.Message, ToolResults: toolResults, Steps: step + 1}, nil
		}
		for _, call := range response.ToolCalls {
			var output json.RawMessage
			var toolErr error
			if toolCallsUsed >= agent.MaxToolCalls {
				toolErr = fmt.Errorf("search limit reached; answer using passages already returned")
			} else {
				toolCallsUsed++
				toolStarted := time.Now()
				output, toolErr = agent.Executor.Execute(ctx, call)
				log.Printf("agent tool_call name=%s duration=%s error=%t", call.Name, time.Since(toolStarted).Round(time.Millisecond), toolErr != nil)
			}
			if toolErr != nil {
				output, _ = json.Marshal(map[string]string{"error": toolErr.Error()})
			}
			toolResults = append(toolResults, ToolResult{Name: call.Name, Arguments: call.Arguments, Output: output})
			messages = append(messages, llm.Message{Role: "tool", ToolCallID: call.ID, ToolName: call.Name, Content: string(output)})
		}
	}
	return Transcript{}, fmt.Errorf("agent %q exceeded its step limit", agent.Name)
}
