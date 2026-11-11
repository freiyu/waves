import { describe, expect, it } from 'vitest';
import spec from '../../openapi.json';
import { createMock } from '../mock/server';
import { EVENT_NAMES } from '../lib/events';

describe('contract parity', () => {
  it('the mock adapter answers exactly the routes of the generated contract', () => {
    const fromSpec = Object.entries(spec.paths).flatMap(([p, ops]) => Object.keys(ops).filter((m) => ['get', 'post', 'patch', 'put', 'delete'].includes(m)).map((m) => `${m.toUpperCase()} ${p}`));
    const fromMock = createMock().routeKeys;
    expect([...fromMock].sort()).toEqual([...fromSpec].sort());
  });

  it('the SSE client listens for every event of the contract and no other', () => {
    const props = Object.keys((spec.components.schemas as any).ContractEventPayloads.properties);
    expect([...EVENT_NAMES].sort()).toEqual([...props].sort());
  });
});
