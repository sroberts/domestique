# Review flows

Walked by the web-app-review pipeline against a running Domestique (default
`http://127.0.0.1:8099`, phone viewport 390×844 unless noted). Data: the dev
history in the scratch storage dir, or any store with rides and strength
sessions. Each step lists its expected outcome.

## 1. Today briefing
1. Open `/`. → Wordmark, date, a readiness card (title + one-sentence detail), recovery score with band swatch, HRV and resting HR.
2. Scroll. → Training load: fitness, fatigue, form numbers; Mon–Sun bars with legend; planned session sheet with per-lift change notes; three latest activities.
3. → Primary "Start <plan>" button sits above the tab bar; no horizontal scroll.

## 2. Live strength session
1. From Today, tap "Start <plan>". → `/session/<id>`: exercise name, set ladder, plate "1 of N", target reps × weight, "Start set". Tab bar hidden.
2. Tap "Start set". → Plate ringed in signal colour, "In set" timer counting.
3. Tap "Finish set". → Rest screen: countdown, draining bar, "Logged" steppers prefilled at target, "Next" preview.
4. Tap "−" reps and "+" weight. → Values change; "Target was …" note; next targets carry the new weight.
5. Tap "End" → sheet with "Skip the rest of …", "Finish session now", "Keep going" (focused). Escape closes it.
6. Repeat start/finish until "Finish session"; tap it. → `/session/<id>/summary` with sets table; heart-rate trace or "arrives after Whoop syncs" state.

## 3. Activities list
1. Open `/activities`. → Filter chips (All, Rides, Strength, Other), weeks with totals, rows with name, meta line, source badges, TSS + NP/HR.
2. Tap "Rides". → Only rides and indoor rides.
3. Scroll to the end, tap "Show older" if present. → Older weeks append.

## 4. Ride detail
1. Open an outdoor ride from the list. → Name, meta, source badges, route map with start/end markers, stat grid, load note.
2. Move the pointer across "Over the ride". → Crosshair in every panel, readouts show values, a marker moves on the map.
3. → Power curve vs "Best before" with legend; time in power zones; time in HR zones; "Recorded by" list.
4. Open an indoor ride recorded by Zwift and Wahoo. → Two sources listed and a "Merged into one" note.

## 5. Trends
1. Open `/trends`. → Fitness/fatigue/form chart with legend and end labels; range control (3 months, 6 months, 1 year).
2. Tap "1 year". → Chart redraws with a longer range.
3. → Weekly load stacked bars with legend; power curve (90 days vs all time) with bests list; FTP by month; lifts with estimated 1RM.

## 6. Settings
1. Open `/settings`. → Connections (Whoop, Wahoo) with status and setup instructions; Import file picker; Thresholds form; heart-rate instructions; Appearance.
2. Enter an FTP override and Save. → "Saved. Every activity has been rescored." Clear it and Save again to restore detection.
3. Tap "Use light theme". → Whole app switches to the light theme; choice persists across reload.

## 7. Offline
1. Load `/` online, then go offline and reload. → App shell loads from the service worker; "No connection. Showing today as of …" notice; status reads "Offline".
2. Start a session and log a set offline. → Set is kept; status shows it waiting; going back online syncs it.
