package core

import (
	"fmt"
	"strings"
)

// Text an agent writes ends up where a human decides: validator commands, titles and risky-action text in an approval, a
// result summary, a blocker. A terminal or a browser can be made to show something other than the bytes by ESC and CR
// sequences, text-direction overrides and zero-width characters, so these are refused at the door and shown visibly
// (Visible) wherever stored text is printed. Newline and tab stay: commands and prose use them.

// UnsafeRune reports a rune of s that can hide or rewrite text where a human reads it.
func UnsafeRune(s string) (rune, bool) {
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
		case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f: // C0 (ESC, CR, BEL, BS), DEL, C1 (CSI)
			return r, true
		case r == 0x061c, r == 0x200b, r == 0x200c, r == 0x200d, r == 0x200e, r == 0x200f, r == 0x2060, r == 0xfeff, r == 0x2028, r == 0x2029,
			r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069: // direction marks and overrides, zero-width, line separators
			return r, true
		case r == 0xad, r == 0x34f, r == 0x180e, r == 0x115f, r == 0x1160, r == 0x3164, r == 0xffa0, r == 0x2800, // soft hyphen, grapheme joiner, Mongolian vowel separator, Hangul fillers, blank braille
			r >= 0x2061 && r <= 0x2064, r >= 0x206a && r <= 0x206f, // invisible operators, deprecated format controls
			r >= 0xfe00 && r <= 0xfe0f, r >= 0xe0100 && r <= 0xe01ef, // variation selectors
			r >= 0xfff9 && r <= 0xfffc,   // interlinear annotation, object replacement
			r >= 0xe0000 && r <= 0xe007f: // tag characters ("ASCII smuggling")
			return r, true
		}
	}
	return 0, false
}

// plainText refuses text with an unsafe rune and says which, so the agent can resend plain text.
func plainText(what, s string) *Error {
	if r, bad := UnsafeRune(s); bad {
		return errValidation(fmt.Sprintf("%s contains a control or text-direction character (U+%04X).", what, r), "Send plain text without escape, carriage-return or direction characters.")
	}
	return nil
}

// Visible shows s with every unsafe rune spelled out as \u{XXXX}, so what a human reads is what is stored.
func Visible(s string) string {
	if _, bad := UnsafeRune(s); !bad {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if _, bad := UnsafeRune(string(r)); bad {
			fmt.Fprintf(&b, "\\u{%04X}", r)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// visibleAll applies Visible to each string.
func visibleAll(xs []string) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = Visible(x)
	}
	return out
}
