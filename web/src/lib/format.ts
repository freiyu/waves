const pad = (n: number) => String(n).padStart(2, '0');

export function duration(s: number | null | undefined): string {
  if (s == null) return 'none';
  return `${pad(Math.floor(s / 3600))}:${pad(Math.floor((s % 3600) / 60))}:${pad(s % 60)}`;
}

export function ago(iso: string | null | undefined, now = Date.now()): string {
  if (!iso) return 'none';
  const s = Math.max(0, Math.round((now - Date.parse(iso)) / 1000));
  if (s < 60) return 'just now';
  const m = Math.floor(s / 60);
  if (m < 60) return `${m} min ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h} h ago`;
  const d = Math.floor(h / 24);
  return d === 1 ? 'yesterday' : `${d} days ago`;
}

export const clock = (iso: string) => new Date(iso).toTimeString().slice(0, 8);
export const shortClock = (iso: string) => new Date(iso).toTimeString().slice(0, 5);
export const dayLabel = (d = new Date()) => d.toLocaleDateString('en-GB', { weekday: 'short', day: 'numeric', month: 'short', year: 'numeric' });
export const bytes = (n: number) => (n >= 1_000_000 ? `${(n / 1_000_000).toFixed(1)} MB` : n >= 1000 ? `${Math.round(n / 1000)} KB` : `${n} B`);
export const plural = (n: number, one: string, many = one + 's') => `${n} ${n === 1 ? one : many}`;

/** Parse a unified diff into classified lines for display. Text only, never HTML. */
export function diffLines(text: string): Array<{ kind: 'add' | 'del' | 'hdr' | 'ctx'; text: string }> {
  return text.split('\n').filter((l, i, a) => !(i === a.length - 1 && l === '')).map((text) => ({
    text,
    kind: /^(diff |index |--- |\+\+\+ |@@|new file|deleted file)/.test(text) ? 'hdr' : text.startsWith('+') ? 'add' : text.startsWith('-') ? 'del' : 'ctx',
  }));
}
