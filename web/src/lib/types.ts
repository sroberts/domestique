// Mirrors internal/store and internal/strength on the Go side.

export interface TargetSet {
  reps: number;
  weight_kg: number;
}

export interface Exercise {
  name: string;
  sets: TargetSet[];
  /** Rest after each set; in a circuit, rest after this exercise (the last one's is the rest between rounds). */
  rest_seconds: number;
  notes?: string;
  change?: string;
  /** Adjacent exercises sharing a key form a circuit: one set of each in turn, a round per set. */
  circuit?: string;
}

export interface Template {
  id: string;
  name: string;
  exercises: Exercise[];
  updated_at: number;
}

export interface Recovery {
  date: string;
  score: number;
  hrv_ms: number;
  resting_hr: number;
  source: string;
}

export interface Session {
  id: string;
  template_id: string;
  name: string;
  plan: Exercise[];
  started_at: number;
  finished_at: number | null;
  strain?: number | null;
  updated_at: number;
  /** How much of the plan was lifted. Server-side lists only. */
  progress?: Progress;
  /** Server clock minus the phone's, sent with each upload. */
  clock_offset_ms?: number;
  /** Manual heart rate alignment; null means automatic. */
  hr_shift_ms?: number | null;
}

export interface Alignment {
  clock_offset_ms: number;
  /** How far heart rate was moved earlier to meet the sets, in ms. */
  hr_shift_ms: number;
  source: 'auto' | 'manual' | 'none';
  confidence: number;
}

export interface Progress {
  /** Sets really lifted; taps under 5 s (or 2 s a rep) don't count. */
  done: number;
  logged: number;
  planned: number;
}

export type Phase = 'ready' | 'active' | 'rest' | 'last';

/** Where the athlete is in a session. Lives on the device only. */
export interface Cursor {
  /** Index into the flattened list of planned sets. */
  index: number;
  phase: Phase;
  /** Epoch ms when the current phase began. */
  phase_started_at: number;
  /** Rest target for the current rest, in seconds. */
  rest_s: number;
  /** Weight carried forward per exercise after an adjustment. */
  weight_override: Record<number, number>;
  /** Indices of planned sets that were skipped. */
  skipped: number[];
  /** The set just logged, editable during rest. */
  last_set_id: string | null;
}

export interface LocalSession extends Session {
  cursor: Cursor;
}

export interface SetRecord {
  id: string;
  session_id: string;
  exercise_index: number;
  exercise_name: string;
  set_index: number;
  target_reps: number;
  target_weight_kg: number;
  reps: number;
  weight_kg: number;
  started_at: number;
  ended_at: number;
  updated_at: number;
}

export interface HRSample {
  t: number;
  bpm: number;
}

export interface SetStat {
  set_id: string;
  has_hr: boolean;
  peak_bpm: number;
  avg_bpm: number;
  zone: number;
  recovery_drop: number;
  recovery_window_s: number;
  rest_s: number;
}

export interface Summary {
  session: Session;
  sets: SetRecord[];
  hr: HRSample[];
  hr_status: 'ready' | 'pending' | 'none';
  max_hr: number;
  zones: [number, number, number, number, number];
  duration_s: number;
  volume_kg: number;
  total_reps: number;
  peak_bpm: number;
  avg_bpm: number;
  zone_seconds: [number, number, number, number, number, number];
  set_stats: SetStat[];
  progress: Progress;
  alignment?: Alignment;
  /** System 1's reading of how hard each set was, by set id (Outpost AI). */
  efforts?: Record<string, { level: EffortLevel; score: number }>;
  efforts_pending?: boolean;
  /** The LLM's debrief of the session (Outpost AI). */
  note?: AINote;
  /** Other finished sessions the same day, which this one can be merged with. */
  same_day?: SessionRef[];
  /** One of same_day that looks like this session's first part. */
  suggest_merge?: string;
}

export interface SessionRef {
  id: string;
  name: string;
  started_at: number;
  finished_at: number;
  sets: number;
}

