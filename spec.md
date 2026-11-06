# Domestique: Workout Tracker Spec

## Bottom Line

Domestique is a self-hosted Outpost service and progressive web app on Scott's Mac Mini that automatically imports workouts from Whoop, Wahoo, and Zwift, stores them locally, and exposes them over REST and MCP. No manual logging. Long-term goal: replace Strava entirely.

## Goals

- Automated ingestion from Whoop, Wahoo, and Zwift with zero manual entry
- Self-owned storage of raw files and normalized data
- One unified view across mixed workout types (rides, strength, recovery)
- REST and MCP access so Claude can coach directly from the data

## Non-Goals (v1)

- Social features, segments, or leaderboards
- Manual workout entry UI
- Running Zwift on the Mac Mini (iPad stays the primary Zwift device)

## Architecture

One Outpost service with three importers, a normalization layer, a dedup engine, and a SQLite store.

### Whoop Importer

- Official Whoop Developer API, OAuth 2.0
- Webhooks for new workouts, recovery, and sleep, plus a nightly backfill poll
- Data: strain, heart rate zones, recovery, HRV, sleep

### Wahoo Importer

- Wahoo Cloud API, OAuth 2.0
- Workout webhooks trigger download of the raw FIT file
- Fallback: Wahoo's Dropbox auto-sync folder

### Zwift Importer

- Zwift has no public API; use the community-documented endpoints to list and download activity FIT files
- Poll every 15 minutes, with exponential backoff on failure
- Risk: unofficial API may break. Fallback: run Zwift on the Mac Mini and watch the local Activities folder

## Storage

- Raw files: FIT and JSON stored unmodified on disk, content-addressed by SHA-256
- SQLite tables: activities, streams (per-second samples), recovery, sleep, sources, sync\_state
- Every activity keeps a link to all of its source records

## Deduplication

A Zwift ride recorded on a Wahoo head unit produces two records, and Whoop may log a third.

- Match recordings whose time spans overlap by at least 80% of the shorter one (start times alone miss Whoop, which notices a workout minutes after it starts)
- Use heart rate stream correlation to tell apart workouts that only partly coincide; near-total overlap is one workout regardless
- The activity list shows one entry per workout; its detail page can show every recording's own version alongside the combined one
- Merge into one canonical activity; precedence: Wahoo for power, cadence, and heart rate (chest strap beats wrist band whenever the two disagree), Zwift for virtual route, Whoop for strain and recovery
- Keep all source records; never delete raw data

## Strength Workout Matching

Strength sessions get heart rate from Whoop, since no Wahoo or Zwift record exists for them.

- Detect strength activities from Whoop workout type and from time blocks with no ride record
- Attach the Whoop heart rate stream, strain, and zones to each strength session
- Report heart rate against each strength workout: average, peak, time in zones, and recovery between sets where the data allows
- Exercise detail comes from the live Strength Workout View below

## Strength Workout View

A live logging screen, delivered as a progressive web app served by Outpost, records each set as it happens, timestamped to line up with Whoop heart rate. It runs on iPhone or iPad from the home screen.

- Load a planned workout: exercises, target sets, reps, and weight
- Follow along set by set; tap to start and finish each set
- Log actual reps and weight as you go; edits allowed after
- Rest timer between sets
  - Offline-first: sets queue on the device and sync to Outpost when the connection returns; timestamps come from the device clock at logging time
- Each set stores start and end timestamps, so the merge step slices the Whoop heart rate stream per set and per rest period
- Post-workout summary: heart rate peak and recovery per set, total volume, strain

## Interfaces

- REST: list and query activities, streams, recovery, and training load
- MCP tools: get\_recent\_activities, get\_training\_load, get\_recovery, get\_activity\_detail
- Derived metrics: TSS, CTL, ATL, TSB computed from power data

## Additional Features

These features turn the store into a training tool, not just an archive.

1. **Workout templates and progression:** saved strength templates; the app pre-fills last session's weights and suggests the next increase.
2. **Combined training load:** one fatigue model across rides (TSS from power) and strength (Whoop strain mapped to a load score), feeding CTL, ATL, and TSB.
3. **Recovery-aware planning:** pull the morning Whoop recovery score and flag days to reduce intensity or swap sessions.
4. **PRs and trends:** power curve PRs, lift PRs, estimated one-rep max, and long-term trend charts.
5. **Offsite backups:** automated encrypted nightly backups of SQLite and raw files to an offsite target, with restore testing.
6. **Gear tracking:** bike and component mileage (chain, tires), chest strap battery reminders.
7. **Export:** standard FIT and CSV export of any activity or date range, so the data never locks in.

