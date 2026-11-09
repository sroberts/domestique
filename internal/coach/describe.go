package coach

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ---- Gear upkeep from the athlete's own descriptions ----------------------------

// GearEvent is a part the athlete wrote about fitting: "new chain today".
// It's only ever a suggestion; nothing changes until the athlete accepts it.
type GearEvent struct {
	Part   string `json:"part"`           // one of the kinds asked with
	Name   string `json:"name,omitempty"` // the part as written, when it's named: "GP5000s", "saddle"
	Action string `json:"action"`         // replaced or installed
	Bike   string `json:"bike,omitempty"` // the bike as written, if named
	Quote  string `json:"quote"`          // the athlete's own words it came from
}

// GearRead is what one description said about gear, kept per description.
type GearRead struct {
	Events []GearEvent `json:"events"`
	Model  string      `json:"model,omitempty"`
}

// gearWords finds descriptions that may say something about parts, so the
// LLM is asked only about those.
var gearWords = regexp.MustCompile(`(?i)\b(chains?|tyres?|tires?|tubeless|cassettes?|brakes?|pads|sprockets?|cogs?|rotors?|chainrings?|saddle|bar ?tape|cleats?|derailleur|bottom bracket|groupset|wheels?)\b`)

// MentionsGear reports whether a description may mention a bike part.
func MentionsGear(description string) bool { return gearWords.MatchString(description) }

const gearPrompt = `You read one cyclist's own note about a workout and pick out bike parts they say were fitted: replaced or newly installed. Parts are these kinds: %s. Anything else that wears, such as a saddle, bar tape or cleats, is "other", with its name.

Only take what the note plainly says happened. A part that was cleaned, lubed, waxed, adjusted, checked or merely mentioned ("chain was noisy") was not fitted: leave it out, or give it as "serviced". Never guess a bike: give one only when the note names it.

Reply with only a JSON object, no other text:
{"events": [{"part": "<kind>", "name": "<the part as written, or empty>", "action": "replaced|installed|serviced", "bike": "<bike as written, or empty>", "quote": "<the exact words from the note>"}]}
With nothing fitted, reply {"events": []}.`

// GearPrompt is the prompt for the given part kinds; it fingerprints the
// answers kept per description.
func GearPrompt(kinds []string) string { return fmt.Sprintf(gearPrompt, strings.Join(kinds, ", ")) }

// GearEvents has the LLM read the parts fitted from a description. Every
// event must quote the description, and name a known kind and a bike the
// description names; others are dropped. Serviced parts are dropped too:
// the gear store keeps fittings, not services.
func (w *Writer) GearEvents(ctx context.Context, description string, kinds []string) (GearRead, error) {
	text, model, err := w.AI.Chat(ctx, GearPrompt(kinds), "Note:\n"+description)
	if err != nil {
		return GearRead{}, err
	}
	var out GearRead
	if err := parseObject(text, &out); err != nil {
		return GearRead{}, fmt.Errorf("%w (model %s)", err, model)
	}
	read := GearRead{Events: []GearEvent{}, Model: model}
	in := squash(description)
	for _, e := range out.Events {
		e.Part, e.Action = strings.ToLower(strings.TrimSpace(e.Part)), strings.ToLower(strings.TrimSpace(e.Action))
		e.Name, e.Bike, e.Quote = strings.TrimSpace(e.Name), strings.TrimSpace(e.Bike), strings.TrimSpace(e.Quote)
		switch {
		case !slices.Contains(kinds, e.Part), e.Action != "replaced" && e.Action != "installed":
			continue
		case e.Quote == "" || !strings.Contains(in, squash(e.Quote)):
			continue // not the athlete's words
		case e.Bike != "" && !strings.Contains(in, squash(e.Bike)):
			e.Bike = "" // a bike the note doesn't name is a guess
		}
		if utf8.RuneCountInString(e.Name) > 40 || (e.Name != "" && !strings.Contains(in, squash(e.Name))) {
			e.Name = ""
		}
		if e.Part == "other" && e.Name == "" {
			continue // an unnamed "other" part says nothing
		}
		if !slices.ContainsFunc(read.Events, func(x GearEvent) bool { return x.Part == e.Part && x.Name == e.Name }) && len(read.Events) < 4 {
			read.Events = append(read.Events, e)
		}
	}
	return read, nil
}

