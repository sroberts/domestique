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

### Whoop webhooks (optional)

Polling every 30 minutes always runs. Webhooks bring new recovery, sleep and
workouts within seconds and pass deletions on, which polling can't. In the
Whoop developer dashboard, add the webhook URL
`<your Domestique address>/v1/webhooks/whoop` with model version **v2**.

- `outpost.toml` lists that one path in `public_paths`, so the gateway lets
  Whoop's unauthenticated POST through. Domestique accepts it only with a valid
  `X-WHOOP-Signature` (HMAC-SHA256 of timestamp + body with the client secret,
  timestamp within 2 hours); everything else under `/v1/` still needs a login.
- Whoop calls from the public internet. A tailnet-only address
  (`*.ts.net` without Funnel) can't receive webhooks; Outpost doesn't offer
  Funnel yet, so this needs the gateway reachable at a public hostname.
  Without that, leave the URL unset and polling carries on alone.
- Updates trigger a sync (bursts are batched over 10 seconds); a deleted
  workout, sleep or recovery is removed here too. Redeliveries are dropped by
  `trace_id`. Settings shows when the last webhook arrived.

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
