# Domestique

Self-hosted workout store for one athlete, supervised by Outpost. Imports rides
and recovery automatically (Wahoo, Whoop, watched FIT folders, a Strava archive),
merges duplicate recordings, scores everything on one training-load model, logs
strength sessions live from a PWA, and serves it all over REST (`/v1`) and MCP
(`/mcp`).

## Run

    make run                 # builds the PWA and the binary, serves on :8099 with demo data
    make test                # go vet + go test
    make release             # dist/domestique-darwin-arm64 + SHA256SUMS + outpost.toml

## Configuration (environment)

| Variable | Purpose |
|---|---|
| `OUTPOST_PORT` / `--port` | Loopback port |
| `OUTPOST_STORAGE_DIR` | All state: SQLite, raw files (`raw/`), OAuth tokens |
| `DOMESTIQUE_WHOOP_CLIENT_ID`, `DOMESTIQUE_WHOOP_CLIENT_SECRET_FILE` | Whoop developer app (scopes incl. `offline`) |
| `DOMESTIQUE_WAHOO_CLIENT_ID`, `DOMESTIQUE_WAHOO_CLIENT_SECRET_FILE` | Wahoo Cloud API app |
| `DOMESTIQUE_WATCH_DIRS` | `:`-separated folders polled for FIT/GPX/TCX files |
| `DOMESTIQUE_PUBLIC_URL` | The address Domestique is served at, e.g. `https://domestique.tail1234.ts.net`. Sets the OAuth redirect base (default: the address you browsed from) and, behind the gateway, pins the identity token's audience so another service on the machine can't replay its own tokens here. Set it. |
| `DOMESTIQUE_ALLOWED_HOSTS` | Comma-separated extra host names Domestique is reached at (a renamed node, a custom name). Behind the gateway they are accepted audiences; without it, requests for any other non-local name are refused (DNS-rebinding guard) |
| `DOMESTIQUE_TZ` | Athlete's time zone for days and weeks (default: system) |
| `DOMESTIQUE_MAX_HR` | Fallback max HR until one is detected (default 190) |
| `DOMESTIQUE_DEMO=1` | Stand-in Whoop data until Whoop is connected |
| `OUTPOST_AI_URL`, `OUTPOST_AI_TOKEN` | Set by Outpost when the operator has chosen models (service spec §15); see [Outpost's AI models](#outposts-ai-models) |

### Outpost's AI models

When Outpost provides models (`OUTPOST_AI_URL`, `OUTPOST_AI_TOKEN`, service
spec §15), Domestique uses them as an enhancement. Without them, or while a
model is down, every screen shows what it showed before. No model call runs
inside a request: a response carries `pending` while the work runs in the
background, the app checks again shortly, and results are kept in
`sync_state`. They're redone only when their facts (or the prompt) change,
and after a failure the same work waits 15 minutes.

**The LLM** writes, in a directeur sportif's voice:
- **Today's readiness call:** a rewording of the rule-based call. The rules
  still decide the level and the day's weights.
- **A note on each workout** (activity page), and **a debrief of each lifting
  session** (session summary), from their metrics and your own description.
- **A note on last week** (Trends, and the `get_week_note` MCP tool).
- **Tags and a lasting fact from your descriptions**, from a fixed list
  (`ill`, `new gear`, `sore`, `rain`…). Lines apps write themselves (Zwift's
  route and power-best lines, Whoop's strain sentence) are skipped.
- **A workout from text** in the editor ("5x5 squat at 100, 3x8 RDL 80").
  It fills in the draft for you to check, and saves nothing.
- **Answers about your own data** (Ask, linked from Today and Trends, and
  `POST /v1/ask`): "how has my threshold moved since June?" The LLM looks
  things up with the MCP server's read-only tools (today, activities,
  load, recovery, trends, gear, workouts, the week note), which run through
  the same handlers as the app; it's never offered a tool that changes
  anything. Up to 6 rounds, 150 seconds at most, and the last few turns are
  kept for follow-ups. This one runs inside the request, since there's
  nothing to show until it's done. Every number in the answer must appear in
  what the tools returned or in the conversation; otherwise the answer is
  retried once, then replaced with a plain "couldn't answer without
  guessing".

Prose is rejected, and nothing shown, unless it is the expected JSON, short
enough, free of `!`, and uses only numbers that appear in its facts, with no
more than a couple of figures beyond small counts.

**The System 1 model** answers typed questions, and Domestique changes data
only on a confident answer (≥ 0.85):
- the sport of a workout labelled "other";
- whether a ride with no GPS was indoors;
- whether two overlapping recordings that heart rate kept apart are one
  workout (they're then merged);
- whether a hand-typed Strava entry with no file is a stray. A stray is left
  out of training load, says so on its page, and "It was real: count it"
  restores it;
- how hard each lifting set was (easy, working, hard, limit), shown as a word
  beside the set.

A background sweep asks these about stored workouts after each load
recalculation, asking each question once per set of facts. The first sweep
over a long history takes a while on a local model: hundreds of descriptions
at tens of seconds each.

Zwift's "🗺️ Route in World" line is read into the activity's route without
any model. Earlier imports are backfilled once at startup.

### Secrets

The easy way: Settings → Connections → **Set up Whoop** (or Wahoo), paste the
app's client ID and secret, Save. Domestique writes them to
`OUTPOST_STORAGE_DIR/secrets/` (directory `700`, files `600`), applies them
without a restart, and never sends the secret back to the browser. Replace or
Remove from the same place; removing also disconnects. They travel with
backups of the storage directory, like the OAuth tokens.

Or name the files in the manifest, which takes precedence and locks the
Settings form. Client secrets are read only from files; the manifest is plaintext and its
`env` is visible in the process table, so a `DOMESTIQUE_*_CLIENT_SECRET` set
directly in the environment stops Domestique at start. On the Mac Mini:

    mkdir -p ~/.config/domestique && chmod 700 ~/.config/domestique
    (umask 077; pbpaste > ~/.config/domestique/whoop-secret)   # copy the secret first
    (umask 077; pbpaste > ~/.config/domestique/wahoo-secret)

Each file must be non-empty and readable only by you (mode `600`); a
group- or world-readable file is refused, as ssh refuses a loose key. Client
IDs aren't secret and go in the manifest. OAuth access and refresh tokens are
kept in the SQLite database under `OUTPOST_STORAGE_DIR` (mode `600`), are never
returned by the API, MCP or exports, and are dropped by Settings → Disconnect.

### Webhooks (optional)

Polling always runs (Whoop every 30 minutes, Wahoo every 15). Webhooks bring
new data within seconds; for Whoop they also pass deletions on, which polling
can't. Settings → Connections → Webhooks shows what to enter for each.

- **Whoop**: in the developer dashboard add
  `<your Domestique address>/v1/webhooks/whoop`, model version **v2**.
  Requests must carry a valid `X-WHOOP-Signature` (HMAC-SHA256 of timestamp +
  body with the client secret, timestamp within 2 hours). Updates trigger a
  sync; a deleted workout, sleep or recovery is removed here too.
- **Wahoo**: in the developer portal turn webhooks on with
  `<your Domestique address>/v1/webhooks/wahoo` and the webhook token Settings
  shows (Domestique makes one on first run, kept in
  `OUTPOST_STORAGE_DIR/secrets/wahoo-webhook-token`; Settings can replace it).
  Wahoo signs nothing, so a matching token only triggers a Wahoo sync over the
  authenticated API; the FIT URL in the payload is never fetched.
- `outpost.toml` lists exactly those two paths in `public_paths`, so the
  gateway lets the providers' unauthenticated POSTs through; everything else
  under `/v1/` still needs a login. Bursts are batched into one sync after 10
  seconds and redeliveries are dropped.
- Both call from the public internet. A tailnet-only address (`*.ts.net`
  without Funnel) can't receive them; Outpost doesn't offer Funnel yet, so this
  needs the gateway reachable at a public hostname. Without that, leave the
  webhook URLs unset and polling carries on alone.

OAuth redirect URIs to register: `<your Domestique address>/v1/oauth/whoop/callback`
and `/v1/oauth/wahoo/callback`. Connect from Settings in the app.

## Importing history

    OUTPOST_STORAGE_DIR=~/.outpost/data/domestique domestique import ~/Downloads/export_123.zip

Works while the service runs. Also accepts single files and folders. Settings →
Import does the same from a browser.

## Heart rate for strength sets

Whoop's API has no heart-rate time series. Post samples to `POST /v1/hr`
(`{"source":"health","samples":[{"t":<ms or ISO time>,"bpm":123}]}`), e.g. from
an iOS Shortcut reading Apple Health, and sessions in that window are re-analysed.

### Clock alignment

Sets are timestamped by the phone; heart rate carries the strap's or watch's
clock. Two corrections line them up before a session is analysed:

1. **Phone clock.** Every `/v1` response carries `X-Server-Time` (ms). The PWA
   brackets it with the request's round trip, keeps the median of recent quick
   readings, and sends it as `clock_offset_ms` (server minus phone) with each
   session upload. The session's activity is placed on true time with it.
2. **Heart rate source.** The summary predicts the heart rate curve the logged
   sets should produce and finds the shift (within ±2 minutes) where the recorded
   curve fits it best. It applies the shift only when the fit is strong and
   clearly beats any other shift; shifts under 5 s are left alone. The result is
   in the summary's `alignment` field (`source`: `auto`, `manual` or `none`).
   Override it with `PUT /v1/sessions/{id}/hr-shift` and `{"shift_ms": 12000}`
   (positive moves heart rate earlier), or `{"shift_ms": null}` to go back to
   automatic. The Summary screen's "Adjust" control does the same.
