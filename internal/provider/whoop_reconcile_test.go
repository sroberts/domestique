package provider

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"
)

func TestWhoopReconcile(t *testing.T) {
	w := &Whoop{C: &Client{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	ctx := context.Background()
	since := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	var asked int64
	stored := func(ids ...string) func(context.Context, string, int64) ([]string, error) {
		return func(_ context.Context, prefix string, from int64) ([]string, error) {
			if prefix != "whoop:" {
				t.Fatalf("prefix %q: only records from Whoop's API may be reconciled", prefix)
			}
			asked = from
			return ids, nil
		}
	}
	run := func(seen map[string]bool, have ...string) []string {
		var removed []string
		if err := w.reconcile(ctx, since, seen, stored(have...), func(id string) error {
			removed = append(removed, id)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return removed
	}

	// Whoop no longer returns b: it was deleted there.
	if got := run(map[string]bool{"whoop:a": true, "whoop:c": true}, "whoop:a", "whoop:b", "whoop:c"); !slices.Equal(got, []string{"whoop:b"}) {
		t.Fatalf("removed %v, want whoop:b", got)
	}
	if asked != since.Add(time.Minute).UnixMilli() {
		t.Fatalf("looked from %d, want a minute inside the window", asked)
	}
	// One record, and Whoop now has none: that one was deleted.
	if got := run(map[string]bool{}, "whoop:x"); !slices.Equal(got, []string{"whoop:x"}) {
		t.Fatalf("removed %v, want whoop:x", got)
	}
	// Nothing back against several stored: an outage, not a mass deletion.
	if got := run(map[string]bool{}, "whoop:x", "whoop:y"); len(got) != 0 {
		t.Fatalf("removed %v on an empty answer", got)
	}
}
