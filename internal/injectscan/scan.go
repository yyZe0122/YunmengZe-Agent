// Package injectscan validates instruction text and reports suspicious phrases.
// Phrase diagnostics are not an authorization or execution boundary.
package injectscan

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ErrRejected means content must not be written or injected.
var ErrRejected = errors.New("injectscan: content rejected")

// MaxScanRunes caps work per call (very large blobs still fail closed if dirty prefix).
const MaxScanRunes = 200_000

// Scan reports whether text is safe to store/inject as system-side instruction text.
// Empty input is OK. Returns ErrRejected wrapped with a short reason on failure.
func Scan(text string) error {
	if text == "" {
		return nil
	}
	if !utf8.ValidString(text) {
		return fmt.Errorf("%w: invalid utf-8", ErrRejected)
	}
	if utf8.RuneCountInString(text) > MaxScanRunes {
		return fmt.Errorf("%w: exceeds max scan runes", ErrRejected)
	}
	if err := scanInvisible(text); err != nil {
		return err
	}
	if err := scanInjectionMarkers(text); err != nil {
		slog.Warn("instruction text diagnostic", "component", "injectscan", "error", err)
	}
	return nil
}

func scanInvisible(text string) error {
	for _, r := range text {
		if r == '\n' || r == '\r' || r == '\t' {
			continue
		}
		// C0 controls except common whitespace above.
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: control character U+%04X", ErrRejected, r)
		}
		// Zero-width / bidi / tag / soft hyphen / fullwidth-slash camouflage.
		switch {
		case r == 0x00AD, // soft hyphen
			r == 0x034F,              // combining grapheme joiner
			r == 0x061C,              // arabic letter mark
			r == 0x115F, r == 0x1160, // hangul fillers
			r == 0x17B4, r == 0x17B5, // khmer vowel inherent
			r == 0x180B, r == 0x180C, r == 0x180D, r == 0x180E,
			r == 0x200B, r == 0x200C, r == 0x200D, r == 0x200E, r == 0x200F, // zw* / lrm / rlm
			r == 0x202A, r == 0x202B, r == 0x202C, r == 0x202D, r == 0x202E, // bidi embeddings
			r == 0x2060, r == 0x2061, r == 0x2062, r == 0x2063, r == 0x2064,
			r == 0x2066, r == 0x2067, r == 0x2068, r == 0x2069, // isolate
			r == 0x206A, r == 0x206B, r == 0x206C, r == 0x206D, r == 0x206E, r == 0x206F,
			r == 0x3164,                           // hangul filler
			r == 0xFEFF,                           // BOM / ZWNBSP
			r == 0xFFA0,                           // halfwidth hangul filler
			r == 0xFFF9, r == 0xFFFA, r == 0xFFFB, // interlinear annotation
			r >= 0xE0001 && r <= 0xE007F: // tags
			return fmt.Errorf("%w: invisible/bidi character U+%04X", ErrRejected, r)
		}
		// Other format chars (Cf) except those already handled — fail closed.
		if unicode.In(r, unicode.Cf) {
			return fmt.Errorf("%w: format character U+%04X", ErrRejected, r)
		}
	}
	return nil
}

func scanInjectionMarkers(text string) error {
	lower := strings.ToLower(text)
	// Compact whitespace for phrase checks.
	compact := strings.Join(strings.Fields(lower), " ")
	markers := []string{
		"ignore previous instructions",
		"ignore all previous instructions",
		"ignore your previous instructions",
		"disregard previous instructions",
		"disregard all prior instructions",
		"forget previous instructions",
		"you are now",
		"you are dan",
		"do anything now",
		"developer mode enabled",
		"system prompt override",
		"override system prompt",
		"jailbreak",
		"<|im_start|>",
		"<|im_end|>",
		"<|system|>",
		"<<sys>>",
		"[system]",
		"### system",
		"begin system prompt",
		"new instructions supersede",
		"from now on you will",
	}
	for _, m := range markers {
		if strings.Contains(compact, m) || strings.Contains(lower, m) {
			return fmt.Errorf("%w: injection marker %q", ErrRejected, m)
		}
	}
	// Role-play system header lines.
	for _, line := range strings.Split(lower, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "system:") && len(line) > 7 {
			// Allow "system: " only if very short? Fail if looks like role label.
			rest := strings.TrimSpace(strings.TrimPrefix(line, "system:"))
			if rest != "" && !strings.HasPrefix(rest, "http") {
				return fmt.Errorf("%w: system role line", ErrRejected)
			}
		}
	}
	if strings.Contains(text, "／") || strings.Contains(text, "＼") {
		if strings.Contains(compact, "system") || strings.Contains(compact, "prompt") {
			return fmt.Errorf("%w: fullwidth slash near system/prompt", ErrRejected)
		}
	}
	return nil
}
