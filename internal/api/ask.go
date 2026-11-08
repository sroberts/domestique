package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"domestique/internal/coach"
)

// AskTools are the read-only tools the LLM may call to answer a question
// about the athlete's data: the MCP server's, so answers match the app.
type AskTools interface {
	ReadTools() []coach.ToolDef
	RunReadTool(r *http.Request) coach.ToolRunner
}

// askTimeout covers every tool round and the answer. Local models are slow;
// past this the athlete is better off asking something narrower.
const askTimeout = 150 * time.Second

// ask answers a question about the athlete's own data with the operator's
// LLM. There's no rule-based answer to fall back on, so with no model it's
// 503 and the app hides the question box.
func (a *API) ask(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Question string       `json:"question"`
		History  []coach.Turn `json:"history"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	in.Question = strings.TrimSpace(in.Question)
	if in.Question == "" || utf8.RuneCountInString(in.Question) > 500 {
		writeErr(w, http.StatusBadRequest, "question must be 1-500 characters")
		return
	}
	if len(in.History) > coach.AskHistory {
		in.History = in.History[len(in.History)-coach.AskHistory:]
	}
	for i, t := range in.History {
		if utf8.RuneCountInString(t.Question) > 500 || utf8.RuneCountInString(t.Answer) > 2000 {
			writeErr(w, http.StatusBadRequest, "history is too long")
			return
		}
		in.History[i] = coach.Turn{Question: strings.TrimSpace(t.Question), Answer: strings.TrimSpace(t.Answer)}
	}
	if !a.aiOn(r) || a.Tools == nil {
		writeErr(w, http.StatusServiceUnavailable, "asking needs an LLM from Outpost, and none is available")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), askTimeout)
	defer cancel()
	today := time.Now().In(a.loc()).Format("Monday 2 January 2006 (2006-01-02)")
	res, err := a.Coach.AI.Ask(ctx, in.Question, in.History, today, a.Tools.ReadTools(), a.Tools.RunReadTool(r))
	if err != nil {
		switch {
		case r.Context().Err() != nil:
			// The athlete left; nobody to answer.
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			writeErr(w, http.StatusGatewayTimeout, "the model took too long; try a narrower question")
		case coach.Unavailable(err):
			writeErr(w, http.StatusServiceUnavailable, "the AI model isn't available right now")
		default:
			a.Log.Warn("ask", "err", err.Error())
			writeErr(w, http.StatusBadGateway, "the model's reply couldn't be read")
		}
		return
	}
	a.Log.Info("ask", "tools", strings.Join(res.Tools, ","), "checked", res.Checked, "model", res.Model)
	writeJSON(w, http.StatusOK, res)
}
