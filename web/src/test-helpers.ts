import { createClient, type Session } from './api/client';
import { createMock, type MockOptions } from './mock/server';
import { MOCK_CSRF, MOCK_PASSPHRASE } from './mock/seed';

export { MOCK_PASSPHRASE };

/** A client wired to a fresh mock service, already signed in. */
export function setup(opts: MockOptions = {}) {
  const mock = createMock(opts);
  const session: Session & { unauth: number } = { csrf: MOCK_CSRF, unauth: 0, onUnauthenticated() { session.unauth++; } };
  const api = createClient(mock.transport, session);
  return { mock, api, session };
}
