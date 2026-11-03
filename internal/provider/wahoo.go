package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"domestique/internal/ingest"
	"domestique/internal/store"
)

// WahooConfig is Wahoo's OAuth application shape. Access tokens last two
// hours; refresh tokens are effectively single-use.
var WahooConfig = Config{
	Name:     "wahoo",
	AuthURL:  "https://api.wahooligan.com/oauth/authorize",
	TokenURL: "https://api.wahooligan.com/oauth/token",
	Scopes:   []string{"user_read", "workouts_read", "offline_data"},
}

const wahooAPI = "https://api.wahooligan.com/v1"

// Wahoo downloads the original FIT file of every workout.
type Wahoo struct {
	C  *Client
	In *ingest.Ingester
}

type wahooWorkout struct {
	ID     int64     `json:"id"`
	Starts time.Time `json:"starts"`
	Name   string    `json:"name"`
}

// Sync walks the workout list newest first and stops at the first page that
// holds nothing new, so steady-state polling is one request.
func (w *Wahoo) Sync(ctx context.Context) error {
	st := w.C.Store
	if _, err := st.GetToken(ctx, "wahoo"); errors.Is(err, store.ErrNotFound) {
		return ErrNotConnected
	}
	for page := 1; page <= 200; page++ {
		var list struct {
			Workouts []wahooWorkout `json:"workouts"`
			Total    int            `json:"total"`
		}
		if err := w.C.GetJSON(ctx, fmt.Sprintf("%s/workouts?page=%d&per_page=30", wahooAPI, page), &list); err != nil {
			return err
		}
		fresh := 0
		for _, wo := range list.Workouts {
			id := strconv.FormatInt(wo.ID, 10)
			if _, err := st.GetSource(ctx, "wahoo:"+id); err == nil {
				continue
			}
			if v, err := st.SyncState(ctx, "wahoo:skip:"+id); err == nil && v != "" {
				continue
			}
			fresh++
			if err := w.importOne(ctx, wo); err != nil {
				if errors.Is(err, errRateLimited) {
					return err
				}
				w.C.Log.Warn("wahoo workout", "id", id, "err", err.Error())
			}
		}
		if fresh == 0 || len(list.Workouts) < 30 {
			return nil
		}
	}
	return nil
}

func (w *Wahoo) importOne(ctx context.Context, wo wahooWorkout) error {
	id := strconv.FormatInt(wo.ID, 10)
	var sum struct {
		File *struct {
			URL string `json:"url"`
		} `json:"file"`
	}
	if err := w.C.GetJSON(ctx, wahooAPI+"/workouts/"+id+"/workout_summary", &sum); err != nil {
		return err
	}
	if sum.File == nil || sum.File.URL == "" {
		// Still processing on Wahoo's side; try again on a later poll unless it's old.
		if time.Since(wo.Starts) > 72*time.Hour {
			_ = w.C.Store.SetSyncState(ctx, "wahoo:skip:"+id, "no file")
		}
		return nil
	}
	// The CDN download needs no token and doesn't count against rate limits.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sum.File.URL, nil)
	if err != nil {
		return err
	}
	resp, err := w.C.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download fit: %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 128<<20))
	if err != nil {
		return err
	}
	_, err = w.In.Ingest(ctx, "wahoo-"+id+".fit", data, "wahoo-api",
		ingest.Hint{Source: "wahoo", ExternalID: id, Name: wo.Name})
	return err
}
