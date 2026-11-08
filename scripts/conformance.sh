#!/usr/bin/env bash
# Outpost conformance: outpost-service-spec.md §13 checks 1-8, the §11 gateway
# rules a probe can see (no cookies, nothing under /.outpost/), and the log
# rules (every stdout line a JSON object, no ANSI, nothing on stderr).
# Runs the real binary with only the environment Outpost would give it.
# Usage: bash scripts/conformance.sh   (bash 3.2 compatible; needs curl, lsof, jq, perl)
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d "${TMPDIR:-/tmp}/domestique-conformance.XXXXXX")
bin="$work/domestique"
out="$work/stdout.log"
err="$work/stderr.log"
pass=0
fail=0
live=""

cleanup() {
	[ -n "$live" ] && kill -9 $live 2>/dev/null || true
	rm -rf "$work"
}
trap cleanup EXIT

ok() { pass=$((pass + 1)); echo "ok   $*"; }
bad() { fail=$((fail + 1)); echo "FAIL $*"; }
now() { perl -MTime::HiRes=time -e 'printf "%.3f\n", time'; }
since() { awk -v a="$(now)" -v b="$1" 'BEGIN { printf "%.3f", a - b }'; }
lt() { awk -v a="$1" -v b="$2" 'BEGIN { exit !(a < b) }'; }

# Build before HOME and TMPDIR move, so the Go caches stay where they are. The
# PWA isn't needed: the binary embeds whatever internal/webui/dist holds.
(cd "$root" && CGO_ENABLED=0 go build -trimpath -o "$bin" .)

export HOME="$work/home" TMPDIR="$work/tmp"
mkdir -p "$HOME" "$TMPDIR"
storage="$work/storage"
mkdir -m 700 "$storage"
: >"$out"
: >"$err"

while :; do
	port=$((20000 + RANDOM % 30000))
	lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1 || break
done
base="http://127.0.0.1:$port"

# bounded SECS VAR=VAL... -- ARGS... runs the binary with only Outpost's
# environment plus VAR=VALs, under a hard timeout, and prints its exit code.
bounded() {
	local secs=$1 p w rc
	shift
	local envs=()
	while [ "$1" != "--" ]; do
		envs+=("$1")
		shift
	done
	shift
	env -i PATH="$PATH" HOME="$HOME" TMPDIR="$TMPDIR" ${envs[@]+"${envs[@]}"} "$bin" "$@" >>"$out" 2>>"$err" &
	p=$!
	(sleep "$secs" && kill -9 "$p") >/dev/null 2>&1 &
	w=$!
	set +e
	wait "$p"
	rc=$?
	set -e
	kill "$w" 2>/dev/null || true
	wait "$w" 2>/dev/null || true
	echo "$rc"
}

probe() { curl -s -o /dev/null -w '%{http_code}' --max-time 2 "$base/up" || true; }

# start launches the service on $storage and sets $pid and $t0.
start() {
	t0=$(now)
	env -i PATH="$PATH" HOME="$HOME" TMPDIR="$TMPDIR" OUTPOST_STORAGE_DIR="$storage" \
		"$bin" --port "$port" >>"$out" 2>>"$err" &
	pid=$!
	live="$pid"
}

# healthy waits up to 5 s for /up to answer 200 and prints the last code.
healthy() {
	local code
	code=$(probe)
	while [ "$code" != "200" ] && lt "$(since "$t0")" 5; do
		sleep 0.02
		code=$(probe)
	done
	echo "$code"
}

# stop sends SIGTERM and sets $rc and $took. Not called in $(...): a
# subshell can't wait on the server, which is this shell's child.
stop() {
	local ts
	ts=$(now)
	kill -TERM "$pid"
	set +e
	wait "$pid"
	rc=$?
	set -e
	took=$(since "$ts")
	live=""
}

echo "domestique conformance: port $port, work $work"

# --- first start ----------------------------------------------------------
start

# 3. The first answer is not-ready or ready, never a connection error.
first="000"
while [ "$first" = "000" ] && lt "$(since "$t0")" 2; do
	first=$(probe)
done
case "$first" in
503 | 200) ok "3 first /up answer is $first" ;;
*) bad "3 first /up answer is $first, want 503 or 200" ;;
esac

# 2. Healthy promptly, then 100 probes, each 200 in under 50 ms.
code=$(healthy)
if [ "$code" = "200" ]; then ok "2 /up 200 after $(since "$t0")s"; else bad "2 /up not 200 within 5s (last $code)"; fi
slow=0
worst=0
for _ in $(seq 100); do
	r=$(curl -s -o /dev/null -w '%{http_code} %{time_total}' --max-time 2 "$base/up" || echo "000 9")
	c=${r% *}
	s=${r#* }
	if [ "$c" != "200" ] || ! lt "$s" 0.050; then slow=$((slow + 1)); fi
	if lt "$worst" "$s"; then worst=$s; fi
done
if [ "$slow" -eq 0 ]; then ok "2 100 probes all 200 under 50ms (worst ${worst}s)"; else bad "2 $slow/100 probes failed or took >=50ms (worst ${worst}s)"; fi

# 1. Loopback only: every listening socket of this PID is 127.0.0.1:$port.
names=$(lsof -nP -a -p "$pid" -iTCP -sTCP:LISTEN -Fn 2>/dev/null | sed -n 's/^n//p' || true)
if [ "$names" = "127.0.0.1:$port" ]; then ok "1 listens on 127.0.0.1:$port only"; else bad "1 listening sockets: $(echo "$names" | tr '\n' ' ')"; fi

# 8. Does not daemonize: the PID we started is still the server after 2 s.
sleep "$(awk -v e="$(since "$t0")" 'BEGIN { r = 2.1 - e; print (r > 0) ? r : 0 }')"
if kill -0 "$pid" 2>/dev/null; then ok "8 PID $pid still running after 2s"; else bad "8 PID $pid is gone"; fi

# Gateway (§11): no cookie on any page or API answer, and nothing served
# under /.outpost/, which belongs to the login gateway.
cookie=""
for path in / /workouts /settings /v1/templates; do
	if curl -s -D - -o /dev/null --max-time 2 "$base$path" | grep -qi '^set-cookie:'; then cookie="$cookie $path"; fi
done
if [ -z "$cookie" ]; then ok "gw no page sets a cookie"; else bad "gw cookies set on:$cookie"; fi
for path in /.outpost/logout /.outpost/login; do
	code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 "$base$path" || true)
	if [ "$code" = "404" ]; then ok "gw $path is not served"; else bad "gw $path answered $code, want 404"; fi
