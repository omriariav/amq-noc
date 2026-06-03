// Package console — noc_color.go: the NOC color-mode resolver. It maps the
// terminal's capability to one of three modes the NOC theme + glyph layer use.
//
// This is intentionally self-contained from the session console's lipgloss
// auto-detection: the NOC surface must produce DETERMINISTIC plain text for the
// --once / NO_COLOR / non-TTY tests (no ANSI escapes at all), and must drop to
// pure-ASCII glyphs on a dumb terminal. lipgloss.ColorProfile() conflates "no
// color" with "ascii glyphs"; the NOC layer keeps them separate so a no-color
// UTF-8 terminal still gets unicode glyphs while a dumb terminal gets ascii.
package console

import (
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ColorMode is the resolved rendering capability for the NOC surface.
type ColorMode int

const (
	// ColorFull: ANSI color + unicode glyphs (a capable interactive terminal).
	ColorFull ColorMode = iota
	// ColorNone: no ANSI color, but unicode glyphs are fine (a UTF-8 pipe, or
	// NO_COLOR set on a unicode terminal). The --once board to a non-TTY lands
	// here unless the terminal is dumb.
	ColorNone
	// ColorAscii: no color AND no unicode — a dumb terminal. Glyphs degrade to
	// ascii markers ([run]/[deg]/[stop]/[!]/[x], +/-/|).
	ColorAscii
)

// nocColorEnv / nocColorProfile are seams so tests drive mode resolution without
// depending on the real environment or terminal.
var (
	nocColorEnv     = os.Getenv
	nocColorProfile = func() string { return lipgloss.ColorProfile().Name() }
)

// resolveColorMode picks the NOC ColorMode from the environment + terminal.
//
//   - interactive=false (a pipe / --once to a non-TTY): never emit color. The
//     mode is ColorAscii on a dumb terminal (TERM=dumb / unset), else ColorNone.
//   - NO_COLOR set (any non-empty value): no color; ascii only if also dumb.
//   - TERM=dumb or unset: ColorAscii (no color, ascii glyphs).
//   - otherwise: ColorFull.
func resolveColorMode(interactive bool) ColorMode {
	dumb := terminalIsDumb()
	noColor := strings.TrimSpace(nocColorEnv("NO_COLOR")) != ""

	if !interactive || noColor {
		if dumb {
			return ColorAscii
		}
		return ColorNone
	}
	if dumb {
		return ColorAscii
	}
	// A capable interactive terminal but a profile that cannot do color (the
	// lipgloss "Ascii" profile) still wants plain text; unicode is fine.
	if nocColorProfile() == "Ascii" {
		return ColorNone
	}
	return ColorFull
}

// terminalIsDumb reports whether TERM indicates a no-unicode/no-capability
// terminal (unset or "dumb").
func terminalIsDumb() bool {
	term := strings.TrimSpace(nocColorEnv("TERM"))
	return term == "" || term == "dumb"
}
