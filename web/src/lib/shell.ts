import { getContext } from 'svelte';
import type { Project, Status } from '../api/models';
import type { Res } from './res';

export interface ShellData {
  status: Res<Status>;
  projects: Res<{ items: Project[] }>;
}
export const SHELL = Symbol('shell');
export const shellData = () => getContext<ShellData>(SHELL);
