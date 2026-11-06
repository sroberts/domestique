package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"domestique/internal/activity"
	"domestique/internal/gear"
	"domestique/internal/store"
)

// Gear: bikes, the parts fitted to them, and sensors with batteries. IDs are
// chosen by the client (PUT creates or replaces), like sessions and sets.
func (a *API) registerGear(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/gear", a.getGear)
	mux.HandleFunc("PUT /v1/gear/bikes/{id}", a.putBike)
	mux.HandleFunc("DELETE /v1/gear/bikes/{id}", a.deleteBike)
	mux.HandleFunc("PUT /v1/gear/components/{id}", a.putComponent)
	mux.HandleFunc("DELETE /v1/gear/components/{id}", a.deleteComponent)
	mux.HandleFunc("PUT /v1/gear/sensors/{id}", a.putSensor)
	mux.HandleFunc("DELETE /v1/gear/sensors/{id}", a.deleteSensor)
	mux.HandleFunc("GET /v1/activities/{id}/bike", a.getActivityBike)
	mux.HandleFunc("PUT /v1/activities/{id}/bike", a.putActivityBike)
	mux.HandleFunc("DELETE /v1/activities/{id}/bike", a.deleteActivityBike)
}

const maxDistanceM = 1e9 // a million km

func (a *API) gearView(ctx context.Context) (gear.View, error) {
	var in gear.Input
	var err error
	if in.Bikes, err = a.Store.ListBikes(ctx); err != nil {
		return gear.View{}, err
	}
	if in.Components, err = a.Store.ListComponents(ctx); err != nil {
		return gear.View{}, err
	}
	if in.Sensors, err = a.Store.ListSensors(ctx); err != nil {
		return gear.View{}, err
	}
	if len(in.Bikes) > 0 {
		if in.Rides, err = a.Store.GearRides(ctx); err != nil {
			return gear.View{}, err
		}
		if in.Overrides, err = a.Store.BikeOverrides(ctx); err != nil {
			return gear.View{}, err
		}
	}
	if len(in.Sensors) > 0 {
		from := in.Sensors[0].BatteryAt
		for _, s := range in.Sensors {
			from = min(from, s.BatteryAt)
		}
		if in.HR, err = a.Store.HRUses(ctx, from); err != nil {
			return gear.View{}, err
		}
	}
	return gear.Compute(in), nil
}

