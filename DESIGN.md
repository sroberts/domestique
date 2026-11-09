---
name: Domestique
description: A self-hosted training log for a cyclist who lifts, built like the inside of a pro team car.
colors:
  graphite: "#16171a"
  graphite-surface: "#1f2126"
  graphite-raised: "#272a30"
  tape-line: "#2e3138"
  tape-line-strong: "#444852"
  bone: "#ece8df"
  dust: "#8a8f98"
  signal-yellow: "#f2c230"
  signal-yellow-press: "#d9ab1f"
  go-green: "#4fc283"
  go-green-press: "#3fae73"
  stop-red: "#f0656a"
  stop-red-press: "#e5484d"
  danger-red: "#e5484d"
  zone-1-slate: "#7d8590"
  zone-2-blue: "#4f8fd9"
  zone-3-green: "#45b07a"
  zone-4-amber: "#f2a33a"
  zone-5-red: "#e5484d"
  sport-ride: "#4f8fd9"
  sport-strength: "#cf7a26"
  sport-other: "#3fa88a"
  recovery-green: "#4fc283"
  recovery-amber: "#f2b43d"
  recovery-red: "#f0656a"
  fitness-violet: "#8c78ec"
  fatigue-rose: "#de5a78"
  light-bg: "#f4f1ea"
  light-surface: "#ffffff"
  light-line: "#ddd8cc"
  light-ink: "#1b1c1f"
  light-muted: "#6b6f76"
  light-signal: "#a87d00"
  light-go: "#1f7a49"
  light-stop: "#c8323a"
typography:
  live:
    fontFamily: "Barlow Condensed, Arial Narrow, sans-serif-condensed, sans-serif"
    fontSize: "6rem"
    fontWeight: 600
    lineHeight: 0.9
    fontFeature: "'tnum' 1, 'lnum' 1"
  target:
    fontFamily: "Barlow Condensed, Arial Narrow, sans-serif-condensed, sans-serif"
    fontSize: "3rem"
    fontWeight: 600
    lineHeight: 0.9
    fontFeature: "'tnum' 1, 'lnum' 1"
  stat:
    fontFamily: "Barlow Condensed, Arial Narrow, sans-serif-condensed, sans-serif"
    fontSize: "2rem"
    fontWeight: 600
    lineHeight: 0.9
    fontFeature: "'tnum' 1, 'lnum' 1"
  headline:
    fontFamily: "Public Sans, system-ui, -apple-system, Segoe UI, sans-serif"
    fontSize: "1.5rem"
    fontWeight: 600
    lineHeight: 1.2
  body:
    fontFamily: "Public Sans, system-ui, -apple-system, Segoe UI, sans-serif"
    fontSize: "1rem"
    fontWeight: 400
    lineHeight: 1.5
    letterSpacing: "0.005em"
  small:
    fontFamily: "Public Sans, system-ui, -apple-system, Segoe UI, sans-serif"
    fontSize: "0.875rem"
    fontWeight: 400
    lineHeight: 1.5
  label:
    fontFamily: "Public Sans, system-ui, -apple-system, Segoe UI, sans-serif"
    fontSize: "0.75rem"
    fontWeight: 600
    lineHeight: 1.3
    letterSpacing: "0.08em"
  mono:
    fontFamily: "IBM Plex Mono, ui-monospace, SF Mono, Menlo, monospace"
    fontSize: "0.75rem"
    fontWeight: 400
    fontFeature: "'tnum' 1"
rounded:
  s: "3px"
  m: "6px"
spacing:
  "1": "4px"
  "2": "8px"
  "3": "12px"
  "4": "16px"
  "5": "24px"
  "6": "32px"
  "7": "48px"
  "8": "64px"
