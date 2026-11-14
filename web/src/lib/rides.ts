// Planned rides: the words and numbers Today and the plan sheet show. No
// imports, so Node's test runner can load it.
import type { RideIntensity, RidePlan } from './types';

export const INTENSITIES: { k: RideIntensity; label: string; hint: string }[] = [
  { k: 'recovery', label: 'Recovery', hint: 'Zone 1, spin the legs' },
  { k: 'endurance', label: 'Endurance', hint: 'Zone 2, all day pace' },
  { k: 'tempo', label: 'Tempo', hint: 'Zone 3, or sweet spot' },
  { k: 'threshold', label: 'Threshold', hint: 'Efforts around FTP' },
  { k: 'vo2', label: 'VO2 max', hint: 'Short, hard efforts above FTP' },
];

export function intensityLabel(k: RideIntensity | ''): string {
  return INTENSITIES.find((i) => i.k === k)?.label ?? '';
}

/** "90 min", "2 h 30". */
export function minutesText(min: number): string {
  if (min < 60) return `${min} min`;
  const h = Math.floor(min / 60);
  const m = min % 60;
  return m ? `${h} h ${String(m).padStart(2, '0')}` : `${h} h`;
}

/** "Indoor · 90 min · Threshold · 95 TSS". */
export function rideMeta(p: Pick<RidePlan, 'kind' | 'duration_min' | 'intensity' | 'tss'>, withKind = true): string {
  const parts = [minutesText(p.duration_min)];
  if (withKind) parts.unshift(p.kind === 'indoor' ? 'Indoor' : 'Outdoor');
  if (p.intensity) parts.push(intensityLabel(p.intensity));
  if (p.tss > 0) parts.push(`${p.tss} TSS`);
  return parts.join(' · ');
}

/** The YYYY-MM-DD n days after date (local calendar days). */
export function addDays(date: string, n: number): string {
  const [y, m, d] = date.split('-').map(Number) as [number, number, number];
  const t = new Date(y, m - 1, d + n);
  return `${t.getFullYear()}-${String(t.getMonth() + 1).padStart(2, '0')}-${String(t.getDate()).padStart(2, '0')}`;
}

/** Noon on a YYYY-MM-DD, for formatting it as a day. */
export function dateMs(date: string): number {
  const [y, m, d] = date.split('-').map(Number) as [number, number, number];
  return new Date(y, m - 1, d, 12).getTime();
}

export interface RideForm {
  date: string;
  name: string;
  kind: 'indoor' | 'outdoor';
  minutes: string;
  intensity: RideIntensity | '';
  tss: string;
  notes: string;
}

/** The form's values as a plan, or what's wrong with them. */
export function formToPlan(f: RideForm, today: string): RidePlan | string {
  const name = f.name.trim().replace(/\s+/g, ' ');
  const minutes = Number(f.minutes.trim());
  const tss = f.tss.trim() === '' ? 0 : Number(f.tss.trim());
  if (!/^\d{4}-\d{2}-\d{2}$/.test(f.date)) return 'Pick a day.';
  if (f.date < today) return 'Pick today or a day to come.';
  if (!name) return 'Give the ride a name.';
  if (name.length > 80) return 'Keep the name under 80 characters.';
  if (!Number.isInteger(minutes) || minutes < 10 || minutes > 600) return 'Length is 10 to 600 minutes.';
  if (!Number.isInteger(tss) || tss < 0 || tss > 600) return 'Target TSS is a whole number up to 600, or blank.';
  return { date: f.date, name, kind: f.kind, duration_min: minutes, tss, intensity: f.intensity, notes: f.notes.trim() };
}
