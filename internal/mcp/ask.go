package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"domestique/internal/coach"
)

// The read-only tools, offered to the LLM when the athlete asks about their
// own data (POST /v1/ask). They run through the same REST handlers as MCP
// and the app, so an answer can't disagree with what the athlete sees.
//
// askable is an allowlist, not "every tool marked read-only": a tool added
// later is offered only once someone decides it should be.
var askable = map[string]bool{
	"get_today":             true,
	"get_recent_activities": true,
	"get_activity_detail":   true,
	"get_week_note":         true,
	"get_training_load":     true,
	"get_recovery":          true,
	"get_trends":            true,
	"get_gear":              true,
	"list_templates":        true,
}

func readTool(t tool) bool {
	ro, _ := t.Annotations["readOnlyHint"].(bool)
	return askable[t.Name] && ro
}

// ReadTools lists the tools the LLM may call to answer a question.
func (s *Server) ReadTools() []coach.ToolDef {
	var out []coach.ToolDef
	for _, t := range tools {
		if readTool(t) {
			out = append(out, coach.ToolDef{Name: t.Name, Description: t.Description, Parameters: t.InputSchema})
		}
	}
	return out
}

// RunReadTool returns a runner for ReadTools that calls through to REST as
// the user who made r. Any other tool is refused.
func (s *Server) RunReadTool(r *http.Request) coach.ToolRunner {
	return func(ctx context.Context, name string, args map[string]any) (string, error) {
		for _, t := range tools {
			if t.Name != name {
				continue
			}
			if !readTool(t) {
				break
			}
			v, err := t.call(s, r.WithContext(ctx), args)
			if err != nil {
				return "", err
			}
			b, err := json.Marshal(v)
			return string(b), err
		}
		return "", fmt.Errorf("tool %q is not offered", name)
	}
}
