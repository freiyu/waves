// A project's phases as chains of tasks, for the Overview, the project page and the Graph.
import type { ProjectDetail, TaskSummary } from '../api/models';

/** The state a task is drawn in: a running task whose harness waits for the human is shown as waiting. */
export const taskState = (t: Pick<TaskSummary, 'state' | 'execution_id'>, waiting: Map<string, string>) =>
  t.state === 'in_progress' && t.execution_id && waiting.has(t.execution_id) ? 'waiting_for_instruction' : t.state;

const NOTE: Record<string, string> = { awaiting_phase_review: 'Review', revision_required: 'Revision' };

export const chainOf = (p: ProjectDetail, waiting: Map<string, string>) =>
  p.phases.map((ph) => ({
    id: ph.id, code: ph.code, title: ph.title, note: NOTE[ph.state],
    tasks: p.phase_tasks.filter((t) => t.phase_id === ph.id).map((t) => ({ id: t.id, code: t.code, title: t.title, state: taskState(t, waiting) })),
  }));
