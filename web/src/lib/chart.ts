/** Round tick values spanning [lo, hi], about `count` of them. */
export function ticks(lo: number, hi: number, count = 4): number[] {
  if (!(hi > lo)) return [lo];
  const raw = (hi - lo) / count;
  const mag = 10 ** Math.floor(Math.log10(raw));
  const step = [1, 2, 2.5, 5, 10].map((m) => m * mag).find((s) => s >= raw) ?? raw;
  const out: number[] = [];
  for (let v = Math.ceil(lo / step) * step; v <= hi + 1e-9; v += step) out.push(Math.round(v * 1000) / 1000);
  return out;
}

export function niceMax(v: number): number {
  if (v <= 0) return 1;
  const t = ticks(0, v, 4);
  const last = t.at(-1) ?? v;
  return last >= v ? last : last + (t[1] ?? v) - (t[0] ?? 0);
}

/** SVG path through values, breaking at gaps (null/negative). */
export function linePath(xs: number[], ys: (number | null)[]): string {
  let d = '';
  let pen = false;
  for (let i = 0; i < xs.length; i++) {
    const y = ys[i];
    if (y == null || Number.isNaN(y)) {
      pen = false;
      continue;
    }
    d += `${pen ? 'L' : 'M'}${xs[i]!.toFixed(1)},${y.toFixed(1)}`;
    pen = true;
  }
  return d;
}

/** Closed area under a line down to baseline y0, per contiguous run. */
export function areaPath(xs: number[], ys: (number | null)[], y0: number): string {
  let d = '';
  let run: [number, number][] = [];
  const flush = () => {
    if (run.length > 1) {
      d += `M${run[0]![0].toFixed(1)},${y0}` + run.map(([x, y]) => `L${x.toFixed(1)},${y.toFixed(1)}`).join('') + `L${run.at(-1)![0].toFixed(1)},${y0}Z`;
    }
    run = [];
  };
  for (let i = 0; i < xs.length; i++) {
    const y = ys[i];
    if (y == null || Number.isNaN(y)) flush();
    else run.push([xs[i]!, y]);
  }
  flush();
  return d;
}

/** Index of the nearest x. */
export function nearest(xs: number[], x: number): number {
  let lo = 0;
  let hi = xs.length - 1;
  while (hi - lo > 1) {
    const mid = (lo + hi) >> 1;
    if (xs[mid]! < x) lo = mid;
    else hi = mid;
  }
  return Math.abs(xs[lo]! - x) <= Math.abs(xs[hi]! - x) ? lo : hi;
}
