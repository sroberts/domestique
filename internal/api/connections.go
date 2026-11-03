package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"domestique/internal/importer"
	"domestique/internal/ingest"
	"domestique/internal/provider"
)

func (a *API) registerConnections(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/connections", a.connections)
	mux.HandleFunc("GET /v1/oauth/{provider}/start", a.oauthStart)
	mux.HandleFunc("GET /v1/oauth/{provider}/callback", a.oauthCallback)
	mux.HandleFunc("DELETE /v1/oauth/{provider}", a.oauthDisconnect)
	mux.HandleFunc("POST /v1/connections/{provider}/sync", a.syncNow)
	mux.HandleFunc("POST /v1/imports", a.upload)
	mux.HandleFunc("GET /v1/imports", a.uploadStatus)
}

func (a *API) connections(w http.ResponseWriter, r *http.Request) {
	out := []provider.Status{}
	for _, name := range []string{"whoop", "wahoo"} {
		if c := a.Providers[name]; c != nil {
			out = append(out, c.Status(r.Context()))
		} else {
			out = append(out, provider.Status{Provider: name})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": out, "redirect_base": a.publicBase(r)})
}

// publicBase is the address the browser reached us at. Behind Outpost that's
// the Tailscale node; the gateway preserves Host and sets X-Forwarded-*.
func (a *API) publicBase(r *http.Request) string {
	if v := os.Getenv("DOMESTIQUE_PUBLIC_URL"); v != "" {
		return strings.TrimSuffix(v, "/")
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	proto := r.Header.Get("X-Forwarded-Proto")
	if proto == "" {
		proto = "http"
	}
	return proto + "://" + strings.Split(host, ",")[0]
}

func (a *API) provider(w http.ResponseWriter, r *http.Request) *provider.Client {
	c := a.Providers[r.PathValue("provider")]
	if c == nil {
		writeErr(w, http.StatusNotFound, "unknown provider")
		return nil
	}
	if !c.Cfg.Configured() {
		writeErr(w, http.StatusConflict, fmt.Sprintf("%s isn't configured: set %s_CLIENT_ID and %s_CLIENT_SECRET_FILE",
			c.Cfg.Name, envPrefix(c.Cfg.Name), envPrefix(c.Cfg.Name)))
		return nil
	}
	return c
}

func envPrefix(name string) string { return "DOMESTIQUE_" + strings.ToUpper(name) }

func (a *API) oauthStart(w http.ResponseWriter, r *http.Request) {
	// A GET that replaces the pending sign-in state: only our own page (or a
	// typed address) may start it, not another site's link or <img>.
	switch r.Header.Get("Sec-Fetch-Site") {
	case "cross-site", "same-site":
		writeErr(w, http.StatusForbidden, "start the connection from Settings")
		return
	}
	c := a.provider(w, r)
	if c == nil {
		return
	}
	redirect := a.publicBase(r) + "/v1/oauth/" + c.Cfg.Name + "/callback"
	u, err := c.AuthorizeURL(r.Context(), redirect)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, u, http.StatusFound)
}

func (a *API) oauthCallback(w http.ResponseWriter, r *http.Request) {
	c := a.provider(w, r)
	if c == nil {
		return
	}
	q := r.URL.Query()
	back := "/settings?provider=" + c.Cfg.Name
	if e := q.Get("error"); e != "" {
		http.Redirect(w, r, back+"&error="+url.QueryEscape(firstNonEmpty(q.Get("error_description"), e)), http.StatusFound)
		return
	}
	if err := c.Exchange(r.Context(), q.Get("code"), q.Get("state")); err != nil {
		a.Log.Warn("oauth exchange", "provider", c.Cfg.Name, "err", err.Error())
		http.Redirect(w, r, back+"&error="+url.QueryEscape(err.Error()), http.StatusFound)
		return
	}
	c.SyncNow()
	http.Redirect(w, r, back+"&connected=1", http.StatusFound)
}

func (a *API) oauthDisconnect(w http.ResponseWriter, r *http.Request) {
	c := a.Providers[r.PathValue("provider")]
	if c == nil {
		writeErr(w, http.StatusNotFound, "unknown provider")
		return
	}
	if err := a.Store.DeleteToken(r.Context(), c.Cfg.Name); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) syncNow(w http.ResponseWriter, r *http.Request) {
	c := a.provider(w, r)
	if c == nil {
		return
	}
	c.SyncNow()
	writeJSON(w, http.StatusAccepted, c.Status(r.Context()))
}

func ingestHint() ingest.Hint { return ingest.Hint{Source: "upload"} }

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// ---- Uploads: FIT/GPX/TCX files or a whole Strava export ------------------------------

// Uploads tracks background imports so the browser can poll progress.
type Uploads struct {
	mu   sync.Mutex
	jobs []*UploadJob
}

type UploadJob struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	State    string            `json:"state"` // running | done | failed
	Progress importer.Progress `json:"progress"`
	Error    string            `json:"error,omitempty"`
	Started  int64             `json:"started"`
}

func (u *Uploads) add(j *UploadJob) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.jobs = append(u.jobs, j)
	if len(u.jobs) > 20 {
		u.jobs = u.jobs[len(u.jobs)-20:]
	}
}