/** POST /v1/sessions/{id}/merge?dry_run=1: what a merge would make. */
export interface MergePreview {
  /** The session kept (the earlier one), and the one merged into it. */
  id: string;
  merged: string;
  name: string;
  plan: Exercise[];
  sets: number;
  started_at: number;
  finished_at: number;
}

/** One session merged into another (GET /v1/sessions/merged). */
export interface Merge {
  from: string;
  into: string;
  merged_at: number;
}

export type EffortLevel = 'easy' | 'working' | 'hard' | 'limit';

/** Prose from Outpost's LLM, or the promise of it. */
export interface AINote {
  text?: string;
  pending?: boolean;
}

export interface Today {
  date: string;
  recovery: Recovery | null;
  sleep: SleepRecord | null;
  plan: Template | null;
  template: Template | null;
  readiness: Readiness;
  load: LoadDay | null;
  ramp_ctl: number;
  week: LoadDay[] | null;
  recent: Session[];
  latest: ActivityRow[];
  athlete: Athlete;
  /** An easier session the LLM proposes on a hold-back day. */
  ease?: EaseView;
  /** The plan is the eased one the athlete took. */
  eased?: boolean;
  /** System 1 thinks the last mornings look like an illness coming on. */
  run_down?: boolean;
}

export interface EaseView {
  reason: string;
  exercises: Exercise[];
  model?: string;
}

// ---- Activities, load, trends (v1/activities, v1/load, v1/trends) ----

export type Sport = 'ride' | 'virtual_ride' | 'run' | 'walk' | 'hike' | 'swim' | 'strength' | 'other';

export interface Badge {
  source: string;
  has_power: boolean;
  has_hr: boolean;
  has_gps: boolean;
}

export interface Activity {
  id: string;
  sport: Sport;
  name: string;
  start_at: number;
  elapsed_s: number;
  moving_s: number;
  distance_m: number;
  route: string;
  strain: number | null;
  session_id: string | null;
  ftp: number;
  tss: number;
  intensity: number;
  load_method: 'power' | 'hr' | 'strain' | 'none';
}

export interface ActivityRow extends Activity {
  sources: Badge[];
  summary: { avg_power: number; np: number; avg_hr: number; max_hr: number; ascent_m: number; kj: number };
  progress?: Progress;
}

export interface Metrics {
  avg_power: number;
  np: number;
  max_power: number;
  kj: number;
  avg_hr: number;
  max_hr: number;
  avg_cadence: number;
  ascent_m: number;
  power_curve: number[];
  power_seconds: number;
}

export interface SourceRecord {
  id: string;
  source: string;
  external_id: string;
  sport: Sport;
  name: string;
  start_at: number;
  elapsed_s: number;
  distance_m: number;
  device: string;
  has_power: boolean;
  has_hr: boolean;
  has_gps: boolean;
  extra: Record<string, unknown>;
}

export interface ChartStream {
  t: number[];
  power?: number[];
  hr?: number[];
  cadence?: number[];
  speed?: number[];
  altitude?: number[];
  distance?: number[];
  route?: [number, number][];
  /** The Zwift world the route is in; real = it sits on the place it copies. */
  world?: ZwiftWorld;
}

export interface ZwiftWorld {
  name: string;
  slug: string;
  real: boolean;
  /** [lat, lng] of Zwift's map image's top-left, top-right, bottom-right and
   * bottom-left corners; absent when there's no image that lines up. */
  map_corners?: [[number, number], [number, number], [number, number], [number, number]];
  /** Colour at the image's edges, to fill the map beyond it. */
  background?: string;
}

export interface ActivityDetail {
  activity: Activity;
  metrics: Metrics;
  sources: SourceRecord[];
  channels: Record<string, string>;
  stream: ChartStream;
  power_zones: number[];
  hr_zones: number[];
  hr_zones_from?: 'whoop';
  max_hr: number;
  hr_zone_bounds?: number[];
  curve_durations: number[];
  best_curve: number[] | null;
  strength?: Summary;
  /** Each recording's own numbers, when more than one recorded this. */
  versions?: Version[];
  /** The source shown alone, or absent for the merged view. */
  view?: string;
  /** From Outpost's models, merged view only. */
  note?: AINote;
  tags?: string[];
  tag_note?: string;
  /** System 1 judged this a stray entry; it carries no load until kept. */
  stray?: boolean;
  /** Channels System 1 judged broken and left out ("hr", "power"), with why. */
  bad_data?: Record<string, string>;
  /** What kind of ride: endurance, tempo, intervals, race, commute, recovery. */
  ride_type?: string;
  /** A name the LLM suggests for one still called "Afternoon Ride". */
  name_suggestion?: AINote;
}

