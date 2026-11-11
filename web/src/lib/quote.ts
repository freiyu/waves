/** Quote a path for a POSIX shell, so a pasted command survives spaces, quotes and $(...). Plain paths stay as they are. */
export const shellQuote = (path: string) => (/^[\w@%+=:,./~-]+$/.test(path) ? path : `'${path.replace(/'/g, `'\\''`)}'`);