func (u *Uploads) update(j *UploadJob, f func(*UploadJob)) {
	u.mu.Lock()
	defer u.mu.Unlock()
	f(j)
}

func (u *Uploads) list() []UploadJob {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := make([]UploadJob, len(u.jobs))
	for i, j := range u.jobs {
		out[len(u.jobs)-1-i] = *j
	}
	return out
}

func (a *API) uploadStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"jobs": a.Uploads.list()})
}

// upload accepts multipart "file" fields. A .zip is treated as a Strava
// export and imported in the background; activity files import immediately.
func (a *API) upload(w http.ResponseWriter, r *http.Request) {
	mr, err := r.MultipartReader()
	if err != nil {
		writeErr(w, http.StatusBadRequest, "send files as multipart/form-data")
		return
	}
	tmpDir := filepath.Join(a.Ingest.StorageDir, "tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		a.fail(w, r, err)
		return
	}
	results := []map[string]any{}
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeErr(w, http.StatusBadRequest, "upload interrupted: "+err.Error())
			return
		}
		name := filepath.Base(part.FileName())
		if name == "" || name == "." {
			continue
		}
		if strings.HasSuffix(strings.ToLower(name), ".zip") {
			// Spool to disk: exports run to gigabytes.
			f, err := os.CreateTemp(tmpDir, "upload-*.zip")
			if err != nil {
				a.fail(w, r, err)
				return
			}
			if _, err := io.Copy(f, part); err != nil {
				f.Close()
				os.Remove(f.Name())
				writeErr(w, http.StatusBadRequest, "upload interrupted: "+err.Error())
				return
			}
			f.Close()
			job := &UploadJob{ID: filepath.Base(f.Name()), Name: name, State: "running", Started: time.Now().UnixMilli()}
			a.Uploads.add(job)
			go a.runExport(job, f.Name())
			results = append(results, map[string]any{"file": name, "job": job.ID})
			continue
		}
		data, err := ingest.ReadLimited(part)
		if errors.Is(err, ingest.ErrTooLarge) {
			results = append(results, map[string]any{"file": name, "error": err.Error()})
			continue
		}
		if err != nil {
			writeErr(w, http.StatusBadRequest, "upload interrupted: "+err.Error())
			return
		}
		res, err := a.Ingest.Ingest(r.Context(), name, data, "upload", ingestHint())
		if err != nil {
			results = append(results, map[string]any{"file": name, "error": err.Error()})
			continue
		}
		results = append(results, map[string]any{"file": name, "result": res})
	}
	a.kickRecalc()
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (a *API) runExport(job *UploadJob, path string) {
	// Detached from the request: an export can take minutes. A shutdown
	// stops it between activities; re-uploading resumes, since done files dedup.
	ctx := a.background
	if ctx == nil {
		ctx = context.Background()
	}
	defer os.Remove(path)
	p, err := importer.StravaExport(ctx, a.Ingest, path, func(p importer.Progress) {
		a.Uploads.update(job, func(j *UploadJob) { j.Progress = p })
	})
	a.Uploads.update(job, func(j *UploadJob) {
		j.Progress = p
		if err != nil {
			j.State, j.Error = "failed", err.Error()
		} else {
			j.State = "done"
		}
	})
	a.kickRecalc()
}
