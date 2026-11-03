package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
