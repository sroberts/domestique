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