export interface WeekNote {
  week: string;
  ai: boolean;
  note: AINote | null;
  /** What the athlete kept writing about: "knee, 4 times in 3 weeks". */
  keeps_coming_up?: string[];
}

/** POST /v1/ask: an answer from Outpost's LLM, looked up in the athlete's data. */
export interface AskResult {
  answer: string;
  model?: string;
  /** The read-only tools it called, e.g. "get_training_load". */
  tools: string[];
  /** False when the model's answer failed the number check and answer is the fallback. */
  checked: boolean;
}

/** One recording's own account of a workout, before merging. */
export interface Version {
  id: string;
  source: string;
  device: string;
  file: boolean;
  start_at: number;
  elapsed_s: number;
  moving_s: number;
  distance_m: number;
  avg_hr: number;
  max_hr: number;
  avg_power: number;
  np: number;
  avg_cadence: number;
  strain: number | null;
  tss: number;
  load_method: Activity['load_method'];
  has_power: boolean;
  has_hr: boolean;
  has_gps: boolean;
  sets?: number;
  volume_kg?: number;
}

export interface Athlete {
  ftp: number;
  ftp_source: 'override' | 'detected' | 'none';
  max_hr: number;
  max_hr_source: string;
  rest_hr: number;
  lthr: number;
}

export interface LoadDay {
  date: string;
  tss: number;
  ride_tss: number;
  strength_tss: number;
  other_tss: number;
  ctl: number;
  atl: number;
  tsb: number;
  hours: number;
}

export interface Readiness {
  level: 'push' | 'normal' | 'hold' | 'easy';
  title: string;
  detail: string;
  /** "llm" when the operator's model wrote title and detail; the level is always the rules'. */
  source?: 'llm';
  /** The model is writing today's call now; ask again shortly. */
  pending?: boolean;
}

export interface SleepRecord {
  date: string;
  start_at: number;
  end_at: number;
  asleep_s: number;
  performance: number | null;
}

export interface CurvePoint {
  duration: number;
  watts: number;
  activity_id?: string;
  at?: number;
}

export interface Week {
  start: string;
  ride_tss: number;
  strength_tss: number;
  other_tss: number;
  hours: number;
  ride_km: number;
  count: number;
}

export interface LiftPR {
  exercise: string;
  best_e1rm: number;
  best_set: string;
  best_at: number;
  latest_e1rm: number;
  latest_at: number;
  sessions: number;
  history: [number, number][];
}

export interface Trends {
  curve_all_time: CurvePoint[];
  curve_90d: CurvePoint[];
  weeks: Week[];
  ftp_history: { month: string; ftp: number }[];
  lifts: LiftPR[];
  athlete: Athlete;
}

export interface ProviderStatus {
  provider: 'whoop' | 'wahoo';
  configured: boolean;
  connected: boolean;
  last_sync: number;
  /** When the provider last sent a verified webhook (Whoop only). */
  last_webhook?: number;
  last_error?: string;
  syncing: boolean;
  /** Where the app credentials came from; the secret itself is never sent. */
  credentials_from: '' | 'manifest' | 'settings';
  client_id?: string;
  /** Token to give Wahoo for its webhooks. */
  webhook_token?: string;
}

/** A finished workout read from text, for the athlete to check before saving. */
export interface LoggedDraft {
  name: string;
  started_at: number;
  minutes: number;
  exercises: Exercise[];
  /** What the text mentioned that isn't lifting; never saved. */
  not_logged?: string[];
}

/** An exercise name counted as another, from System 1. */
export interface ExerciseAlias {
  name: string;
  to: string;
  p?: number;
  at: number;
}