components:
  button:
    backgroundColor: "transparent"
    textColor: "{colors.bone}"
    rounded: "{rounded.m}"
    padding: "0 16px"
    height: "44px"
  button-primary:
    backgroundColor: "{colors.signal-yellow}"
    textColor: "{colors.graphite}"
    rounded: "{rounded.m}"
    height: "64px"
    width: "100%"
  button-primary-active:
    backgroundColor: "{colors.signal-yellow-press}"
    textColor: "{colors.graphite}"
  button-go:
    backgroundColor: "{colors.go-green}"
    textColor: "{colors.graphite}"
    rounded: "{rounded.m}"
    height: "64px"
    width: "100%"
  button-go-active:
    backgroundColor: "{colors.go-green-press}"
    textColor: "{colors.graphite}"
  button-stop:
    backgroundColor: "{colors.stop-red}"
    textColor: "{colors.graphite}"
    rounded: "{rounded.m}"
    height: "64px"
    width: "100%"
  button-stop-active:
    backgroundColor: "{colors.stop-red-press}"
    textColor: "{colors.graphite}"
  button-quiet:
    backgroundColor: "transparent"
    textColor: "{colors.dust}"
    padding: "0 8px"
    height: "44px"
  plate:
    backgroundColor: "{colors.bone}"
    textColor: "{colors.graphite}"
    rounded: "{rounded.s}"
    size: "72px"
  plate-active:
    backgroundColor: "{colors.signal-yellow}"
    textColor: "{colors.graphite}"
    rounded: "{rounded.s}"
    size: "72px"
  tab:
    backgroundColor: "{colors.graphite}"
    textColor: "{colors.dust}"
    typography: "{typography.label}"
    height: "56px"
  tab-current:
    textColor: "{colors.bone}"
  input:
    backgroundColor: "{colors.graphite-surface}"
    textColor: "{colors.bone}"
    rounded: "{rounded.m}"
    padding: "0 12px"
    height: "44px"
  segment:
    backgroundColor: "{colors.graphite-surface}"
    textColor: "{colors.bone}"
    rounded: "{rounded.m}"
    padding: "0 12px"
    height: "56px"
  segment-selected:
    backgroundColor: "{colors.bone}"
    textColor: "{colors.graphite}"
---

# Design System: Domestique

## 1. Overview

**Creative North Star: "The Team Car"**

Domestique looks like the inside of a pro team car during a stage: graphite dash, a clipboard of splits, a race number pinned to a jersey, yellow tape where something needs attention, race radio in your ear. Everything earns its place by being read in a hurry. The athlete is mid-set with sweaty hands, glancing at a phone on a bench from arm's length, or sitting down after a ride to go through the numbers unhurried. The first moment sets the density: one huge number, one action. The second allows a dense, analytical page of charts and tables.

The system is flat and numeral-led, on graphite. Chrome stays graphite and bone; signal yellow marks the one thing to do now. Meaning gets colour at full strength, in solid blocks like race-broadcast captions and team kit: the day's recovery paints Today's verdict slab, every activity leads with a block in its sport's colour, heart-rate zones, form and fitness/fatigue each have their own hues. A good day and a bad day, a ride and a lift, look different before you read a word. Type is a three-voice instrument: condensed numerals for the numbers that matter, a plain humanist sans for words, and a monospace for timings and metadata. Shapes are barely rounded, like tape and painted plates, not pills. Motion only marks a change of state, in 150ms, and disappears under reduced motion.

It explicitly rejects Strava's orange, social feeds, kudos and leaderboards; neon-gradient fitness apps and purple-blue gradients; glassmorphism; identical icon card grids; and badges, streak flames, confetti or gamification of any kind.

**Key Characteristics:**
- Dark by default (graphite `#16171a`), with a complete light theme for daylight review.
- Numbers are the hero: Barlow Condensed tabular numerals, 6rem live, 3rem target, 2rem stat.
- One action colour, signal yellow, reserved for the current action and the active state. The one exception is mid-workout: Start set is green and Finish set is red, so the state reads at arm's length.
- Meaning carries colour at full strength: recovery slabs, sport blocks and tags, zones, form, chart roles. Chrome never does.
- Flat surfaces separated by 1px rules, not cards and shadows.
- Workout actions are at least 56px tall, and the primary action is 64px tall and full width at the bottom.
- Content sits in a single column: 35rem in the gym, 40rem for lists, 60rem for analysis on iPad or Mac.

## 2. Colors

A graphite dash and painted bone, with one stripe of team yellow and a zone ramp that only appears when there's heart rate to show.

