// The reason codes the guard records for a decision, in words. An unknown code is shown as it is.
export const GUARD_REASONS: Record<string, string> = {
  leased: "inside the session's lease",
  lease_ended: "the session's lease has ended",
  unmanaged: 'a session that never claimed a task',
  outside_workspace: 'outside the project folder',
  protected_path: 'a protected path',
  read_only_task: 'a read-only task',
  no_target: 'no target path',
  bash_unchecked: 'Bash calls are not checked',
  waves_tool: 'a Waves tool',
  not_a_file_tool: 'not a file tool',
  error: 'a hook error',
  policy_off: 'the guard is off',
  not_a_project: 'not a registered project',
};
export const reasonText = (code: string) => GUARD_REASONS[code] ?? code;
