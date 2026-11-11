import type { Snippet } from 'svelte';

export interface Column<R> {
  key: string;
  label: string;
  /** Plain text of the cell. Shown when `cell` is absent. */
  text?: (r: R) => string;
  /** Value to sort by. Columns without it are not sortable. */
  sort?: (r: R) => string | number;
  cell?: Snippet<[R]>;
  mono?: boolean;
}

export interface Filter<R> {
  id: string;
  label: string;
  pick: (r: R) => string;
  options?: Array<{ value: string; label: string }>;
  /** Show as filter chips with counts instead of a select. */
  chips?: boolean;
  /** With `chips`: put them on their own line under the search, for filters with many options. */
  row?: boolean;
}
