package mcp

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"domestique/internal/api"
	"domestique/internal/store"
)

func TestBatchIsBounded(t *testing.T) {
	calls := 0
	s := &Server{API: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})}
	one := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_trends"}}`
	post := func(n int) *httptest.ResponseRecorder {
		body := "[" + strings.TrimSuffix(strings.Repeat(one+",", n), ",") + "]"
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("POST", "/mcp", strings.NewReader(body)))
		return w
	}
	if w := post(maxBatch); calls != maxBatch || !strings.Contains(w.Body.String(), `"result"`) {
		t.Fatalf("batch of %d: %d calls, %s", maxBatch, calls, w.Body.String())
	}
	calls = 0
	if w := post(maxBatch + 1); calls != 0 || !strings.Contains(w.Body.String(), "batch larger") {
		t.Fatalf("oversized batch ran %d calls: %s", calls, w.Body.String())
	}
}

// save_template, list_templates and delete_template run the REST handlers,
// so Claude gets the same validation and the same today's-plan guard.
func TestTemplateTools(t *testing.T) {
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &api.API{Store: st, Log: slog.New(slog.DiscardHandler), Loc: time.Local}
	mux := http.NewServeMux()
	a.Register(mux)
	s := &Server{API: mux}
	callTool := func(name, args string) string {
		body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}`
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("POST", "/mcp", strings.NewReader(body)))
		return w.Body.String()
	}
	bad := `{"id":"push","name":"Push","exercises":[{"name":"Bench","rest_seconds":120,"sets":[{"reps":5,"weight_kg":80.3}]}]}`
	if out := callTool("save_template", bad); !strings.Contains(out, `"isError":true`) || !strings.Contains(out, "steps of 0.25") {
		t.Fatalf("bad weight accepted: %s", out)
	}
	good := strings.Replace(bad, "80.3", "80.25", 1)
	if out := callTool("save_template", good); strings.Contains(out, "isError") {
		t.Fatalf("save: %s", out)
	}
	if out := callTool("list_templates", `{}`); !strings.Contains(out, `\"id\":\"push\"`) {
		t.Fatalf("list: %s", out)
	}
	if err := st.PutPlan(context.Background(), time.Now().Format("2006-01-02"), "push"); err != nil {
		t.Fatal(err)
	}
	if out := callTool("delete_template", `{"id":"push"}`); !strings.Contains(out, "today's plan") {
		t.Fatalf("deleted today's plan without asking: %s", out)
	}
	if out := callTool("delete_template", `{"id":"push","clear_plans":true}`); !strings.Contains(out, `"deleted":"push"`) {
		t.Fatalf("delete: %s", out)
	}
	if n, _ := st.CountTemplates(context.Background()); n != 0 {
		t.Fatalf("%d templates left", n)
	}
}
