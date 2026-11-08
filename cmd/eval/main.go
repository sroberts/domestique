// Command eval scores the operator's models on Domestique's questions
// (internal/coach/eval). Against Outpost, it reads OUTPOST_AI_URL and
// OUTPOST_AI_TOKEN like the service does. Against Ollama on this machine,
// with no Outpost:
//
//	go run ./cmd/eval -ollama http://127.0.0.1:11434 -system1 laya -llm gemma4
//
// It exits 1 when any answer would be acted on wrongly at today's bars.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"strings"

	"domestique/internal/coach"
	"domestique/internal/coach/eval"
)

func main() {
	ollama := flag.String("ollama", "", "call this Ollama directly instead of Outpost")
	system1 := flag.String("system1", "", "with -ollama: the System 1 model")
	llm := flag.String("llm", "", "with -ollama: the LLM")
	only := flag.String("q", "", "comma-separated questions to run (default all): "+strings.Join(eval.Names(), ", "))
	verbose := flag.Bool("v", false, "show correct items too")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if *ollama != "" {
		srv := httptest.NewServer(standIn(strings.TrimRight(*ollama, "/"), *system1, *llm))
		defer srv.Close()
		os.Setenv("OUTPOST_AI_URL", srv.URL)
		os.Setenv("OUTPOST_AI_TOKEN", "eval")
	}
	ai := coach.FromEnv()
	if ai == nil {
		fmt.Fprintln(os.Stderr, "eval: set OUTPOST_AI_URL and OUTPOST_AI_TOKEN, or pass -ollama")
		os.Exit(2)
	}
	names := eval.Names()
	if *only != "" {
		names = strings.Split(*only, ",")
	}
	wrong := 0
	for _, n := range names {
		sc, err := eval.Run(ctx, ai, strings.TrimSpace(n))
		if err != nil {
			fmt.Fprintln(os.Stderr, "eval:", err)
			os.Exit(2)
		}
		eval.Print(os.Stdout, sc, *verbose)
		wrong += sc.Counts[eval.Wrong]
	}
	if wrong > 0 {
		fmt.Printf("\n%d answers would be acted on wrongly at today's bars.\n", wrong)
		os.Exit(1)
	}
}

// standIn answers as Outpost would (service spec §15), passing each
// request to Ollama with the chosen model, or 503 when none was chosen.
func standIn(base, system1, llm string) http.Handler {
	forward := func(path, model string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if model == "" {
				http.Error(w, `{"type":"error","error":{"type":"api_error","message":"no model chosen; pass -system1 or -llm"}}`, http.StatusServiceUnavailable)
				return
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "bad body", http.StatusBadRequest)
				return
			}
			body["model"] = model
			b, _ := json.Marshal(body)
			req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, base+path, bytes.NewReader(b))
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				http.Error(w, `{"type":"error","error":{"type":"api_error","message":"ollama unreachable"}}`, http.StatusBadGateway)
				return
			}
			defer resp.Body.Close()
			w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
			w.WriteHeader(resp.StatusCode)
			_, _ = io.Copy(w, resp.Body)
		}
	}
	mux := http.NewServeMux()
	mux.Handle("POST /systemone/v1/systemone", forward("/v1/systemone", system1))
	mux.Handle("POST /openai/v1/chat/completions", forward("/v1/chat/completions", llm))
	return mux
}