## Name

**Domestique.** In pro cycling, the domestique rides for the leader: fetches bottles, blocks the wind, does the unglamorous work. This app does the same with data. It fetches every ride and lift so Scott only has to train. Repo and service name: `domestique`. Home screen label: Domestique (10 characters, fits iOS without truncation).

## Visual Style

Structured as a design context for impeccable. Direction: **Team Car**, the inside of a pro team car with race radio, a clipboard of splits, hand-painted race numbers, and tape on the dash.

**Users:** one athlete, mid-set, sweaty hands, phone on a bench or bar mount, reading the screen in under two seconds. Secondary: post-ride analysis on iPad or Mac.

**Brand personality:** loyal, precise, unflashy.

**Aesthetic direction:** dark graphite cabin, bone-white numerals, one signal color. Data-dense but calm. Utility over decoration.

**Anti-references:** Strava orange and social feeds; neon gradient fitness apps; purple-blue gradients; glassmorphism; identical icon card grids; badges, streak flames, and confetti.

**Design principles:**

1. Glanceable first: current set, target, and rest time readable at arm's length.
2. Numbers are the hero: large tabular numerals, small quiet labels.
3. One action per state during a workout, with tap targets of at least 56px in the bottom thumb zone.
4. Color carries meaning only: heart rate zones and the active state, never decoration.
5. Dark by default for the gym and pain cave, with a full light theme for daylight.

**Color tokens:**

| Token | Dark | Light | Use |
| --- | --- | --- | --- |
| bg | #16171A | #F4F1EA | App background |
| surface | #1F2126 | #FFFFFF | Panels, sheets |
| line | #2E3138 | #DDD8CC | Dividers, borders |
| text | #ECE8DF | #1B1C1F | Primary text, numerals |
| text-muted | #8A8F98 | #6B6F76 | Labels, metadata |
| signal | #F2C230 | #A87D00 | Active set, primary action, PRs |
| zone-1 | #7D8590 | #7D8590 | HR zone 1 |
| zone-2 | #4F8FD9 | #2F6FB9 | HR zone 2 |
| zone-3 | #45B07A | #2A8A57 | HR zone 3 |
| zone-4 | #F2A33A | #C77A12 | HR zone 4 |
| zone-5 | #E5484D | #C8323A | HR zone 5 |

**Typography:**

- Numerals and display: Barlow Condensed, 600 to 700, tabular figures (race-number feel)
- UI and body: Public Sans, 400 to 600
- Timestamps and logs: IBM Plex Mono
- Scale: live numerals 96px, set targets 48px, headings 24px, body 16px, labels 12px uppercase with 0.08em tracking

**Layout and motion:** 8px grid, 16px side gutters, primary action anchored to the bottom. Rest timer is a full-width bar that drains. Motion only marks state changes (150ms set start and finish) and respects reduced motion. No shimmer skeletons.

**Key screens:**

1. Today: morning Whoop recovery, planned session, ease-off flag
2. Live Set: exercise, set X of Y, target reps and weight, one big log button
3. Rest: draining timer bar with next set preview
4. Session Summary: heart rate trace with each set shaded as a band
5. Ride Detail: power, heart rate, cadence, merged source badges
6. Trends: fitness, fatigue, and form chart, power curve, lift PRs
7. Gear: component mileage and battery reminders

**Signature element:** the Session Summary trace. Each set appears as a shaded band over the heart rate line, like a stage profile with climbs marked. It shows effort and recovery per set at a glance.

**Constraint:** iOS Safari blocks Web Bluetooth, so the PWA shows no live heart rate. Heart rate attaches to sets after Whoop syncs.

## Security

- OAuth tokens stored in macOS Keychain
- Service bound to localhost or Tailscale only

## Migration Off Strava

- One-time import of Strava bulk export archive into the same store
- Run in parallel with Strava until the data matches, then stop uploads

## Open Questions

- How to handWill this support strong mapping of my bike rides especially? le clock drift between the phone and Whoop when aligning sets
- Whether to add a lightweight web dashboard in v2
- How to classify strength sessions from Whoop for training load
