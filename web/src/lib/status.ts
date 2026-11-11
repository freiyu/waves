// Status vocabulary: every state shows a color token, an icon, and a text label.
export type Tone = 'active' | 'attention' | 'ok' | 'danger' | 'warning' | 'neutral';
export type IconName = 'run' | 'wait' | 'ok' | 'fail' | 'warn' | 'neu';

const TONES: Record<Tone, string[]> = {
  active: ['running', 'in_progress', 'starting', 'validation'],
  attention: ['awaiting_approval', 'waiting_approval', 'awaiting_phase_review', 'in_review', 'pending'],
  ok: ['completed', 'done', 'approved', 'passed', 'connected', 'verified', 'supported'],
  danger: ['failed', 'rejected'],
  warning: ['blocked', 'interrupted', 'disconnected', 'stale', 'revision_required', 'changed', 'partial', 'broken', 'waiting'],
  neutral: ['queued', 'paused', 'backlog', 'planned', 'ready', 'ready_for_execution', 'cancelled', 'draft', 'not_connected', 'unknown', 'unsupported', 'unchanged', 'superseded'],
};
const ICON: Record<Tone, IconName> = { active: 'run', attention: 'wait', ok: 'ok', danger: 'fail', warning: 'warn', neutral: 'neu' };

const toneOf = new Map<string, Tone>();
for (const [tone, keys] of Object.entries(TONES) as Array<[Tone, string[]]>) for (const k of keys) toneOf.set(k, tone);

export const label = (key: string) =>
  key.split('_').map((w, i) => (i > 0 && w === 'for' ? w : w.charAt(0).toUpperCase() + w.slice(1))).join(' ');

export function status(key: string): { tone: Tone; icon: IconName; label: string } {
  const tone = toneOf.get(key) ?? 'neutral';
  return { tone, icon: ICON[tone], label: label(key) };
}
