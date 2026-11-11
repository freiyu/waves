import { describe, expect, it } from 'vitest';
import { shellQuote } from './quote';

describe('shellQuote', () => {
  it('leaves a plain absolute path alone', () => {
    expect(shellQuote('/home/dev/work/billing-api')).toBe('/home/dev/work/billing-api');
    expect(shellQuote('~/work/x_1.2')).toBe('~/work/x_1.2');
  });
  it('quotes spaces, shell metacharacters and an empty path', () => {
    expect(shellQuote('/home/dev/my project')).toBe("'/home/dev/my project'");
    expect(shellQuote('/tmp/$(rm -rf x)')).toBe("'/tmp/$(rm -rf x)'");
    expect(shellQuote('/tmp/a;b')).toBe("'/tmp/a;b'");
    expect(shellQuote('')).toBe("''");
  });
  it("escapes a single quote the POSIX way", () => {
    expect(shellQuote("/tmp/it's")).toBe("'/tmp/it'\\''s'");
  });
});
