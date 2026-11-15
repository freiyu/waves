/** Agent-written text can hold characters that make a terminal or browser show something other than what is stored: escape and
 *  carriage-return controls, text-direction overrides, zero-width and invisible characters, tag characters. Where a human decides
 *  on stored text, those are spelled out as \u{XXXX} so what is read is what is stored. Newline and tab stay. The set equals the
 *  service's (it refuses these on input); unsafe.json holds one list of examples that both sides test against. */
const UNSAFE = new RegExp('[\\u0000-\\u0008\\u000b-\\u001f\\u007f-\\u009f\\u00ad\\u034f\\u061c\\u115f\\u1160\\u180e\\u200b-\\u200f\\u2028-\\u202e\\u2060-\\u2064\\u2066-\\u206f\\u2800\\u3164\\ufe00-\\ufe0f\\ufeff\\uffa0\\ufff9-\\ufffc\\u{e0000}-\\u{e007f}\\u{e0100}-\\u{e01ef}]', 'gu');
export const visible = (s: string) => s.replace(UNSAFE, (c) => `\\u{${c.codePointAt(0)!.toString(16).toUpperCase().padStart(4, '0')}}`);
/** True when `visible` would change the text. */
export const hasHidden = (s: string) => visible(s) !== s;