// squash lowercases and collapses spacing and quote marks, for checking a
// quote is the athlete's own words.
func squash(s string) string {
	s = strings.NewReplacer("’", "'", "‘", "'", "“", `"`, "”", `"`).Replace(strings.ToLower(s))
	return strings.Join(strings.Fields(s), " ")
}

// ---- A better name than "Afternoon Ride" ---------------------------------------

var genericRe = regexp.MustCompile(`(?i)^(?:(?:morning|afternoon|evening|night|lunch|midday|early morning|late)\s+)?` +
	`(?:ride|run|walk|hike|swim|lift|activity|workout|indoor ride|virtual ride|indoor cycling|cycling|running|walking|` +
	`e-bike ride|mountain bike ride|gravel ride|weight training|strength training|training|zwift|session)$`)

// GenericName reports whether an activity name is a device or app default
// ("Afternoon Ride", "Morning Activity", "Zwift", "Workout") rather than one
// the athlete chose.
func GenericName(name string) bool {
	return genericRe.MatchString(strings.Join(strings.Fields(name), " "))
}

// NameFacts is what a suggested name is built from.
type NameFacts struct {
	Name    string   `json:"current_name"`
	Sport   string   `json:"sport"`
	Route   string   `json:"route,omitempty"`
	When    string   `json:"when"` // "Saturday morning"
	Minutes int      `json:"minutes"`
	KM      *float64 `json:"distance_km,omitempty"`
	AscentM *int     `json:"ascent_m,omitempty"`
	Notes   string   `json:"athletes_own_notes,omitempty"`
}

const namePrompt = `You name one finished workout for a cyclist who also lifts, to replace a default name like "Afternoon Ride". Build the name from the route and the athlete's own notes: the place, the climb, the people, the kind of session. Two to six words, at most 40 characters, in title case or plain words. No emoji, no exclamation marks, no quotation marks, no time of day, no numbers unless they're in the facts.

If the route and notes give nothing specific, reply with an empty name rather than another generic one.

Examples of the style, not of this workout:
{"name": "Coast Road with the Club"}
{"name": "Tempus Fugit, Easy Spin"}
{"name": "Hill Repeats on Box Hill"}

Reply with only a JSON object, no other text:
{"name": "<the name, or empty>"}`

// NameKey keys an activity's suggested name.
func NameKey(activityID string) string { return "ai:name:" + activityID }

// NameSuggestion returns a suggested name for an activity, or starts writing
// one. A kept empty Text means the model found nothing better to say.
func (w *Writer) NameSuggestion(ctx context.Context, id string, f NameFacts) (n Note, ok, pending bool) {
	ok, pending = w.Do(ctx, NameKey(id), Fingerprint(namePrompt, f), &n,
		func(ctx context.Context) (any, error) { return w.suggestName(ctx, f) })
	return n, ok, pending
}

func (w *Writer) suggestName(ctx context.Context, f NameFacts) (Note, error) {
	in, _ := json.MarshalIndent(f, "", "  ")
	text, model, err := w.AI.Chat(ctx, namePrompt, "Facts:\n"+string(in))
	if err != nil {
		return Note{}, err
	}
	var out struct {
		Name string `json:"name"`
	}
	if err := parseObject(text, &out); err != nil {
		return Note{}, fmt.Errorf("%w (model %s)", err, model)
	}
	name := strings.Trim(strings.Join(strings.Fields(out.Name), " "), `"'.`)
	if name == "" || GenericName(name) || strings.EqualFold(name, f.Name) {
		return Note{Model: model}, nil // nothing better: keep that, and don't ask again
	}
	if err := checkProse(name, 40, f); err != nil {
		return Note{}, fmt.Errorf("%w (model %s)", err, model)
	}
	for _, r := range name {
		if unicode.Is(unicode.So, r) || unicode.Is(unicode.Sk, r) || r == '#' || r == '@' {
			return Note{}, fmt.Errorf("name has a symbol (model %s): %q", model, name)
		}
	}
	return Note{Text: name, Model: model}, nil
}
