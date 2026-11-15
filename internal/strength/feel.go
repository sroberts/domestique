package strength

import (
	"strings"

	"domestique/internal/checkin"
	"domestique/internal/store"
)

// AfterCheckIns carries how yesterday's workouts felt into today's call.
// Dead legs, or pain that changed a workout, mean hold back (push or normal
// becomes hold); heavy legs take the push out of a fresh day; a niggle is
// only mentioned. The rules stay in charge: this never makes a day harder.
func AfterCheckIns(r Readiness, yesterday []store.CheckIn) Readiness {
	var dead, heavy bool
	var pain, niggle []string
	for _, c := range yesterday {
		switch c.Legs {
		case "dead":
			dead = true
		case "heavy":
			heavy = true
		}
		var parts []string
		for _, p := range c.Where {
			parts = append(parts, checkin.PartWord(p))
		}
		switch c.Pain {
		case "pain":
			pain = append(pain, parts...)
		case "niggle":
			niggle = append(niggle, parts...)
		}
	}
	where := func(parts []string, none string) string {
		if len(parts) == 0 {
			return none
		}
		return strings.Join(dedupe(parts), " and ")
	}
	hold := func(title string) bool {
		if r.Level == "push" || r.Level == "normal" {
			r.Level, r.Title = "hold", title
			return true
		}
		return false
	}
	var add []string
	switch {
	case len(pain) > 0:
		raised := hold("Mind yesterday's pain")
		add = append(add, "Yesterday's "+strings.TrimSpace(where(pain, "")+" pain")+" changed the workout: nothing today that aggravates it.")
		if raised {
			add = append(add, "Hold the weights rather than adding load.")
		}
	case dead:
		hold("Legs still tired")
		add = append(add, "Your legs felt dead yesterday: train, but hold the weights and keep the riding steady.")
	case heavy:
		if r.Level == "push" {
			r.Level, r.Title = "normal", "Train as planned"
		}
		add = append(add, "Legs felt heavy yesterday.")
	}
	if len(pain) == 0 && len(niggle) > 0 {
		add = append(add, "Keep an eye on the "+strings.TrimSpace(where(niggle, "")+" niggle")+".")
	}
	if len(add) > 0 {
		r.Detail = strings.TrimSpace(r.Detail + " " + strings.Join(add, " "))
	}
	return r
}

func dedupe(xs []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
