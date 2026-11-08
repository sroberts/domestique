package coach

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Asking about your own data: the LLM looks things up with read-only tools
// (the MCP server's, so answers can't disagree with the app), then answers.
// Every number in the answer must appear in what the tools returned, the
// question, or the conversation so far; otherwise the athlete gets an
// honest "couldn't" instead of a made-up figure.

// Turn is one earlier question and its answer, for follow-ups.
type Turn struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// AskResult is the answer to one question. Checked is false when the
// model's answer failed the checks and Answer is the fallback.
type AskResult struct {
	Answer  string   `json:"answer"`
	Model   string   `json:"model,omitempty"`
	Tools   []string `json:"tools"`
	Checked bool     `json:"checked"`
}

// ToolRunner runs one offered tool and returns its result as text.
type ToolRunner func(ctx context.Context, name string, args map[string]any) (string, error)

const (
	// AskRounds caps the model's turns: tool rounds, then one to answer.
	AskRounds = 6
	// AskHistory is how many earlier turns are sent with a question.
	AskHistory = 4
	// maxToolResult keeps one tool's result from filling a small model's
	// context; the rest is cut with a marker the model can see.
	maxToolResult = 12000
	maxAnswer     = 1200
	maxQuestion   = 500
)

// AskFallback is what the athlete reads when the model's answer couldn't
// be checked against the data.
const AskFallback = "I couldn't answer that from your data without guessing at numbers. Try asking about one thing: a week, a ride, or one lift."

func askSystem(today string) string {
	return `You answer the athlete's questions about their own training, recovery and gear data, with the tools given. Look the answer up with the tools before answering; never answer from memory or general knowledge about the athlete. Today is ` + today + `.

Answer in plain text, at most five short sentences, no lists, tables or markdown. Quote numbers exactly as the tools return them, with units. If the tools don't hold the answer, say so in one sentence. Don't give medical advice.

` + voice
}

// Ask answers one question about the athlete's data, calling tools through
// run. history is the conversation so far, oldest first; only the last
// AskHistory turns are sent.
func (a *AI) Ask(ctx context.Context, question string, history []Turn, today string, tools []ToolDef, run ToolRunner) (AskResult, error) {
	question = strings.TrimSpace(question)
	if utf8.RuneCountInString(question) > maxQuestion {
		question = string([]rune(question)[:maxQuestion])
	}
	if len(history) > AskHistory {
		history = history[len(history)-AskHistory:]
	}
	offered := map[string]bool{}
	for _, t := range tools {
		offered[t.Name] = true
	}
	msgs := []Message{{Role: "system", Content: askSystem(today)}}
	// What the answer's numbers may come from.
	facts := []string{question}
	for _, t := range history {
		msgs = append(msgs, Message{Role: "user", Content: t.Question}, Message{Role: "assistant", Content: t.Answer})
		facts = append(facts, t.Question, t.Answer)
	}
	msgs = append(msgs, Message{Role: "user", Content: question})

	res := AskResult{Tools: []string{}}
	retried := false
	for round := 0; round < AskRounds; round++ {
		offer := tools
		if round == AskRounds-1 {
			offer = nil // last turn: answer with what's been found
		}
		m, model, err := a.ChatTools(ctx, msgs, offer)
		if err != nil {
			return res, err
		}
		res.Model = model
		msgs = append(msgs, m)
		if len(m.ToolCalls) > 0 && offer != nil {
			for _, c := range m.ToolCalls {
				out := a.runTool(ctx, c, offered, run)
				if offered[c.Function.Name] && !contains(res.Tools, c.Function.Name) {
					res.Tools = append(res.Tools, c.Function.Name)
				}
				facts = append(facts, out)
				msgs = append(msgs, Message{Role: "tool", ToolCallID: c.ID, Content: out})
			}
			continue
		}
		answer := cleanAnswer(m.Content)
		err = checkProse(answer, maxAnswer, facts)
		if err == nil {
			res.Answer, res.Checked = answer, true
			return res, nil
		}
		if retried || round >= AskRounds-1 {
			break
		}
		retried = true
		msgs = append(msgs, Message{Role: "user", Content: fmt.Sprintf(
			"That answer can't be shown: %s. Answer again, using only numbers exactly as the tool results give them, no exclamation marks, at most five sentences.", reason(err))})
	}
	res.Answer = AskFallback
	return res, nil
}

func (a *AI) runTool(ctx context.Context, c ToolCall, offered map[string]bool, run ToolRunner) string {
	name := c.Function.Name
	if !offered[name] {
		return fmt.Sprintf(`{"error": "no tool named %q"}`, name)
	}
	args := map[string]any{}
	if s := strings.TrimSpace(c.Function.Arguments); s != "" && s != "null" {
		if err := json.Unmarshal([]byte(s), &args); err != nil {
			return `{"error": "arguments must be a JSON object"}`
		}
	}
	out, err := run(ctx, name, args)
	if err != nil {
		b, _ := json.Marshal(map[string]string{"error": err.Error()})
		return string(b)
	}
	if utf8.RuneCountInString(out) > maxToolResult {
		out = string([]rune(out)[:maxToolResult]) + " …(cut: ask for fewer days or one item)"
	}
	return out
}

// cleanAnswer strips thinking and the markdown small models add anyway.
func cleanAnswer(s string) string {
	s = thinkRe.ReplaceAllString(s, "")
	s = strings.NewReplacer("**", "", "__", "", "`", "").Replace(s)
	return strings.TrimSpace(s)
}

// reason is a check failure without the quoted reply.
func reason(err error) string {
	r, _, _ := strings.Cut(err.Error(), ": \"")
	return strings.TrimSuffix(r, ":")
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
