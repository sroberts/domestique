package coach

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Tool calling over the OpenAI Chat Completions API: Outpost passes tools
// and tool_calls through to the provider unchanged (service spec §15).

// ToolDef is one function the LLM may call. Parameters is a JSON Schema.
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// ToolCall is one call the LLM asked for. Arguments is a JSON object, as text.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// Message is one Chat Completions message: system, user, assistant (with
// any tool calls) or tool (a call's result, by ToolCallID).
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ChatTools sends a conversation and the tools on offer, and returns the
// LLM's next message: an answer, or tool calls to run and send back.
func (a *AI) ChatTools(ctx context.Context, msgs []Message, tools []ToolDef) (Message, string, error) {
	type fn struct {
		Type     string  `json:"type"`
		Function ToolDef `json:"function"`
	}
	ts := make([]fn, len(tools))
	for i, t := range tools {
		ts[i] = fn{"function", t}
	}
	req := map[string]any{"model": "llm", "messages": msgs}
	if len(ts) > 0 {
		req["tools"] = ts
	}
	body, err := json.Marshal(req)
	if err != nil {
		return Message{}, "", err
	}
	raw, err := a.post(ctx, "/openai/v1/chat/completions", body)
	if err != nil {
		return Message{}, "", err
	}
	var out struct {
		Model   string `json:"model"`
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Message{}, "", fmt.Errorf("ai: decode reply: %w", err)
	}
	if len(out.Choices) == 0 {
		return Message{}, out.Model, errors.New("ai: reply has no choices")
	}
	m := out.Choices[0].Message
	m.Role = "assistant"
	return m, out.Model, nil
}
