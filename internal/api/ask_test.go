package api

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"domestique/internal/coach"
	"domestique/internal/coach/coachtest"
	"domestique/internal/mcp"
)

func askAPI(t *testing.T, f *coachtest.Fake) (*API, func(body string) (int, coach.AskResult, string)) {
	t.Helper()
	a, _, h := testAPI(t)
	a.Tools = &mcp.Server{API: h}
	if f != nil {
		a.Coach = &coach.Writer{AI: f.AI(t), KV: a.Store, Log: a.Log}
	}
	return a, func(body string) (int, coach.AskResult, string) {
		w := call(t, h, "POST", "/v1/ask", body)
		var r coach.AskResult
		_ = json.Unmarshal(w.Body.Bytes(), &r)
		return w.Code, r, w.Body.String()
	}
}

func lastTool(msgs []coach.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "tool" {
			return msgs[i].Content
		}
	}
	return ""
}

func toolCall(id, name, args string) coach.Message {
	c := coach.ToolCall{ID: id, Type: "function"}
	c.Function.Name, c.Function.Arguments = name, args
	return coach.Message{Role: "assistant", ToolCalls: []coach.ToolCall{c}}
}

// With no model there's nothing to answer with.
func TestAskUnavailable(t *testing.T) {
	_, ask := askAPI(t, nil)
	if code, _, body := ask(`{"question":"How is my form?"}`); code != 503 {
		t.Fatalf("no model: %d %s", code, body)
	}
	_, ask = askAPI(t, &coachtest.Fake{}) // a model kind with nothing chosen
	if code, _, body := ask(`{"question":"How is my form?"}`); code != 503 {
		t.Fatalf("no LLM chosen: %d %s", code, body)
	}
	if code, _, _ := ask(`{"question":""}`); code != 400 {
		t.Fatalf("empty question: %d", code)
	}
}

// The model looks something up through the app's own REST handlers, then
// answers with a figure from what it found.
func TestAskLooksUp(t *testing.T) {
	var offered []string
	num := regexp.MustCompile(`\d+(\.\d+)?`)
	f := &coachtest.Fake{Tools: func(msgs []coach.Message, tools []string) coach.Message {
		offered = tools
		out := lastTool(msgs)
		if out == "" {
			return toolCall("c1", "get_training_load", `{"days": 14}`)
		}
		n := num.FindString(out)
		if n == "" || strings.Contains(out, `"error"`) {
			t.Errorf("tool result: %s", out)
		}
		return coach.Message{Role: "assistant", Content: "**Fitness** is " + n + "."}
	}}
	_, ask := askAPI(t, f)
	code, r, body := ask(`{"question":"How is my form?"}`)
	if code != 200 || !r.Checked || !slices.Equal(r.Tools, []string{"get_training_load"}) || strings.Contains(r.Answer, "**") {
		t.Fatalf("ask: %d %s", code, body)
	}
	for _, w := range []string{"save_template", "delete_template", "plan_strength_session"} {
		if slices.Contains(offered, w) {
			t.Errorf("write tool %s offered", w)
		}
	}
	if len(offered) != 9 {
		t.Errorf("offered %v", offered)
	}
}

// A figure that's in none of the data is retried once, then replaced with
// an honest fallback.
func TestAskRejectsInventedNumbers(t *testing.T) {
	var calls atomic.Int32
	f := &coachtest.Fake{Tools: func(msgs []coach.Message, _ []string) coach.Message {
		calls.Add(1)
		return coach.Message{Role: "assistant", Content: "Your FTP is 312 W."}
	}}
	_, ask := askAPI(t, f)
	code, r, body := ask(`{"question":"What's my FTP?"}`)
	if code != 200 || r.Checked || r.Answer != coach.AskFallback || calls.Load() != 2 {
		t.Fatalf("ask: %d %s (%d calls)", code, body, calls.Load())
	}
	// A number the athlete gave in the conversation may be used.
	code, r, body = ask(`{"question":"Is that above 300?","history":[{"question":"I think my FTP is 312 W","answer":"I can't see that."}]}`)
	if code != 200 || !r.Checked {
		t.Fatalf("number from the conversation: %d %s", code, body)
	}
}

// A call to a tool that wasn't offered (a write tool) runs nothing.
func TestAskRefusesWriteTools(t *testing.T) {
	var got string
	f := &coachtest.Fake{Tools: func(msgs []coach.Message, _ []string) coach.Message {
		if out := lastTool(msgs); out != "" {
			got = out
			return coach.Message{Role: "assistant", Content: "I can't change templates."}
		}
		return toolCall("c1", "delete_template", `{"id": "lower-a"}`)
	}}
	a, ask := askAPI(t, f)
	before, _ := a.Store.ListTemplates(t.Context())
	code, r, body := ask(`{"question":"Delete my lower template"}`)
	if code != 200 || len(r.Tools) != 0 || !strings.Contains(got, "no tool named") {
		t.Fatalf("ask: %d %s, tool said %s", code, body, got)
	}
	if after, _ := a.Store.ListTemplates(t.Context()); len(after) != len(before) {
		t.Fatal("a template was deleted")
	}
}
