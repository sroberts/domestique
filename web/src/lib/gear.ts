// Gear: bikes, wearing parts and sensor batteries. Mirrors internal/gear.
import { deleteJSON, getJSON, postJSON, putJSON } from './api';
import { cachedGet } from './data';

export type GearStatus = 'ok' | 'soon' | 'due';
export type ComponentKind = 'chain' | 'tyres' | 'cassette' | 'brake_pads' | 'other';

export interface Bike {
  id: string;
  name: string;
  aliases: string[];
  default_ride: boolean;
  default_virtual: boolean;
  start_distance_m: number;
  retired: boolean;
}

export interface Component {
  id: string;
  bike_id: string;
  kind: ComponentKind;
  name: string;
  installed_at: number;
  removed_at: number | null;
  replace_at_m: number;
  start_distance_m: number;
  replaced_by: string | null;
}

export interface ComponentView extends Component {
  label: string;
  distance_m: number;
  moving_s: number;
  rides: number;
  wear: number | null;
  status: GearStatus;
}

export interface BikeView extends Bike {
  distance_m: number;
  moving_s: number;
  rides: number;
  status: GearStatus;
  components: ComponentView[];
  history: ComponentView[];
}

export interface Sensor {
  id: string;
  name: string;
  kind: 'hr_strap' | 'power_meter' | 'other';
  device_match: string;
  battery_at: number;
  battery_life_h: number;
  retired: boolean;
}

export interface SensorView extends Sensor {
  used_s: number;
  activities: number;
  wear: number | null;
  status: GearStatus;
}

export interface Reminder {
  kind: 'component' | 'sensor';
  id: string;
  bike_id?: string;
  status: GearStatus;
  text: string;
}

export interface GearView {
  bikes: BikeView[];
  sensors: SensorView[];
  reminders: Reminder[];
  unassigned: { rides: number; distance_m: number };
  unmatched_gear: { name: string; rides: number; distance_m: number }[];
  rides: number;
}

export interface ActivityBike {
  activity_id: string;
  bike: { id: string; name: string; retired: boolean } | null;
  how: 'manual' | 'strava' | 'default' | 'none';
  strava_gear?: string;
  bikes: { id: string; name: string; retired: boolean }[];
}

export const KIND_LABEL: Record<ComponentKind, string> = {
  chain: 'Chain',
  tyres: 'Tyres',
  cassette: 'Cassette',
  brake_pads: 'Brake pads',
  other: 'Other part',
};

/** Typical replacement distances, km: a starting point the athlete edits. */
export const KIND_LIFE_KM: Record<ComponentKind, number> = {
  chain: 3000,
  tyres: 4000,
  cassette: 10000,
  brake_pads: 2000,
  other: 0,
};

export const STATUS_LABEL: Record<GearStatus, string> = { ok: 'OK', soon: 'Soon', due: 'Due' };

export const fetchGear = () => cachedGet<GearView>('/v1/gear');
export const saveBike = (b: Bike) => putJSON(`/v1/gear/bikes/${encodeURIComponent(b.id)}`, b);
export const removeBike = (id: string) => deleteJSON(`/v1/gear/bikes/${encodeURIComponent(id)}`);
export const saveComponent = (c: Component, replaces?: string) =>
  putJSON(`/v1/gear/components/${encodeURIComponent(c.id)}`, { ...c, replaces: replaces ?? '' });
export const removeComponent = (id: string) => deleteJSON(`/v1/gear/components/${encodeURIComponent(id)}`);
export const saveSensor = (s: Sensor) => putJSON(`/v1/gear/sensors/${encodeURIComponent(s.id)}`, s);
export const removeSensor = (id: string) => deleteJSON(`/v1/gear/sensors/${encodeURIComponent(id)}`);

export const fetchActivityBike = (id: string) => getJSON<ActivityBike>(`/v1/activities/${encodeURIComponent(id)}/bike`);
/** bikeId null: this ride was on no bike of mine. undefined: back to automatic. */
export async function setActivityBike(id: string, bikeId: string | null | undefined): Promise<ActivityBike> {
  const path = `/v1/activities/${encodeURIComponent(id)}/bike`;
  if (bikeId === undefined) await deleteJSON(path);
  else await putJSON(path, { bike_id: bikeId });
  return fetchActivityBike(id);
}

/** A part the athlete wrote about fitting, read by Outpost's LLM: offered, never applied on its own. */
export interface GearSuggestion {
  id: string;
  activity_id: string;
  activity_name: string;
  at: number;
  bike_id: string;
  bike_name: string;
  kind: ComponentKind;
  name: string;
  quote: string;
  replaces?: string;
  replaces_name?: string;
  /** "New chain on the Tarmac, 3 Nov" */
  text: string;
}

export const fetchGearSuggestions = () => getJSON<{ ai: boolean; suggestions: GearSuggestion[] }>('/v1/gear/suggestions');
export const acceptGearSuggestion = (id: string) => postJSON(`/v1/gear/suggestions/${encodeURIComponent(id)}/accept`);
export const dismissGearSuggestion = (id: string) => postJSON(`/v1/gear/suggestions/${encodeURIComponent(id)}/dismiss`);

export function newID(prefix: string): string {
  const b = new Uint8Array(9);
  crypto.getRandomValues(b);
  return prefix + Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('');
}

/** "3,120". Whole kilometres from metres. */
export function kmWhole(m: number): string {
  return Math.round(m / 1000).toLocaleString('en-GB');
}

/** Local midnight of a YYYY-MM-DD date, as epoch ms. */
export function dateToMs(d: string): number {
  const [y, m, day] = d.split('-').map(Number);
  return new Date(y!, m! - 1, day!).getTime();
}

export function msToDate(ms: number): string {
  const d = new Date(ms);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
}
