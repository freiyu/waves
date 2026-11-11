export type Theme = 'system' | 'light' | 'dark';
const KEY = 'waves-theme';

function read(): Theme {
  try {
    const v = localStorage.getItem(KEY);
    return v === 'light' || v === 'dark' ? v : 'system';
  } catch {
    return 'system'; // storage can be blocked; the page works without it
  }
}

export const theme = $state({ value: read() });

export function applyTheme() {
  if (theme.value === 'system') delete document.documentElement.dataset.theme;
  else document.documentElement.dataset.theme = theme.value;
}

export function cycleTheme() {
  theme.value = theme.value === 'system' ? 'light' : theme.value === 'light' ? 'dark' : 'system';
  try {
    if (theme.value === 'system') localStorage.removeItem(KEY);
    else localStorage.setItem(KEY, theme.value);
  } catch {
    /* ignore */
  }
  applyTheme();
}

export function setTheme(v: Theme) {
  theme.value = v;
  try {
    if (v === 'system') localStorage.removeItem(KEY);
    else localStorage.setItem(KEY, v);
  } catch {
    /* ignore */
  }
  applyTheme();
}
