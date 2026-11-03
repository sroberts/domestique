package export

import (
	"encoding/csv"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode"

	"domestique/internal/activity"
	"domestique/internal/store"
)

// StreamCSV writes the per-second stream: one row per sample, a column per
// channel the stream has. Missing samples are empty cells, not zeros.
func StreamCSV(w io.Writer, start time.Time, s *activity.Stream) error {
	cw := csv.NewWriter(w)
	head := []string{"time", "elapsed_s"}
	type col struct {
		name string
		get  func(i int) string
	}
	ints := func(v []int32) func(int) string {
		return func(i int) string {
			if v[i] < 0 {
				return ""
			}
			return strconv.Itoa(int(v[i]))
		}
	}
	floats := func(v []float32, scale float64, prec int) func(int) string {
		return func(i int) string {
			return strconv.FormatFloat(float64(v[i])*scale, 'f', prec, 64)
		}
	}
	var cols []col
	if s.Power != nil {
		cols = append(cols, col{"power_w", ints(s.Power)})
	}
	if s.HR != nil {
		cols = append(cols, col{"heart_rate_bpm", ints(s.HR)})
	}
	if s.Cadence != nil {
		cols = append(cols, col{"cadence_rpm", ints(s.Cadence)})
	}
	if s.Speed != nil {
		cols = append(cols, col{"speed_kmh", floats(s.Speed, 3.6, 2)})
	}
	if s.Distance != nil {
		cols = append(cols, col{"distance_m", floats(s.Distance, 1, 1)})
	}
	if s.Altitude != nil {
		cols = append(cols, col{"altitude_m", floats(s.Altitude, 1, 1)})
	}
	if s.HasGPS() {
		pos := func(v []float64) func(int) string {
			return func(i int) string {
				if s.Lat[i] == 0 && s.Lng[i] == 0 {
					return ""
				}
				return strconv.FormatFloat(v[i], 'f', 6, 64)
			}
		}
		cols = append(cols, col{"latitude", pos(s.Lat)}, col{"longitude", pos(s.Lng)})
	}
	for _, c := range cols {
		head = append(head, c.name)
	}
	if err := cw.Write(head); err != nil {
		return err
	}
	row := make([]string, len(head))
	for i := 0; i < s.Len(); i++ {
		row[0] = start.Add(time.Duration(s.T[i]) * time.Second).UTC().Format(time.RFC3339)
		row[1] = strconv.Itoa(int(s.T[i]))
		for k, c := range cols {
			row[k+2] = c.get(i)
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// SetsHeader is the header of a sets CSV.
var SetsHeader = []string{"session_id", "session", "date", "exercise", "set", "reps", "weight_kg",
	"target_reps", "target_weight_kg", "started_at", "ended_at", "duration_s"}

// SetsCSV writes logged strength sets. Times are ISO 8601 in the athlete's
// zone, corrected by the session's measured clock offset.
type SetsCSV struct {
	w   *csv.Writer
	loc *time.Location
}

// NewSetsCSV starts a sets CSV, writing the header.
func NewSetsCSV(w io.Writer, loc *time.Location) (*SetsCSV, error) {
	if loc == nil {
		loc = time.UTC
	}
	c := &SetsCSV{w: csv.NewWriter(w), loc: loc}
	return c, c.w.Write(SetsHeader)
}

// Session appends one session's sets.
func (c *SetsCSV) Session(sess store.Session, sets []store.Set) error {
	iso := func(ms int64) string {
		return time.UnixMilli(ms + sess.ClockOffsetMs).In(c.loc).Format(time.RFC3339)
	}
	date := time.UnixMilli(sess.StartedAt + sess.ClockOffsetMs).In(c.loc).Format("2006-01-02")
	for _, s := range sets {
		err := c.w.Write([]string{
			sess.ID, sess.Name, date, s.ExerciseName, strconv.Itoa(s.SetIndex + 1),
			strconv.Itoa(s.Reps), num(s.WeightKg), strconv.Itoa(s.TargetReps), num(s.TargetWeightKg),
			iso(s.StartedAt), iso(s.EndedAt), strconv.FormatInt((s.EndedAt-s.StartedAt)/1000, 10),
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// Close flushes the CSV.
func (c *SetsCSV) Close() error {
	c.w.Flush()
	return c.w.Error()
}

func num(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// ActivityRow is one line of activities.csv.
type ActivityRow struct {
	Activity store.Activity
	Metrics  activity.Metrics
	Sources  []string
	Bike     string
	Files    []string
}

// ActivitiesHeader is the header of activities.csv.
var ActivitiesHeader = []string{"id", "date", "start", "sport", "name", "elapsed_s", "moving_s", "distance_km",
	"avg_power_w", "normalized_power_w", "max_power_w", "work_kj", "avg_hr_bpm", "max_hr_bpm", "avg_cadence_rpm",
	"ascent_m", "tss", "intensity", "ftp_w", "load_method", "strain", "sources", "bike", "files"}

// ActivitiesCSV writes one row per activity.
type ActivitiesCSV struct {
	w   *csv.Writer
	loc *time.Location
}

// NewActivitiesCSV starts activities.csv, writing the header.
func NewActivitiesCSV(w io.Writer, loc *time.Location) (*ActivitiesCSV, error) {
	if loc == nil {
		loc = time.UTC
	}
	c := &ActivitiesCSV{w: csv.NewWriter(w), loc: loc}
	return c, c.w.Write(ActivitiesHeader)
}

// Row appends one activity.
func (c *ActivitiesCSV) Row(r ActivityRow) error {
	a, m := r.Activity, r.Metrics
	t := time.UnixMilli(a.StartAt).In(c.loc)
	opt := func(v int) string {
		if v <= 0 {
			return ""
		}
		return strconv.Itoa(v)
	}
	strain := ""
	if a.Strain != nil {
		strain = strconv.FormatFloat(*a.Strain, 'f', 1, 64)
	}
	f1 := func(v float64) string {
		if v <= 0 {
			return ""
		}
		return strconv.FormatFloat(v, 'f', 1, 64)
	}
	intensity := ""
	if a.Intensity > 0 {
		intensity = strconv.FormatFloat(a.Intensity, 'f', 3, 64)
	}
	return c.w.Write([]string{
		a.ID, t.Format("2006-01-02"), t.Format(time.RFC3339), a.Sport, a.Name,
		strconv.Itoa(a.ElapsedS), strconv.Itoa(a.MovingS), strconv.FormatFloat(a.DistanceM/1000, 'f', 2, 64),
		opt(m.AvgPower), opt(m.NP), opt(m.MaxPower), f1(m.KJ), opt(m.AvgHR), opt(m.MaxHR), opt(m.AvgCadence),
		f1(m.AscentM), f1(a.TSS), intensity, opt(a.FTP), a.LoadMethod, strain,
		strings.Join(r.Sources, ";"), r.Bike, strings.Join(r.Files, ";"),
	})
}

// Close flushes the CSV.
func (c *ActivitiesCSV) Close() error {
	c.w.Flush()
	return c.w.Error()
}

// Slug makes a file-name-safe, lower-case ASCII rendering of s: letters and
// digits kept, everything else collapsed to single dashes, at most 60 bytes.
func Slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
			dash = false
		default:
			if r := fold(r); r != 0 {
				b.WriteRune(r)
				dash = false
				continue
			}
			if b.Len() > 0 && !dash {
				b.WriteByte('-')
				dash = true
			}
		}
		if b.Len() >= 60 {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}

// fold maps common accented Latin letters to ASCII ("Côte" → "cote").
func fold(r rune) rune {
	return foldMap[r]
}

var foldMap = func() map[rune]rune {
	m := map[rune]rune{}
	for k, v := range map[string]rune{"àáâãäå": 'a', "èéêë": 'e', "ìíîï": 'i', "òóôõöø": 'o', "ùúûü": 'u', "ýÿ": 'y', "ñ": 'n', "ç": 'c', "ß": 's'} {
		for _, r := range k {
			m[r] = v
		}
	}
	return m
}()

// FileName is "2026-11-02-morning-ride.fit": the athlete's local date, a slug
// of the name (or of fallback), and ext without its dot.
func FileName(t time.Time, name, fallback, ext string) string {
	slug := Slug(name)
	if slug == "" {
		slug = Slug(fallback)
	}
	if slug == "" {
		slug = "activity"
	}
	return t.Format("2006-01-02") + "-" + slug + "." + strings.TrimPrefix(ext, ".")
}
