import { describe, expect, it } from 'vitest';
import { ago, diffLines, duration } from './format';
import { match } from './router.svelte';
import { status, label } from './status';
import { span, groupOf, inLastHour } from './timeline';

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
    expect(label('ready_for_execution')).toBe('Ready for execution');
  });
});

describe('redesign vocabulary and routing', () => {
  it('waiting for instruction and fork have their own tones, and every label is sentence case', async () => {
    const { status, label } = await import('./status');
    expect(status('waiting_for_instruction')).toEqual({ tone: 'waiting', icon: 'prompt', label: 'Waiting for instruction' });
    expect(status('fork').tone).toBe('accent');
    expect(label('awaiting_phase_review')).toBe('Awaiting phase review');
    // a known state never falls back to neutral by accident
    expect(status('interrupted').tone).toBe('warning');
  });

  it('moving between the panes of one project keeps route.params as it was, so nothing reloads', async () => {
    const { route } = await import('./router.svelte');
    location.hash = '#/projects/prj_a';
    window.dispatchEvent(new HashChangeEvent('hashchange'));
    const before = route.params;
    location.hash = '#/projects/prj_a/plan';
    window.dispatchEvent(new HashChangeEvent('hashchange'));
    expect(route.name).toBe('plan');
    expect(route.params).toBe(before); // same object: no resource that reads route.params.id re-runs
    location.hash = '#/projects/prj_b/plan';
    window.dispatchEvent(new HashChangeEvent('hashchange'));
    expect(route.params).not.toBe(before);
    expect(route.params.id).toBe('prj_b');
  });

  it('the old approvals and workflow addresses still resolve to a route name the app redirects', async () => {
    const { match } = await import('./router.svelte');
    expect(match('#/approvals').name).toBe('approvals');
    expect(match('#/projects/p1/workflow').name).toBe('workflow');
    expect(match('#/phases/x/review').name).toBe('phase-review');
    expect(match('#/signin/abc').name).toBe('signin');
    expect(match('#/').name).toBe('overview');
  });
});

import { visible } from './text';
describe('visible', () => {
  it('leaves plain text, newlines and tabs alone', () => {
    expect(visible('go test ./...\n\tok 日本語 🙂')).toBe('go test ./...\n\tok 日本語 🙂');
  });
  it('spells out escape, carriage return, direction overrides and zero-width characters', () => {
    expect(visible('a\x1b[2Kb')).toBe('a\\u{001B}[2Kb');
    expect(visible('x\ry')).toBe('x\\u{000D}y');
    expect(visible('ok ‮ txt')).toBe('ok \\u{202E} txt');
    expect(visible('a​b﻿c')).toBe('a\\u{200B}b\\u{FEFF}c');
    expect(visible('a\u009bb')).toBe('a\\u{009B}b');
  });
});

import fixture from './unsafe.json';
describe('visible and the service agree on one list', () => {
  it('flags every example of the shared list and none of the safe ones', () => {
    for (const cp of fixture.unsafe) expect(visible(String.fromCodePoint(cp)), 'U+' + cp.toString(16)).toMatch(/^\\u\{[0-9A-F]{4,6}\}$/);
    for (const cp of fixture.safe) expect(visible(String.fromCodePoint(cp)), 'U+' + cp.toString(16)).toBe(String.fromCodePoint(cp));
  });
});

describe('timeline', () => {
  const now = Date.parse('2026-11-05T12:00:00Z');
  const at = (min: number) => new Date(now - min * 60_000).toISOString();
  it('places a bar from its start to its end or to now, clipped to the hour', () => {
    expect(span({ started_at: at(30) }, now)).toEqual({ left: 50, width: 50 });
    expect(span({ started_at: at(30), ended_at: at(15) }, now)).toEqual({ left: 50, width: 25 });
    expect(span({ started_at: at(90), ended_at: at(45) }, now)).toEqual({ left: 0, width: 25 });
    expect(span({ started_at: at(0) }, now)).toEqual({ left: 100, width: 0 });
  });
  it('leaves out what never started or ended before the hour', () => {
    expect(span({}, now)).toBeNull();
    expect(inLastHour({ started_at: at(120), ended_at: at(61) }, now)).toBe(false);
    expect(inLastHour({ started_at: at(120) }, now)).toBe(true); // still running
  });
  it('groups a disconnected execution with the interrupted ones and a waiting one by itself', () => {
    expect(groupOf({ state: 'disconnected' })).toBe('interrupted');
    expect(groupOf({ state: 'starting' })).toBe('running');
    expect(groupOf({ state: 'running', waiting_since: at(1) })).toBe('waiting');
  });
});
