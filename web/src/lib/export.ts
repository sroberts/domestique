// Export links. Plain <a href download> to these: the browser downloads
// natively, and behind the Outpost gateway the session cookie rides along.
import { SOURCE_LABEL } from './format';
import type { ActivityDetail } from './types';

export interface ExportItem {
  href: string;
  label: string;
  hint: string;
}

const enc = encodeURIComponent;

export function activityExports(d: ActivityDetail): ExportItem[] {
  const id = enc(d.activity.id);
  const items: ExportItem[] = [];
  if ((d.stream.t?.length ?? 0) > 0) {
    items.push(
      { href: `/v1/activities/${id}/export?format=fit`, label: 'FIT file', hint: 'Merged from every recording' },
      { href: `/v1/activities/${id}/export?format=csv`, label: 'CSV', hint: 'One row per second' },
    );
  }
  for (const s of d.sources as (ActivityDetail['sources'][number] & { raw_sha256?: string | null })[]) {
    if (!s.raw_sha256) continue;
    const fmt = typeof s.extra?.format === 'string' ? s.extra.format.toUpperCase() : 'file';
    items.push({
      href: `/v1/sources/${enc(s.id)}/raw`,
      label: `Original from ${SOURCE_LABEL[s.source] ?? s.source}`,
      hint: `${fmt}, as imported`,
    });
  }
  return items;
}

export function sessionExports(sessionId: string, hasHR: boolean): ExportItem[] {
  const id = enc(sessionId);
  const items: ExportItem[] = [{ href: `/v1/sessions/${id}/export?format=csv`, label: 'Sets as CSV', hint: 'Reps, weight and times' }];
  if (hasHR) items.push({ href: `/v1/sessions/${id}/export?format=fit`, label: 'FIT file', hint: 'Heart rate, a lap per set' });
  return items;
}

export function rangeURL(from: string, to: string, files: 'generated' | 'originals'): string {
  return `/v1/export?from=${from}&to=${to}&files=${files}`;
}
