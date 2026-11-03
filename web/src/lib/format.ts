export function weight(kg: number): string {
  if (kg === 0) return 'BW';
  return Number.isInteger(kg) ? String(kg) : kg.toFixed(1);
}

/** "6 × 100 kg", or "6 × BW" for bodyweight. */
export function setText(reps: number, kg: number): string {
  return kg === 0 ? `${reps} × BW` : `${reps} × ${weight(kg)} kg`;
}

/** m:ss, or h:mm:ss past an hour. */
export function clock(totalSeconds: number): string {
  const s = Math.max(0, Math.floor(totalSeconds));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = String(s % 60).padStart(2, '0');
  return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${sec}` : `${m}:${sec}`;
}

/** "46 min", "1 h 12 min". */
export function duration(totalSeconds: number): string {
  const min = Math.round(totalSeconds / 60);
  if (min < 60) return `${min} min`;
  return `${Math.floor(min / 60)} h ${min % 60} min`;
}

const dayFmt = new Intl.DateTimeFormat('en-GB', { weekday: 'short', day: 'numeric', month: 'short' });
const timeFmt = new Intl.DateTimeFormat('en-GB', { hour: '2-digit', minute: '2-digit', hour12: false });

/** "Tue 3 Nov". */
export function day(ms: number | Date): string {
  return dayFmt.format(ms).replace(',', '');
}

/** "06:12". */
export function time(ms: number): string {
  return timeFmt.format(ms);
}

export function thousands(n: number): string {
  return Math.round(n).toLocaleString('en-GB');
}

export type RecoveryBand = 'green' | 'yellow' | 'red';

/** Whoop's bands: green 67+, yellow 34-66, red 0-33. */
export function recoveryBand(score: number): RecoveryBand {
  if (score >= 67) return 'green';
  if (score >= 34) return 'yellow';
  return 'red';
}

export function km(m: number): string {
  const v = m / 1000;
  return v >= 100 ? v.toFixed(0) : v.toFixed(1);
}

export const SPORT_LABEL: Record<string, string> = {
  ride: 'Ride',
  virtual_ride: 'Indoor ride',
  run: 'Run',
  walk: 'Walk',
  hike: 'Hike',
  swim: 'Swim',
  strength: 'Strength',
  other: 'Workout',
};

export const SOURCE_LABEL: Record<string, string> = {
  wahoo: 'Wahoo',
  zwift: 'Zwift',
  whoop: 'Whoop',
  garmin: 'Garmin',
  strava: 'Strava',
  app: 'Domestique',
  upload: 'Upload',
  watch: 'Folder',
  device: 'Device',
};

export type SportGroup = 'ride' | 'strength' | 'other';
export function sportGroup(s: string): SportGroup {
  if (s === 'ride' || s === 'virtual_ride') return 'ride';
  if (s === 'strength') return 'strength';
  return 'other';
}

/** "1:05", "45 s" for power-curve durations. */
export function dur(s: number): string {
  if (s < 60) return `${s} s`;
  if (s < 3600) return s % 60 === 0 ? `${s / 60} min` : clock(s);
  return s % 3600 === 0 ? `${s / 3600} h` : `${Math.floor(s / 3600)} h ${Math.round((s % 3600) / 60)}`;
}

/** Relative day: "Today", "Yesterday", "Sat 31 Oct". */
export function relDay(ms: number): string {
  const d = new Date(ms);
  const t = new Date();
  const days = Math.round((new Date(t.getFullYear(), t.getMonth(), t.getDate()).getTime() -
    new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime()) / 86400000);
  if (days === 0) return 'Today';
  if (days === 1) return 'Yesterday';
  return day(ms);
}
