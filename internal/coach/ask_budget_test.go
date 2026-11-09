package coach_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"domestique/internal/coach"
	"domestique/internal/coach/coachtest"
)

// Tool results are cut to fit a small model's context: one result at a
// time, and all of one answer's together. Ollama silently drops the oldest
// messages (the question) from a request longer than its window.
func TestAskToolBudget(t *testing.T) {
	var seen []int
	f := &coachtest.Fake{Tools: func(msgs []coach.Message, _ []string) coach.Message {
		last := msgs[len(msgs)-1]
		if last.Role == "tool" {
			seen = append(seen, utf8.RuneCountInString(last.Content))
		}
		if len(seen) < 3 {
			return coach.Message{Role: "assistant", ToolCalls: []coach.ToolCall{{ID: "c", Type: "function",
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{Name: "get_trends", Arguments: "{}"}}}}
		}
		return coach.Message{Role: "assistant", Content: "Plenty of data, nothing to add."}
	}}
	tools := []coach.ToolDef{{Name: "get_trends", Description: "trends", Parameters: map[string]any{"type": "object"}}}
	run := func(context.Context, string, map[string]any) (string, error) {
		b, _ := json.Marshal(strings.Repeat("x", 20000))
		return string(b), nil
	}
	if _, err := f.AI(t).Ask(context.Background(), "trends?", nil, "2026-11-08", tools, run); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 3 {
		t.Fatalf("results seen: %v", seen)
	}
	total := 0
	for _, n := range seen[:2] {
		if n > 4100 {
			t.Errorf("one result of %d characters", n)
		}
		total += n
	}
	if total > 9200 || seen[2] > 1200 {
		t.Errorf("results %v: over the budget", seen)
	}
}