done

# 5. A second instance on the same port can't listen: it must exit non-zero, promptly.
rc=$(bounded 5 OUTPOST_STORAGE_DIR="$work/second" -- --port "$port")
if [ "$rc" -ne 0 ] && [ "$rc" -ne 137 ]; then ok "5 port in use exits $rc"; else bad "5 port in use exits $rc"; fi

# 4. SIGTERM: exit 0 well inside 10 s, with the database checkpointed.
stop
if [ "$rc" -eq 0 ] && lt "$took" 10; then ok "4 SIGTERM: exit 0 in ${took}s"; else bad "4 SIGTERM: exit $rc in ${took}s"; fi
if ls "$storage"/db/*-wal >/dev/null 2>&1 && [ -s "$(ls "$storage"/db/*-wal | head -1)" ]; then bad "4 non-empty WAL left behind"; else ok "4 no WAL left to replay"; fi

# 5. Unrecoverable configuration exits non-zero, promptly.
rc=$(bounded 5 -- --port "$port")
if [ "$rc" -ne 0 ] && [ "$rc" -ne 137 ]; then ok "5 no OUTPOST_STORAGE_DIR exits $rc"; else bad "5 no OUTPOST_STORAGE_DIR exits $rc"; fi
rc=$(bounded 5 OUTPOST_STORAGE_DIR="$work/x" --)
if [ "$rc" -ne 0 ] && [ "$rc" -ne 137 ]; then ok "5 no port exits $rc"; else bad "5 no port exits $rc"; fi
rc=$(bounded 5 OUTPOST_STORAGE_DIR="$work/x" DOMESTIQUE_TZ=Mars/Olympus_Mons -- --port "$port")
if [ "$rc" -ne 0 ] && [ "$rc" -ne 137 ]; then ok "5 bad DOMESTIQUE_TZ exits $rc"; else bad "5 bad DOMESTIQUE_TZ exits $rc"; fi
rc=$(bounded 5 OUTPOST_STORAGE_DIR="$work/x" DOMESTIQUE_MAX_HR=400 -- --port "$port")
if [ "$rc" -ne 0 ] && [ "$rc" -ne 137 ]; then ok "5 bad DOMESTIQUE_MAX_HR exits $rc"; else bad "5 bad DOMESTIQUE_MAX_HR exits $rc"; fi

# Restarts are routine: the same storage starts again, healthy.
start
code=$(healthy)
if [ "$code" = "200" ]; then ok "restart on existing storage is healthy"; else bad "restart on existing storage: /up $code"; fi
stop
[ "$rc" -eq 0 ] || bad "restart: SIGTERM exit $rc"

# 7. Survives its storage being replaced, as a restore would.
rm -rf "$storage" && mkdir -m 700 "$storage"
start
code=$(healthy)
if [ "$code" = "200" ]; then ok "7 starts clean on replaced storage"; else bad "7 replaced storage: /up $code"; fi
stop
[ "$rc" -eq 0 ] || bad "7: SIGTERM exit $rc"

# 6. Writes nothing outside its storage directory: HOME and TMPDIR stay empty.
stray=$(find "$HOME" "$TMPDIR" -mindepth 1 2>/dev/null | head -5)
if [ -z "$stray" ]; then ok "6 nothing written to HOME or TMPDIR"; else bad "6 wrote outside storage: $(echo "$stray" | tr '\n' ' ')"; fi

# Logs: one JSON object per line on stdout, no ANSI, nothing on stderr.
lines=0
badlines=0
while IFS= read -r line; do
	lines=$((lines + 1))
	printf '%s\n' "$line" | jq -e 'type == "object"' >/dev/null 2>&1 || {
		badlines=$((badlines + 1))
		echo "  not JSON: $line"
	}
done <"$out"
if [ "$lines" -gt 0 ] && [ "$badlines" -eq 0 ]; then ok "logs: $lines stdout lines, all JSON objects"; else bad "logs: $badlines/$lines stdout lines are not JSON objects"; fi
if grep -q "$(printf '\033')" "$out" "$err"; then bad "logs: ANSI escape found"; else ok "logs: no ANSI escapes"; fi
if [ -s "$err" ]; then bad "logs: stderr not empty: $(head -3 "$err")"; else ok "logs: stderr empty"; fi

echo "conformance: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
