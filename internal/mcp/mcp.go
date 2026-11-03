// Package mcp serves Domestique's data to Claude over the Model Context
// Protocol, Streamable HTTP transport, at a single path (/mcp), as the Outpost
// portal requires. Session state lives in Mcp-Session-Id, never cookies.
//
// Tools are thin: each runs the REST handler it mirrors, so MCP and REST can
// never disagree.
package mcp

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"time"
)

type Server struct {
	// API is the /v1 handler tools call through.
	API     http.Handler
	Version string
}

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcErr         `json:"error,omitempty"`
}

type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations,omitempty"`
	call        func(s *Server, r *http.Request, args map[string]any) (any, error)
}

func obj(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

func num(desc string) map[string]any { return map[string]any{"type": "number", "description": desc} }
func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

var readOnly = map[string]any{"readOnlyHint": true}

var tools = []tool{
	{
		Name: "get_today",
		Description: "Today at a glance: morning recovery (Whoop), last night's sleep, training form (fitness/fatigue/form), " +
			"this week's load by day, the planned strength session with today's prescribed weights, and a readiness call.",
		InputSchema: obj(map[string]any{"date": str("Local date YYYY-MM-DD; defaults to today")}),
		Annotations: readOnly,
		call: func(s *Server, r *http.Request, a map[string]any) (any, error) {
			q := url.Values{}
			if d, ok := a["date"].(string); ok && d != "" {
				q.Set("date", d)
			}
			return s.get(r, "/v1/today", q)
		},
	},
	{
		Name: "get_recent_activities",
		Description: "Activities (rides, indoor rides, strength, runs) newest first, merged across Wahoo, Zwift, Whoop, Strava " +
			"and the app. Each has duration, distance, TSS, intensity, power and HR summary, and which sources recorded it.",
		InputSchema: obj(map[string]any{
			"days":  num("How many days back (default 14, max 365)"),
			"sport": map[string]any{"type": "string", "enum": []string{"all", "ride", "strength", "other"}},
		}),
		Annotations: readOnly,
		call: func(s *Server, r *http.Request, a map[string]any) (any, error) {
			days := intArg(a, "days", 14, 1, 365)
			q := url.Values{"limit": {"200"}, "from": {strconv.FormatInt(time.Now().AddDate(0, 0, -days).UnixMilli(), 10)}}
			if sp, ok := a["sport"].(string); ok && sp != "all" {
				q.Set("sport", sp)
			}
			return s.get(r, "/v1/activities", q)
		},
	},
	{
		Name: "get_activity_detail",
		Description: "One activity in depth: metrics (avg/normalized/max power, kJ, HR, cadence, ascent), power curve against " +
			"the athlete's prior best, time in power and HR zones, per-source records, and for strength sessions per-set " +
			"heart rate, peak and recovery. Streams are omitted unless include_stream is true (then ~120 points).",
		InputSchema: obj(map[string]any{
			"id":             str("Activity ID from get_recent_activities"),
			"include_stream": map[string]any{"type": "boolean"},
		}, "id"),
		Annotations: readOnly,
		call: func(s *Server, r *http.Request, a map[string]any) (any, error) {
			id, _ := a["id"].(string)
			v, err := s.get(r, "/v1/activities/"+url.PathEscape(id), nil)
			if err != nil {
				return nil, err
			}
			m, ok := v.(map[string]any)
			if !ok {
				return v, nil
			}
			if inc, _ := a["include_stream"].(bool); !inc {
				delete(m, "stream")
			} else if st, ok := m["stream"].(map[string]any); ok {
				thin(st, 120)
			}
			if sum, ok := m["strength"].(map[string]any); ok {
				delete(sum, "hr") // the per-set stats carry the story
			}
			return m, nil
		},
	},
	{
		Name: "get_training_load",
		Description: "Daily training load: TSS (split ride / strength / other), fitness (CTL, 42-day), fatigue (ATL, 7-day), " +
			"form (TSB) and hours, plus the athlete's current FTP, max HR and where each came from.",
		InputSchema: obj(map[string]any{"days": num("Days of history (default 42, max 730)")}),
		Annotations: readOnly,
		call: func(s *Server, r *http.Request, a map[string]any) (any, error) {
			return s.get(r, "/v1/load", url.Values{"days": {strconv.Itoa(intArg(a, "days", 42, 1, 730))}})
		},
	},
	{
		Name:        "get_recovery",
		Description: "Whoop morning recovery (score, HRV, resting HR) and sleep (time asleep, sleep performance) by day.",
		InputSchema: obj(map[string]any{"days": num("Days of history (default 14, max 365)")}),
		Annotations: readOnly,
		call: func(s *Server, r *http.Request, a map[string]any) (any, error) {
			return s.get(r, "/v1/recovery", url.Values{"days": {strconv.Itoa(intArg(a, "days", 14, 1, 365))}})
		},
	},
	{
		Name: "get_trends",
		Description: "Long-term trends: power curve (all-time and last 90 days, with the ride that set each best), FTP by " +
			"month, weekly volume for 26 weeks, and lift PRs (estimated 1RM) with history.",
		InputSchema: obj(map[string]any{}),
		Annotations: readOnly,
		call: func(s *Server, r *http.Request, a map[string]any) (any, error) {
			return s.get(r, "/v1/trends", nil)
		},
	},
	{
		Name:        "list_templates",
		Description: "Strength templates the athlete can be planned onto: exercises, sets, reps, weights and rest.",
		InputSchema: obj(map[string]any{}),
		Annotations: readOnly,
		call: func(s *Server, r *http.Request, a map[string]any) (any, error) {
			return s.get(r, "/v1/templates", nil)
		},
	},
	{
		Name: "save_template",
		Description: "Create or replace a strength template. Weights are the starting point; the app adds load " +
			"automatically when every set hits its reps.",
		InputSchema: obj(map[string]any{
			"id":   str("Stable ID: letters, digits, dashes"),
			"name": str("Display name, e.g. 'Lower B'"),
			"exercises": map[string]any{"type": "array", "items": obj(map[string]any{
				"name":         str("Exercise name"),
				"rest_seconds": num("Rest after each set"),
				"notes":        str("Optional cue"),
				"sets": map[string]any{"type": "array", "items": obj(map[string]any{
					"reps": num("Target reps"), "weight_kg": num("Target weight; 0 for bodyweight"),
				}, "reps", "weight_kg")},
			}, "name", "sets", "rest_seconds")},
		}, "id", "name", "exercises"),
		call: func(s *Server, r *http.Request, a map[string]any) (any, error) {
			id, _ := a["id"].(string)
			return s.send(r, http.MethodPut, "/v1/templates/"+url.PathEscape(id), a)
		},
	},
	{
		Name:        "plan_strength_session",
		Description: "Plan a template for a date. It appears on the athlete's Today screen with prescribed weights.",
		InputSchema: obj(map[string]any{"date": str("YYYY-MM-DD"), "template_id": str("Template ID")}, "date", "template_id"),
		call: func(s *Server, r *http.Request, a map[string]any) (any, error) {
			d, _ := a["date"].(string)
			return s.send(r, http.MethodPut, "/v1/plans/"+url.PathEscape(d), map[string]any{"template_id": a["template_id"]})
		},
	},
}

func intArg(a map[string]any, k string, def, lo, hi int) int {
	v, ok := a[k].(float64)
	if !ok {
		return def
	}
	return max(lo, min(hi, int(v)))
}

// thin keeps every nth sample of each array in a stream map.
func thin(st map[string]any, n int) {
	for k, v := range st {
		arr, ok := v.([]any)
		if !ok || len(arr) <= n {
			continue
		}
		step := len(arr) / n
		out := make([]any, 0, n+1)
		for i := 0; i < len(arr); i += step {
			out = append(out, arr[i])
		}
		st[k] = out
	}
}

// ---- Calling through to REST -------------------------------------------------------

func (s *Server) do(r *http.Request, method, path string, q url.Values, body any) (any, error) {
	u := path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return nil, err
		}
	}
	req := httptest.NewRequestWithContext(r.Context(), method, u, &buf)
	// Carry the caller's identity: the gateway already authenticated this
	// request, and the inner handler must see the same user.
	for _, h := range []string{"X-Outpost-User", "X-Outpost-Identity", "X-Forwarded-Host", "X-Forwarded-Proto"} {
		if v := r.Header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}
	req.Host = r.Host
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.API.ServeHTTP(rec, req)
	var v any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			return nil, fmt.Errorf("unexpected response from %s", path)
		}
	}
	if rec.Code >= 400 {
		if m, ok := v.(map[string]any); ok {
			if e, ok := m["error"].(string); ok {
				return nil, fmt.Errorf("%s", e)
			}
		}
		return nil, fmt.Errorf("%s failed: %d", path, rec.Code)
	}
	return v, nil
}

