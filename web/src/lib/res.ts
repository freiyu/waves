import type { ApiError } from '../api/client';

/** What a page needs from a loaded resource. */
export interface Res<T> {
  readonly data: T | undefined;
  readonly error: ApiError | undefined;
  readonly loading: boolean;
  reload: () => Promise<void>;
}
