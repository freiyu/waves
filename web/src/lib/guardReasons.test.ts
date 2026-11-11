import { describe, expect, it } from 'vitest';
import { GUARD_REASONS, reasonText } from './guardReasons';

// The codes the guard service records (internal/core/guard.go): each must read as words, not as a raw id.
const SERVICE_CODES = ['leased', 'lease_ended', 'unmanaged', 'outside_workspace', 'protected_path', 'read_only_task', 'no_target', 'bash_unchecked', 'waves_tool', 'not_a_file_tool', 'error', 'policy_off', 'not_a_project'];

describe('guard reasons', () => {
  it('has words for every code the service records', () => {
    for (const c of SERVICE_CODES) expect(GUARD_REASONS[c], c).toBeTruthy();
    for (const c of SERVICE_CODES) expect(reasonText(c)).not.toBe(c);
  });
  it('shows an unknown code as it is', () => {
    expect(reasonText('something_new')).toBe('something_new');
  });
});