### Primary
- **Signal Yellow** (`#f2c230`, light theme `#a87d00`): the team-car tape. The primary action button, the active set's plate, focus rings and the current selection. Pressed state is `#d9ab1f`. Text on it is graphite (`#16171a`).
- **Go and Stop** (green `#4fc283`, red `#f0656a`, pressed `#3fae73` and `#e5484d`; light theme `#1f7a49` and `#c8323a` with white text): the workout's set buttons. Start set is green, Finish set is red, so a glance from the bench says whether a set is running. Dark mode puts graphite text on them (8:1 and 5.8:1); light mode white (both above 5:1). They take the primary action's place and shape and are used nowhere else; Finish session stays signal yellow.

### Secondary
- **Zone Ramp** (Z1 slate `#7d8590`, Z2 blue `#4f8fd9`, Z3 green `#45b07a`, Z4 amber `#f2a33a`, Z5 red `#e5484d`; light theme darkens each step): heart-rate effort, on time-in-zone bars, the heart-rate trace and per-set effort. Always paired with the zone number or label.

### Tertiary
- **Recovery** (green `#4fc283`, amber `#f2b43d`, red `#f0656a`; light theme `#3fae73`, `#e9a326`, `#e5575d`): the day's Whoop recovery band, painted as a solid slab behind Today's verdict with dark ink on it (all at least 5:1). The same three hues mark form states: fresh, building, overreaching. Neutral form is zone slate.
- **Sport Identity** (ride `#4f8fd9`, strength `#cf7a26`, other `#3fa88a`; light theme `#2f6fb9`, `#a65b14`, `#0f7f5c`): which sport something is. Solid blocks behind activity icons, the sport tag on an activity, the route line on its map, effort bars, week stripes, and chart series. Ink on a sport block is graphite in dark mode and white in light mode (all at least 5:1).
- **Chart Roles** (fitness violet `#8c78ec`, fatigue rose `#de5a78`; light theme `#6a55d8`, `#c9476a`): the fitness and fatigue lines. Power wherever it's drawn uses the fitness violet; they never share a chart. Validated with the dataviz palette checks in both themes.
- **Danger Red** (`#e5484d`, light theme `#c8323a`): destructive actions and errors only. It shares a value with zone 5; context keeps them apart.

### Neutral
- **Graphite** (`#16171a`): page background, the tab bar, and the holes punched in race-number plates.
- **Graphite Surface** (`#1f2126`) and **Raised** (`#272a30`): inputs, segment buttons, the export menu. The only second layer; there is no third.
- **Tape Line** (`#2e3138`) and **Strong** (`#444852`): 1px rules between rows and sections, and button outlines. These do the structural work that cards would do elsewhere.
- **Bone** (`#ece8df`): all primary text, and the race-number plate at rest. Warm, not pure white, so a dark screen doesn't glare in a dim gym.
- **Dust** (`#8a8f98`): labels, units, secondary metadata and inactive tabs.
- **Light theme**: background `#f4f1ea`, surface `#ffffff`, rules `#ddd8cc`, ink `#1b1c1f`, muted `#6b6f76`. The plate inverts to ink-on-light.

### Named Rules
**The Tape Rule.** Signal yellow is only for "do this now" or "this one is active": the single primary action on screen, the plate of the set in progress, focus and selection. It never fills a card, a header, a chart series or decoration. If two things on one screen are yellow, one of them is wrong. During a workout the set buttons are green (start) and red (finish) instead; they're still the single primary action for that state.

**The Meaning-Paints Rule.** Colour belongs to things that mean something: a recovery, a sport, a zone, a form state, a chart role. Those get colour at full strength, as solid blocks with dark ink. Chrome (tabs, buttons, headers, borders) never wears a data colour, and a colour never appears without its word, number or icon beside it.

**The Slab Rule.** Data colour fills a block; it doesn't tint one. Today's verdict is a solid recovery-coloured slab, not an 8% wash with a coloured border. If a colour is so faint you have to look for it, it isn't doing its job.

## 3. Typography

**Numeral Font:** Barlow Condensed 600/700 (fallback Arial Narrow, condensed sans)
**Body Font:** Public Sans 400/600 (fallback system-ui, -apple-system, Segoe UI)
**Label/Mono Font:** IBM Plex Mono 400 (fallback ui-monospace, SF Mono, Menlo)

