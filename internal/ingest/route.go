package ingest

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
)

// zwiftRoute matches the line Zwift writes into an activity's description:
// "🗺️ Tempus Fugit in Watopia".
var zwiftRoute = regexp.MustCompile(`(?m)^\s*\x{1F5FA}\x{FE0F}?\s*(.+?)\s*$`)

// RouteFromDescription is the route Zwift named in a description, as
// "<route> in <world>", or "" if there's none. A line with no route name
// ("🗺️ in Watopia") names no route.
func RouteFromDescription(desc string) string {
	m := zwiftRoute.FindStringSubmatch(desc)
	if m == nil || strings.HasPrefix(m[1], "in ") {
		return ""
	}
	return m[1]
}

// routeOf is a source's route: one the origin gave, else one named in its
// description.
func routeOf(extra json.RawMessage) string {
	var ex struct {
		Route       string `json:"route"`
		Description string `json:"description"`
	}
	_ = json.Unmarshal(extra, &ex)
	if ex.Route != "" {
		return ex.Route
	}
	return RouteFromDescription(ex.Description)
}

// RouteRule names the current way routes are found. BackfillRoutes runs once
// per rule.
const RouteRule = "zwift-description-v1"

// BackfillRoutes rebuilds activities whose route is only in a description,
// imported before routes were read from there. It returns how many changed.
func (in *Ingester) BackfillRoutes(ctx context.Context) (int, error) {
	srcs, err := in.Store.AllSources(ctx)
	if err != nil {
		return 0, err
	}
	done := map[string]bool{}
	n := 0
	for _, s := range srcs {
		if done[s.ActivityID] || routeOf(s.Extra) == "" {
			continue
		}
		done[s.ActivityID] = true
		a, err := in.Store.GetActivity(ctx, s.ActivityID)
		if err != nil || a.Route != "" {
			continue
		}
		in.mu.Lock()
		err = in.Rebuild(ctx, s.ActivityID)
		in.mu.Unlock()
		if err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
