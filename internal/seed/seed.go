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
// are otherwise written over the API (POST /v1/templates), not in the app.
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
		if err := s.PutTemplate(ctx, t); err != nil {
			return err
		}
	}
	return nil
}