**Character:** A race-number condensed face for the numbers, a sturdy civic sans for the words, and a typewriter mono for timings, like splits on a clipboard. Three voices, each with one job, contrasting on width and rhythm rather than competing.

### Hierarchy
- **Live** (Barlow Condensed 600, 6rem, line-height 0.9, tabular lining figures): the one number you read from across the gym, such as the rest countdown or the weight on the bar.
- **Target** (Barlow Condensed 600, 3rem, 0.9): the prescription beside the live number ("8 reps"), and "of 4" next to the set plate.
- **Stat** (Barlow Condensed 600, 2rem, 0.9; up to 1.75rem in the activity stat grid): summary numbers in stat grids, with a small dust-coloured unit next to them.
- **Headline** (Public Sans 600, 1.5rem, 1.2, balanced wrap): screen titles and activity names.
- **Body** (Public Sans 400, 1rem, 1.5, +0.005em): prose and controls. Notes cap at 62ch.
- **Small** (Public Sans 400, 0.875rem): secondary copy and table cells.
- **Label** (Public Sans 600, 0.75rem, +0.08em, uppercase, dust): data labels that sit above or beside a number ("MOVING", "AVG HR"). Nowhere else.
- **Mono** (IBM Plex Mono 400, 0.75–0.875rem, tabular): times, dates, durations, source metadata and chart axis values.

### Named Rules
**The Numbers-Are-the-Hero Rule.** Any number the athlete acts on is set in Barlow Condensed with tabular figures, and is the largest thing near it. Its label is small, quiet and uppercase. A large label above a small number is backwards.

**The Label-Means-Data Rule.** The uppercase, tracked label style is only for naming a measurement. It is never used as a decorative kicker above a section heading.

## 4. Elevation

The system is flat. Depth comes from tone (graphite, then surface, then raised) and from 1px tape lines, not shadows. Everything sits on the dash, and sections are divided by rules rather than lifted into cards.

### Shadow Vocabulary
- **Plate ring** (`box-shadow: 0 0 0 3px var(--bg), 0 0 0 5px var(--signal)`): a yellow ring around the race-number plate while a set is in progress. It's a state indicator, not elevation.
- **Popover** (`box-shadow: 0 8px 24px rgb(0 0 0 / 0.25)`): the export menu only, because it floats over content.

### Named Rules
**The Flat Dash Rule.** Surfaces are flat at rest. A shadow appears only on something that floats over content (a menu) or as a state ring. If a list row or stat block has a shadow, remove it and use a 1px tape line instead.

## 5. Components

### Buttons
Plain, tactile and squared off, like switches on the dash.
- **Shape:** gently squared corners (6px).
- **Default:** transparent with a 1px strong tape-line outline, bone text, Public Sans 600 at 1rem, 44px minimum height, 16px horizontal padding. Hover lifts the outline to dust; pressing scales it to 0.98 over 150ms.
- **Primary (the action):** signal yellow, graphite text, 1.25rem bold, full width, 64px tall, sitting at the bottom within thumb reach. One per screen. Pressed state goes to `#d9ab1f`.
- **Quiet:** no outline, dust text that turns bone on hover. For back links and secondary actions.
- **Danger:** a red outline and red text, never a red fill.
- **Disabled:** 40% opacity, with no press effect.
- **Focus:** a 2px signal-yellow outline, offset 2px, on every interactive element.

### Race-Number Plate (signature)
The current set number, painted like a race number pinned to a jersey.
- **Shape:** a 72px bone square with 3px corners, Barlow Condensed 700 at 3.5rem, graphite numeral, and four punched "safety-pin" holes in the background colour at the corners.
- **Active:** painted signal yellow with the yellow plate ring while the set is in progress. "of 4" sits beside it in target size.
- Screen readers hear "Set 3 of 4"; the visual plate is hidden from them.

### Segments and Switches
Used for choosing a view, such as the Combined / per-recording switch on an activity.
- **Style:** 56px tall, raised-surface background, 1px strong outline, 6px corners, small Public Sans text. They wrap onto further rows rather than scroll.
- **Selected:** inverted to a bone fill with graphite text, weight 600, and exposed as `aria-pressed`.

