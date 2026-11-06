// The phone's clock against the Mac Mini's. Sets are timestamped by the
// phone; heart rate lives on true time. Every API response carries
// X-Server-Time, and the round trip brackets it, so
//   offset ≈ server − (sent + received) / 2
// to within half the round trip. Only quick round trips count, and the
// median of the last few readings rides out a slow one.

const KEY = 'domestique:clock-offset';
const MAX_RTT = 1500;
const KEEP = 7;

let readings: number[] = load();

function load(): number[] {
  try {
    const v = JSON.parse(localStorage.getItem(KEY) ?? '[]');
    return Array.isArray(v) ? v.filter((x) => typeof x === 'number' && Number.isFinite(x)).slice(-KEEP) : [];
  } catch {
    return [];
  }
}

/** Record one reading from a response; call with Date.now() before and after. */
export function observe(sent: number, received: number, header: string | null): void {
  const server = Number(header);
  if (!header || !Number.isFinite(server) || received - sent > MAX_RTT || received < sent) return;
  readings = [...readings, Math.round(server - (sent + received) / 2)].slice(-KEEP);
  try {
    localStorage.setItem(KEY, JSON.stringify(readings));
  } catch {
    /* storage blocked: keep it in memory */
  }
}

/** Server time minus phone time, in ms; 0 until measured. */
export function clockOffset(): number {
  if (!readings.length) return 0;
  const s = [...readings].sort((a, b) => a - b);
  return s[Math.floor(s.length / 2)]!;
}
