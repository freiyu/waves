import { describe, expect, it } from 'vitest';
import { ago, diffLines, duration } from './format';
import { match } from './router.svelte';
import { status, label } from './status';

describe('format', () => {
  it('formats durations and ages', () => {
    expect(duration(423)).toBe('00:07:03');
    expect(duration(null)).toBe('none');
    const now = Date.parse('2026-11-05T12:00:00Z');
    expect(ago('2026-11-05T11:48:00Z', now)).toBe('12 min ago');
    expect(ago('2026-11-04T09:00:00Z', now)).toBe('yesterday');
  });
  it('classifies diff lines as text', () => {
    const l = diffLines('diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-old\n+new <b>x</b>\n ctx\n');
    expect(l.map((x) => x.kind)).toEqual(['hdr', 'hdr', 'hdr', 'hdr', 'del', 'add', 'ctx']);
    expect(l[5]!.text).toBe('+new <b>x</b>'); // kept as text; the UI never renders it as HTML
  });
});

describe('router', () => {
  it('matches routes and decodes parameters', () => {
    expect(match('#/')).toEqual({ name: 'overview', params: {} });
    expect(match('#/executions/exe_1/recovery')).toEqual({ name: 'recovery', params: { id: 'exe_1' } });
    expect(match('#/signin/abc%20d')).toEqual({ name: 'signin', params: { code: 'abc d' } });
    expect(match('#/projects?register')).toEqual({ name: 'projects', params: {} });
    expect(match('#/nope').name).toBe('not-found');
    expect(match('#/executions/%E0%A4%A').name).toBe('not-found'); // malformed escape does not throw
  });
});

describe('status vocabulary', () => {
  it('gives every state a tone, an icon and a label (never color alone)', () => {
    for (const k of ['running', 'awaiting_approval', 'completed', 'failed', 'blocked', 'queued', 'weird_new_state']) {
      const s = status(k);
      expect(s.icon).toBeTruthy();
      expect(s.label.length).toBeGreaterThan(2);
    }
    expect(status('failed').tone).toBe('danger');
    expect(status('weird_new_state').tone).toBe('neutral');
    expect(label('ready_for_execution')).toBe('Ready for Execution');
  });
});