func (a *API) getGear(w http.ResponseWriter, r *http.Request) {
	v, err := a.gearView(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	// How many rides there are at all: the empty state promises distance
	// fills in from them.
	resp := struct {
		gear.View
		Rides int `json:"rides"`
	}{View: v}
	if rides, err := a.Store.GearRides(r.Context()); err == nil {
		resp.Rides = len(rides)
	}
	writeJSON(w, http.StatusOK, resp)
}

func validName(s string, max int) bool {
	s = strings.TrimSpace(s)
	return s != "" && len(s) <= max
}

func (a *API) putBike(w http.ResponseWriter, r *http.Request) {
	var b store.Bike
	if !readJSON(w, r, &b) {
		return
	}
	b.ID = r.PathValue("id")
	b.Name = strings.TrimSpace(b.Name)
	var aliases []string
	for _, al := range b.Aliases {
		if al = strings.TrimSpace(al); al != "" && !slices.Contains(aliases, al) {
			aliases = append(aliases, al)
		}
	}
	b.Aliases = aliases
	switch {
	case !idRe.MatchString(b.ID):
		writeErr(w, http.StatusBadRequest, "bad bike id")
		return
	case !validName(b.Name, 60):
		writeErr(w, http.StatusBadRequest, "a bike needs a name (max 60 characters)")
		return
	case len(b.Aliases) > 10:
		writeErr(w, http.StatusBadRequest, "at most 10 other names")
		return
	case b.StartDistanceM < 0 || b.StartDistanceM > maxDistanceM:
		writeErr(w, http.StatusBadRequest, "start_distance_m is out of range")
		return
	}
	for _, al := range b.Aliases {
		if len(al) > 80 {
			writeErr(w, http.StatusBadRequest, "other names are at most 80 characters")
			return
		}
	}
	if prev, err := a.Store.GetBike(r.Context(), b.ID); err == nil {
		b.CreatedAt = prev.CreatedAt
	} else if !errors.Is(err, store.ErrNotFound) {
		a.fail(w, r, err)
		return
	}
	if err := a.Store.PutBike(r.Context(), b); err != nil {
		a.fail(w, r, err)
		return
	}
	b, _ = a.Store.GetBike(r.Context(), b.ID)
	writeJSON(w, http.StatusOK, b)
}

func (a *API) deleteBike(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !idRe.MatchString(id) {
		writeErr(w, http.StatusBadRequest, "bad bike id")
		return
	}
	if err := a.Store.DeleteBike(r.Context(), id); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// latestAllowed: dates may be today in any time zone, not the future.
func latestAllowed() int64 { return time.Now().Add(26 * time.Hour).UnixMilli() }

func (a *API) putComponent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		store.Component
		// Replaces names the component this one takes over from; that one is
		// retired as of this one's installed_at.
		Replaces string `json:"replaces"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	c := body.Component
	c.ID = r.PathValue("id")
	c.Name = strings.TrimSpace(c.Name)
	switch {
	case !idRe.MatchString(c.ID) || !idRe.MatchString(c.BikeID):
		writeErr(w, http.StatusBadRequest, "bad component or bike id")
		return
	case !slices.Contains(gear.Kinds, c.Kind):
		writeErr(w, http.StatusBadRequest, "kind must be one of "+strings.Join(gear.Kinds, ", "))
		return
	case len(c.Name) > 60:
		writeErr(w, http.StatusBadRequest, "name is at most 60 characters")
		return
	case c.InstalledAt <= 0 || c.InstalledAt > latestAllowed():
		writeErr(w, http.StatusBadRequest, "installed_at must be a past time in epoch ms")
		return
	case c.RemovedAt != nil && *c.RemovedAt < c.InstalledAt:
		writeErr(w, http.StatusBadRequest, "removed_at is before installed_at")
		return
	case c.ReplaceAtM < 0 || c.ReplaceAtM > maxDistanceM || c.StartDistanceM < 0 || c.StartDistanceM > maxDistanceM:
		writeErr(w, http.StatusBadRequest, "distances are out of range")
		return
	case body.Replaces != "" && (!idRe.MatchString(body.Replaces) || body.Replaces == c.ID):
		writeErr(w, http.StatusBadRequest, "bad replaces id")
		return
	}
	ctx := r.Context()
	if _, err := a.Store.GetBike(ctx, c.BikeID); errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusBadRequest, "no such bike")
		return
	} else if err != nil {
		a.fail(w, r, err)
		return
	}
	if prev, err := a.Store.GetComponent(ctx, c.ID); err == nil {
		c.CreatedAt = prev.CreatedAt
		if c.ReplacedBy == nil {
			c.ReplacedBy = prev.ReplacedBy
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		a.fail(w, r, err)
		return
	}
	if body.Replaces != "" {
		old, err := a.Store.GetComponent(ctx, body.Replaces)
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusBadRequest, "the component being replaced doesn't exist")
			return
		} else if err != nil {
			a.fail(w, r, err)
			return
		}
		if c.InstalledAt < old.InstalledAt {
			writeErr(w, http.StatusBadRequest, "the replacement is dated before the part it replaces was fitted")
			return
		}
	}
	if err := a.Store.PutComponent(ctx, c, body.Replaces); err != nil {
		a.fail(w, r, err)
		return
	}
	c, _ = a.Store.GetComponent(ctx, c.ID)
	writeJSON(w, http.StatusOK, c)
}

func (a *API) deleteComponent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !idRe.MatchString(id) {
		writeErr(w, http.StatusBadRequest, "bad component id")
		return
	}
	if err := a.Store.DeleteComponent(r.Context(), id); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) putSensor(w http.ResponseWriter, r *http.Request) {
	var x store.Sensor
	if !readJSON(w, r, &x) {
		return
	}
	x.ID = r.PathValue("id")
	x.Name = strings.TrimSpace(x.Name)
	x.DeviceMatch = strings.TrimSpace(x.DeviceMatch)
	if x.Kind == "" {
		x.Kind = "hr_strap"
	}
	switch {
	case !idRe.MatchString(x.ID):
		writeErr(w, http.StatusBadRequest, "bad sensor id")
		return
	case !validName(x.Name, 60):
		writeErr(w, http.StatusBadRequest, "a sensor needs a name (max 60 characters)")
		return
	case x.Kind != "hr_strap" && x.Kind != "power_meter" && x.Kind != "other":
		writeErr(w, http.StatusBadRequest, "kind must be hr_strap, power_meter or other")
		return
	case len(x.DeviceMatch) > 60:
		writeErr(w, http.StatusBadRequest, "device_match is at most 60 characters")
		return
	case x.BatteryAt <= 0 || x.BatteryAt > latestAllowed():
		writeErr(w, http.StatusBadRequest, "battery_at must be a past time in epoch ms")
		return
	case x.BatteryLifeH < 1 || x.BatteryLifeH > 100000:
		writeErr(w, http.StatusBadRequest, "battery_life_h must be 1-100000")
		return
	}
	if prev, err := a.Store.GetSensor(r.Context(), x.ID); err == nil {
		x.CreatedAt = prev.CreatedAt
	} else if !errors.Is(err, store.ErrNotFound) {
		a.fail(w, r, err)
		return
	}
	if err := a.Store.PutSensor(r.Context(), x); err != nil {
		a.fail(w, r, err)
		return
	}
	x, _ = a.Store.GetSensor(r.Context(), x.ID)
	writeJSON(w, http.StatusOK, x)
}

func (a *API) deleteSensor(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !idRe.MatchString(id) {
		writeErr(w, http.StatusBadRequest, "bad sensor id")
		return
	}
	if err := a.Store.DeleteSensor(r.Context(), id); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- Which bike a ride was on ------------------------------------------------------

type bikeChoice struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Retired bool   `json:"retired"`
}

type activityBikeResp struct {
	ActivityID string       `json:"activity_id"`
	Bike       *bikeChoice  `json:"bike"`
	How        string       `json:"how"` // manual | strava | default | none
	StravaGear string       `json:"strava_gear,omitempty"`
	Bikes      []bikeChoice `json:"bikes"`
}

func (a *API) getActivityBike(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	act, err := a.Store.GetActivity(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "no such activity")
		return
	} else if err != nil {
		a.fail(w, r, err)
		return
	}
	if !activity.IsRide(act.Sport) {
		writeErr(w, http.StatusNotFound, "only rides have a bike")
		return
	}
	bikes, err := a.Store.ListBikes(ctx)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	over, err := a.Store.BikeOverrides(ctx)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	ride := store.GearActivity{ID: act.ID, Sport: act.Sport, StartAt: act.StartAt}
	if srcs, err := a.Store.SourcesFor(ctx, act.ID); err == nil {
		for _, s := range srcs {
			if g := extraString(s.Extra, "gear"); g != "" {
				ride.StravaGear = g
				break
			}
		}
	}
	as := gear.Assign(ride, bikes, over)
	resp := activityBikeResp{ActivityID: act.ID, How: as.How, StravaGear: ride.StravaGear, Bikes: []bikeChoice{}}
	for _, b := range bikes {
		c := bikeChoice{b.ID, b.Name, b.Retired}
		resp.Bikes = append(resp.Bikes, c)
		if b.ID == as.BikeID {
			resp.Bike = &c
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *API) putActivityBike(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	var body struct {
		BikeID *string `json:"bike_id"` // null: no bike
	}
	if !readJSON(w, r, &body) {
		return
	}
	act, err := a.Store.GetActivity(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "no such activity")
		return
	} else if err != nil {
		a.fail(w, r, err)
		return
	}
	if !activity.IsRide(act.Sport) {
		writeErr(w, http.StatusBadRequest, "only rides have a bike")
		return
	}
	if body.BikeID != nil {
		if _, err := a.Store.GetBike(ctx, *body.BikeID); errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusBadRequest, "no such bike")
			return
		} else if err != nil {
			a.fail(w, r, err)
			return
		}
	}
	if err := a.Store.SetActivityBike(ctx, act.ID, body.BikeID); err != nil {
		a.fail(w, r, err)
		return
	}
	a.getActivityBike(w, r)
}

func extraString(raw []byte, key string) string {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	s, _ := m[key].(string)
	return strings.TrimSpace(s)
}

func (a *API) deleteActivityBike(w http.ResponseWriter, r *http.Request) {
	if err := a.Store.ClearActivityBike(r.Context(), r.PathValue("id")); err != nil {
		a.fail(w, r, err)
		return
	}
	a.getActivityBike(w, r)
}
