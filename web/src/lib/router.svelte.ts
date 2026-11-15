// Hash router: no server fallback needed, and a login code in the fragment is
// never sent to the server or to a Referer.
export interface RouteDef { name: string; path: string }

export const ROUTES: RouteDef[] = [
  { name: 'overview', path: '/' },
  { name: 'graph', path: '/graph' },
  { name: 'signin', path: '/signin/:code' },
  { name: 'signed-out', path: '/signed-out' },
  { name: 'welcome', path: '/welcome' },
  { name: 'projects', path: '/projects' },
  { name: 'project', path: '/projects/:id' },
  { name: 'requirements', path: '/projects/:id/requirements' },
  { name: 'plan', path: '/projects/:id/plan' },
  { name: 'review', path: '/projects/:id/review' },
  { name: 'guard', path: '/projects/:id/guard' },
  { name: 'workflow', path: '/projects/:id/workflow' },
  { name: 'workflow-version', path: '/workflow/:vid' },
  { name: 'phase-review', path: '/phases/:id/review' },
  { name: 'approvals', path: '/approvals' },
  { name: 'approval', path: '/approvals/:id' },
  { name: 'executions', path: '/executions' },
  { name: 'execution', path: '/executions/:id' },
  { name: 'recovery', path: '/executions/:id/recovery' },
  { name: 'harnesses', path: '/harnesses' },
  { name: 'connect', path: '/harnesses/connect/:id' },
  { name: 'activity', path: '/activity' },
  { name: 'settings', path: '/settings' },
];

export function match(hash: string): { name: string; params: Record<string, string> } {
  const path = ('/' + hash.replace(/^#\/?/, '').split('?')[0]!).replace(/\/+$/, '') || '/';
  for (const r of ROUTES) {
    const names: string[] = [];
    const re = new RegExp('^' + r.path.replace(/:(\w+)/g, (_, n: string) => (names.push(n), '([^/]+)')) + '$');
    const m = re.exec(path);
    if (m) {
      try {
        return { name: r.name, params: Object.fromEntries(names.map((n, i) => [n, decodeURIComponent(m[i + 1]!)])) };
      } catch {
        return { name: 'not-found', params: {} }; // a malformed % escape must not stop the app from starting
      }
    }
  }
  return { name: 'not-found', params: {} };
}

export const route = $state<{ name: string; params: Record<string, string> }>(match(typeof location === 'undefined' ? '' : location.hash));

if (typeof window !== 'undefined') {
  window.addEventListener('hashchange', () => {
    const m = match(location.hash);
    // Only a real change is written: resources that read route.params must not reload when the address changed but their own parameter did not
    // (moving between the panes of one project keeps the same id).
    if (route.name !== m.name) route.name = m.name;
    if (JSON.stringify(route.params) !== JSON.stringify(m.params)) route.params = m.params;
    document.getElementById('main')?.focus({ preventScroll: true });
    window.scrollTo(0, 0);
  });
}

export const href = (path: string) => '#' + path;
export const go = (path: string) => {
  location.hash = path;
};
