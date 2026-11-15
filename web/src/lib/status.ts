// Status vocabulary: every state shows a color token, an icon, and a text label.
export type Tone = 'active' | 'attention' | 'ok' | 'danger' | 'warning' | 'neutral' | 'waiting' | 'accent';
export type IconName = 'run' | 'wait' | 'ok' | 'fail' | 'warn' | 'neu' | 'prompt' | 'fork';

const TONES: Record<Tone, string[]> = {
  active: ['running', 'in_progress', 'starting', 'validation'],
  attention: ['awaiting_approval', 'waiting_approval', 'awaiting_phase_review', 'in_review', 'pending'],
  ok: ['completed', 'done', 'approved', 'passed', 'connected', 'verified', 'supported'],
  danger: ['failed', 'rejected'],
  warning: ['blocked', 'interrupted', 'disconnected', 'stale', 'revision_required', 'changed', 'partial', 'broken', 'waiting'],
  waiting: ['waiting_for_instruction'],
  accent: ['fork'],
  neutral: ['queued', 'paused', 'backlog', 'planned', 'ready', 'ready_for_execution', 'cancelled', 'draft', 'not_connected', 'unknown', 'unsupported', 'unchanged', 'superseded'],
};
const ICON: Record<Tone, IconName> = { active: 'run', attention: 'wait', ok: 'ok', danger: 'fail', warning: 'warn', neutral: 'neu', waiting: 'prompt', accent: 'fork' };

const toneOf = new Map<string, Tone>();
for (const [tone, keys] of Object.entries(TONES) as Array<[Tone, string[]]>) for (const k of keys) toneOf.set(k, tone);

// Sentence case: "Awaiting approval", "Waiting for instruction".
export const label = (key: string) => {
  const t = key.split('_').join(' ');
  return t.charAt(0).toUpperCase() + t.slice(1);
};

export function status(key: string): { tone: Tone; icon: IconName; label: string } {
  const tone = toneOf.get(key) ?? 'neutral';
  return { tone, icon: ICON[tone], label: label(key) };
}
