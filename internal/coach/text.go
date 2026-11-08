package coach

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	thinkRe  = regexp.MustCompile(`(?s)<think>.*?</think>`)
	numberRe = regexp.MustCompile(`\d+(?:\.\d+)?`)
)

// parseObject reads the JSON object in an LLM reply, ignoring code fences,
// <think> blocks and anything around the braces.
func parseObject(text string, out any) error {
	text = thinkRe.ReplaceAllString(text, "")
	i, j := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if i < 0 || j <= i {
		return fmt.Errorf("reply is not JSON: %q", clip(text))
	}
	if err := json.Unmarshal([]byte(text[i:j+1]), out); err != nil {
		return fmt.Errorf("reply is not the expected JSON: %q", clip(text))
	}
	return nil
}

// checkProse rejects prose that breaks the voice or makes up numbers: every
// number in it must appear in facts, apart from small counts.
func checkProse(s string, maxRunes int, facts any) error {
	switch {
	case strings.TrimSpace(s) == "":
		return fmt.Errorf("reply is empty")
	case utf8.RuneCountInString(s) > maxRunes:
		return fmt.Errorf("too long: %q", clip(s))
	case strings.Contains(s, "!"):
		return fmt.Errorf("reply breaks the voice: %q", clip(s))
	}
	known := knownNumbers(facts)
	for _, n := range numberRe.FindAllString(s, -1) {
		if !known[n] {
			return fmt.Errorf("reply has a number not in the facts (%s): %q", n, clip(s))
		}
	}
	return nil
}

// knownNumbers is every number in facts, plus the small counts ("zone 2",
// "one or 2 sets") that need no source.
func knownNumbers(facts any) map[string]bool {
	in, _ := json.Marshal(facts)
	m := map[string]bool{}
	for _, n := range numberRe.FindAllString(string(in), -1) {
		m[n] = true
		// 1.0 in JSON may be written 1 in prose, and vice versa.
		if t := strings.TrimSuffix(strings.TrimRight(n, "0"), "."); strings.Contains(n, ".") {
			m[t] = true
		}
	}
	for i := 0; i <= 10; i++ {
		m[fmt.Sprint(i)] = true
	}
	return m
}

func clip(s string) string {
	if utf8.RuneCountInString(s) > 120 {
		r := []rune(s)
		return string(r[:120]) + "…"
	}
	return s
}

// voice is the house style every prompt shares (PRODUCT.md: a directeur
// sportif in your ear).
const voice = `Voice: terse, plain, calm, like a directeur sportif on the team radio. Short sentences. No exclamation marks, no emoji, no praise, no hype, no greetings. Speak to the athlete as "you".

Use only numbers that appear in the facts, as given, keeping their sign; never invent a number, a goal or an event, and don't do arithmetic. Leave out anything that isn't in the facts. Don't explain training terms: fitness, fatigue, form, TSS, NP and zones are known.`
