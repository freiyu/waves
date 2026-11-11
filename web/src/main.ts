import { mount } from 'svelte';
import './styles/app.css';
import App from './App.svelte';
import { createApp, setApp } from './lib/app.svelte';
import { httpTransport } from './api/transport';
import { applyTheme } from './lib/theme.svelte';

applyTheme();

async function boot() {
  let deps;
  if (import.meta.env.VITE_ADAPTER !== 'mock') {
    deps = { transport: httpTransport, eventSource: (url: string) => new EventSource(url) as never };
  } else {
    // Review and test builds only (VITE_ADAPTER=mock): the mock adapter follows the same contract.
    const { createMock } = await import('./mock/server');
    const scenario = new URLSearchParams(location.search).get('scenario') ?? '';
    const qs = new URLSearchParams(location.search);
    const mock = createMock({ scenario, latencyMs: Number(qs.get('latency') ?? 60), tickMs: Number(qs.get('tick') ?? 5000) });
    deps = { transport: mock.transport, eventSource: mock.bus.factory };
    // Connection controls for reviewing the Reconnecting and Stale states. Nothing else of the mock is reachable.
    (window as unknown as Record<string, unknown>).__wavesMock = { drop: () => mock.bus.drop(), refuse: (on: boolean) => { mock.bus.refuse = on; if (on) mock.bus.drop(); } };
  }
  const a = createApp(deps);
  setApp(a);
  await a.init();
  mount(App, { target: document.getElementById('app')! });
}

void boot();