func (s *Server) get(r *http.Request, path string, q url.Values) (any, error) {
	return s.do(r, http.MethodGet, path, q, nil)
}

func (s *Server) send(r *http.Request, method, path string, body any) (any, error) {
	return s.do(r, method, path, nil, body)
}

// ---- Transport ------------------------------------------------------------------------

var supported = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
	case http.MethodDelete:
		w.WriteHeader(http.StatusOK) // sessions are stateless; nothing to end
		return
	default:
		// No server-initiated stream: GET is not offered (spec permits 405).
		w.Header().Set("Allow", "POST, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	var raw json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeRPC(w, rpcResp{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcErr{-32700, "parse error"}})
		return
	}
	// Batches (2025-03-26) are accepted for older clients.
	if len(raw) > 0 && raw[0] == '[' {
		var reqs []rpcReq
		if err := json.Unmarshal(raw, &reqs); err != nil {
			writeRPC(w, rpcResp{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcErr{-32600, "invalid request"}})
			return
		}
		var out []rpcResp
		for _, q := range reqs {
			if resp, ok := s.handle(w, r, q); ok {
				out = append(out, resp)
			}
		}
		if len(out) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		writeRPC(w, out)
		return
	}
	var q rpcReq
	if err := json.Unmarshal(raw, &q); err != nil {
		writeRPC(w, rpcResp{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcErr{-32600, "invalid request"}})
		return
	}
	resp, ok := s.handle(w, r, q)
	if !ok {
		w.WriteHeader(http.StatusAccepted) // a notification
		return
	}
	writeRPC(w, resp)
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request, q rpcReq) (rpcResp, bool) {
	if len(q.ID) == 0 {
		return rpcResp{}, false // notifications (initialized, cancelled) need no reply
	}
	resp := rpcResp{JSONRPC: "2.0", ID: q.ID}
	switch q.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(q.Params, &p)
		version := supported[0]
		for _, v := range supported {
			if v == p.ProtocolVersion {
				version = v
			}
		}
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		w.Header().Set("Mcp-Session-Id", hex.EncodeToString(b))
		resp.Result = map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "domestique", "title": "Domestique", "version": s.Version},
			"instructions": "Domestique holds one athlete's training: rides (Wahoo, Zwift, Strava history), strength " +
				"sessions logged live in the app, and Whoop recovery and sleep. Start with get_today; use " +
				"get_training_load for fitness and fatigue, and get_activity_detail to analyse a session. " +
				"TSS on rides is power-based against the FTP in force that day; strength load comes from Whoop " +
				"strain or heart rate.",
		}
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		resp.Result = map[string]any{"tools": tools}
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(q.Params, &p); err != nil {
			resp.Error = &rpcErr{-32602, "invalid params"}
			break
		}
		var t *tool
		for i := range tools {
			if tools[i].Name == p.Name {
				t = &tools[i]
			}
		}
		if t == nil {
			resp.Error = &rpcErr{-32602, "unknown tool " + p.Name}
			break
		}
		if p.Arguments == nil {
			p.Arguments = map[string]any{}
		}
		v, err := t.call(s, r, p.Arguments)
		if err != nil {
			resp.Result = map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": err.Error()}}}
			break
		}
		b, _ := json.Marshal(v)
		resp.Result = map[string]any{
			"content":           []any{map[string]any{"type": "text", "text": string(b)}},
			"structuredContent": wrap(v),
		}
	default:
		resp.Error = &rpcErr{-32601, "method not found: " + q.Method}
	}
	return resp, true
}

// structuredContent must be an object.
func wrap(v any) any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{"items": v}
}

func writeRPC(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
