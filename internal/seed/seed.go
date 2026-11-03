// Package seed installs the starter strength templates on an empty database.
package seed

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"

	"domestique/internal/store"
)

//go:embed templates.json
var templatesJSON []byte

// Templates inserts the starter templates when no templates exist yet. Plans
// and templates are otherwise written in the app, over REST (PUT /v1/templates/{id})
// or by Claude over MCP.
func Templates(ctx context.Context, s *store.Store) error {
	n, err := s.CountTemplates(ctx)
	if err != nil || n > 0 {
		return err
	}
	var ts []store.Template
	if err := json.Unmarshal(templatesJSON, &ts); err != nil {
		return fmt.Errorf("seed templates: %w", err)
	}
	for _, t := range ts {
		t, err := store.CleanTemplate(t)
		if err != nil {
			return fmt.Errorf("seed template %s: %w", t.ID, err)
		}
		if err := s.PutTemplate(ctx, t); err != nil {
			return err
		}
	}
	return nil
}
