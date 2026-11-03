# Product

## Register

product

## Platform

web

## Users

One athlete: a cyclist who also lifts. In the gym they are mid-set with sweaty hands, the phone propped on a bench or bar mount, reading the screen at arm's length in under two seconds between reps. That primary context is an installed PWA on iPhone, sometimes iPad. Secondary context: sitting down after a ride or a session to review it on iPad or Mac, unhurried and analytical.

Jobs to be done:
- In the gym: follow a planned strength session set by set, log what actually happened, and rest the right amount without thinking about the app.
- Afterwards: see how hard each set was and how well they recovered, using Whoop heart rate lined up against the logged sets.
- Over time: hand all of it to Claude (over MCP) as a coach, and stop depending on Strava.

## Product Purpose

Domestique is a self-hosted Outpost service on a Mac Mini. It imports workouts from Whoop, Wahoo, and Zwift automatically, stores raw and normalized data locally, and exposes it over REST and MCP. The PWA is the only place data is entered by hand, and it covers the live strength log, which no device records.

Success: a whole strength session gets logged with nothing but one tap per set, works with no signal in a basement gym, and later shows per-set heart rate effort and recovery without any manual reconciliation.

## Brand Personality

Loyal, precise, unflashy. The domestique rides for the leader: fetches bottles, blocks the wind, does the unglamorous work. The interface should feel like the inside of a pro team car: race radio, a clipboard of splits, hand-painted race numbers, tape on the dash. Calm under effort, terse, never celebratory for its own sake. Copy is short and plain, like a director sportif in your ear: "Set 3 of 4. 8 reps at 80 kg."

## Anti-references

- Strava: orange, social feeds, kudos, segment leaderboards.
- Neon-gradient fitness apps; purple-blue gradients.
- Glassmorphism.
- Identical icon card grids.
- Badges, streak flames, confetti, gamification of any kind.

## Design Principles

1. **Glanceable first.** The current set, target, and rest time must read at arm's length in under two seconds.
2. **Numbers are the hero.** Large tabular numerals; small, quiet labels.
3. **One action per state.** During a workout there is exactly one obvious thing to do next, within thumb reach at the bottom.
4. **Color carries meaning only.** Heart rate zones and the active state, never decoration.
5. **Never lose a set.** Offline-first. A logged set is durable on the device the moment it's tapped, and syncs when it can.

## Accessibility & Inclusion

- WCAG 2.2 AA contrast in both dark and light themes.
- Tap targets of at least 56px for workout actions (above the 24px AA minimum, for sweaty hands).
- Heart rate zones never encoded by color alone: always paired with a zone number or label.
- Respect `prefers-reduced-motion`: the rest-timer drain and set transitions fall back to instant changes.
- The rest timer announces completion non-visually (haptic where supported, plus a screen-reader live region).