### Recovery Slab (signature)
- Today's verdict: a full-width slab in the day's recovery colour, 6px corners, dark ink. "RECOVERY" label top left, the score in 6rem Barlow Condensed top right, then the verdict headline and one sentence. With no recovery yet it falls back to the raised graphite surface and "Not scored yet". The colour changes in 150ms.

### Sport Blocks and Tags
- Activity rows lead with a 36px solid block in the sport colour holding the sport icon in ink. The activity page puts a small solid sport tag before the date, and draws the route in the sport colour. Each week's header carries a 4px "kit stripe": that week's load split by sport, full at 500 TSS, over a faint track.

### Effort and State Tags
- A 3px effort bar under each activity's TSS, filled from the right in its sport colour over a faint track, full at 150 TSS. Form shows as a small solid tag in its state colour carrying the word ("fresh").

### Stat Grid
- Three columns on a phone and five from 640px, ruled top and bottom with tape lines and no boxes. Each cell is a label over a stat numeral with a dust unit next to it.

### Zone Rows
- One row per zone: the zone label with its bpm or watt threshold, a bar in the zone colour, the time in mono, and the percentage. A bar is never shown without its label.

### Comparison Table
- Used for recordings of one workout side by side. Rules only, no fills. Row labels are dust and stay pinned while the table scrolls sideways. Values are in mono, with a "–" where a recording didn't measure something. Column headers are underlined buttons that switch the page to that recording.

### Inputs / Fields
- **Style:** surface background, 1px strong outline, 6px corners, 44px tall, monospace for IDs, secrets and numbers.
- **Focus:** the global 2px signal-yellow outline.
- **Error:** a danger-red message under the field, announced with `role="alert"`.

### Navigation
- A fixed bottom tab bar on graphite with a tape-line top rule, 56px tall plus the safe area, and six equal tabs (icon over label, 0.75rem Public Sans 600). Inactive tabs are dust; the current tab is bone. Pressing shows a faint band. Below 375px wide the labels drop to 0.6875rem with no tracking so they don't run together.

## 6. Do's and Don'ts

### Do:
- **Do** keep exactly one primary action per state, full width, 64px tall, at the bottom of the screen: signal yellow, except Start set (green) and Finish set (red).
- **Do** set every number the athlete acts on in Barlow Condensed with tabular figures, as the largest element near it.
- **Do** pair every zone colour with its zone number or label ("Z3 150+"), in both the bar and the legend.
- **Do** paint meaning at full strength: recovery as a solid slab, sports as solid blocks, with dark ink at 4.5:1 or better.
- **Do** run any new chart colours through the dataviz palette validator in both themes before using them.
- **Do** separate sections with 1px tape lines (`#2e3138`) and spacing from the 4/8px scale.
- **Do** make workout actions at least 56px tall (`--tap-min`), and every other control at least 44px.
- **Do** check both themes: every text colour must meet WCAG 2.2 AA on graphite and on `#f4f1ea`.
- **Do** keep motion to 150ms state changes using `cubic-bezier(0.22, 1, 0.36, 1)`, with instant changes under `prefers-reduced-motion`.

### Don't:
- **Don't** use Strava's orange, social feeds, kudos or segment leaderboards.
- **Don't** use neon-gradient fitness-app styling or purple-blue gradients, and don't use gradient text anywhere.
- **Don't** use glassmorphism: no blurred translucent panels.
- **Don't** build identical icon card grids; lists are ruled rows.
- **Don't** add badges, streak flames, confetti, or gamification of any kind. Nothing celebrates for its own sake.
- **Don't** use signal yellow for decoration, headers, chart series or more than one element on a screen.
- **Don't** put a data colour (recovery, sport, zone, chart role) on chrome: tabs, buttons, headers, borders.
- **Don't** tint meaning faintly (an 8% wash, a coloured hairline) when it should be a solid block.
- **Don't** add a coloured stripe border (border-left or border-right over 1px) to rows, callouts or cards.
- **Don't** round anything past 6px or make pill buttons; the shape language is tape and plates.
- **Don't** use the uppercase label style as a section kicker.
- **Don't** add shadows to rows, cards or stat blocks. If it looks lifted off the dash, it's wrong.
