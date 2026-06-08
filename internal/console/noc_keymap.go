// Package console - noc_keymap.go: the single source of truth for the live NOC
// TUI key map. The help overlay (?) NAVIGATION/VIEW/CONTROL sections and the
// footer legend BOTH render from nocKeyMap, and the key-contract test asserts the
// router actually handles every key here (and nothing it dropped). So an
// advertised key cannot drift from a handled key. The CLI `noc --help` keeps a
// hand-written prose summary that is separately tested (noc_help_test.go) to carry
// no removed key. Keys the 0.1.0 prune removed (jump J/o, palette p, mute A, the
// AMQ context/DLQ/inbox/read c/D/i/v keys, timeline t) are absent here by
// construction, so any surface that still names them fails a contract test.
package console

// nocKeyGroup buckets a binding for grouped rendering.
type nocKeyGroup int

const (
	keyGroupNav nocKeyGroup = iota
	keyGroupView
	keyGroupAction
)

// nocKeyBinding is one operator key.
//   - Keys are the raw tea key tokens the router matches (the contract test drives
//     each one and asserts it is handled).
//   - HelpDisplay/Label render the help overlay row.
//   - Footer/FooterAscii render the compact one-line footer token; "" means the
//     binding folds into a neighbor's footer token (e.g. enter folds into expand).
//   - Mutating marks the preview+confirm control keys.
//   - Scopes lists the selected-row kinds a CONTROL key applies to, so the footer
//     can show only the actions valid for the current selection (#4.2). It mirrors
//     each begin* handler's own "applies to ..." guard. Empty for nav/view keys
//     (always available).
type nocKeyBinding struct {
	Keys        []string
	HelpDisplay string
	Label       string
	Footer      string
	FooterAscii string
	Group       nocKeyGroup
	Mutating    bool
	Scopes      []nocNodeKind
}

// nocKeyMap is the authoritative key map, in display order.
var nocKeyMap = []nocKeyBinding{
	// Navigation (read-only).
	{Keys: []string{"up", "k", "down", "j"}, HelpDisplay: "↑ / k, ↓ / j", Label: "move selection", Footer: "↑↓/jk move", FooterAscii: "up/down move", Group: keyGroupNav},
	{Keys: []string{"right", "l"}, HelpDisplay: "→ / l", Label: "expand a collapsed node, or drill into it", Footer: "→/l/⏎ expand/drill", FooterAscii: "right/l/enter expand", Group: keyGroupNav},
	{Keys: []string{"enter"}, HelpDisplay: "enter", Label: "expand/drill (navigation only)", Group: keyGroupNav},
	{Keys: []string{"left"}, HelpDisplay: "←", Label: "collapse the node, or ascend to its parent", Footer: "← collapse", FooterAscii: "left collapse", Group: keyGroupNav},

	// View (read-only).
	{Keys: []string{"/"}, HelpDisplay: "/", Label: "filter (needs-you / blocked / waiting / online / stale / agent: / model: / project: / session:)", Footer: "/ filter", FooterAscii: "/ filter", Group: keyGroupView},
	{Keys: []string{"h"}, HelpDisplay: "h", Label: "toggle hiding stopped/stale squads", Footer: "h hide-stale", FooterAscii: "h hide-stale", Group: keyGroupView},
	{Keys: []string{"f"}, HelpDisplay: "f", Label: "toggle the inter-agent flow graph in the detail pane", Footer: "f flow", FooterAscii: "f flow", Group: keyGroupView},
	{Keys: []string{"g"}, HelpDisplay: "g", Label: "refresh now", Footer: "g refresh", FooterAscii: "g refresh", Group: keyGroupView},
	{Keys: []string{"C"}, HelpDisplay: "C", Label: "pick a right-pane action command to copy", Footer: "C copy-cmd", FooterAscii: "C copy-cmd", Group: keyGroupView},
	{Keys: []string{"esc"}, HelpDisplay: "esc", Label: "clear filter / collapse / back", Footer: "esc back", FooterAscii: "esc back", Group: keyGroupView},
	{Keys: []string{"?"}, HelpDisplay: "?", Label: "toggle this help", Footer: "? help", FooterAscii: "? help", Group: keyGroupView},
	{Keys: []string{"q", "ctrl+c"}, HelpDisplay: "q", Label: "quit", Footer: "q quit", FooterAscii: "q quit", Group: keyGroupView},

	// Control (mutating; every key previews + confirms before it touches a squad).
	// Scopes mirror each begin* handler's "applies to ..." guard in noc_control.go.
	{Keys: []string{"delete"}, HelpDisplay: "Del", Label: "delete selected session or team profile (preview + confirm)", Footer: "Del delete", FooterAscii: "Del delete", Group: keyGroupAction, Mutating: true, Scopes: []nocNodeKind{nodeProject, nodeSession}},
	{Keys: []string{"d"}, HelpDisplay: "d", Label: "drain the selected agent inbox with bodies (preview + confirm)", Footer: "d drain", FooterAscii: "d drain", Group: keyGroupAction, Mutating: true, Scopes: []nocNodeKind{nodeAgent}},
	{Keys: []string{"a"}, HelpDisplay: "a", Label: "approve the selected needs-you thread", Footer: "a approve", FooterAscii: "a approve", Group: keyGroupAction, Mutating: true, Scopes: []nocNodeKind{nodeSession, nodeAgent}},
	{Keys: []string{"r"}, HelpDisplay: "r", Label: "reply to the selected needs-you thread", Footer: "r reply", FooterAscii: "r reply", Group: keyGroupAction, Mutating: true, Scopes: []nocNodeKind{nodeSession, nodeAgent}},
	{Keys: []string{"x"}, HelpDisplay: "x", Label: "deny the selected needs-you thread", Footer: "x deny", FooterAscii: "x deny", Group: keyGroupAction, Mutating: true, Scopes: []nocNodeKind{nodeSession, nodeAgent}},
	{Keys: []string{"m"}, HelpDisplay: "m", Label: "message the selected agent", Footer: "m message", FooterAscii: "m message", Group: keyGroupAction, Mutating: true, Scopes: []nocNodeKind{nodeAgent}},
	{Keys: []string{"b"}, HelpDisplay: "b", Label: "broadcast to the selected squad", Footer: "b broadcast", FooterAscii: "b broadcast", Group: keyGroupAction, Mutating: true, Scopes: []nocNodeKind{nodeProject, nodeSession}},
	{Keys: []string{"S"}, HelpDisplay: "S", Label: "stop the selected squad (preview + confirm)", Footer: "S stop", FooterAscii: "S stop", Group: keyGroupAction, Mutating: true, Scopes: []nocNodeKind{nodeProject, nodeSession}},
	{Keys: []string{"R"}, HelpDisplay: "R", Label: "resume the selected squad (preview + confirm)", Footer: "R resume", FooterAscii: "R resume", Group: keyGroupAction, Mutating: true, Scopes: []nocNodeKind{nodeProject, nodeSession}},
	{Keys: []string{"X"}, HelpDisplay: "X", Label: "restart the selected squad (preview + confirm)", Footer: "X restart", FooterAscii: "X restart", Group: keyGroupAction, Mutating: true, Scopes: []nocNodeKind{nodeProject, nodeSession}},
	{Keys: []string{"N"}, HelpDisplay: "N", Label: "start a new workstream session (rejects existing names)", Footer: "N new-session", FooterAscii: "N new-session", Group: keyGroupAction, Mutating: true, Scopes: []nocNodeKind{nodeProject, nodeSession, nodeAgent}},
	{Keys: []string{"T"}, HelpDisplay: "T", Label: "create a team profile + pointer stubs", Footer: "T new-team", FooterAscii: "T new-team", Group: keyGroupAction, Mutating: true, Scopes: []nocNodeKind{nodeProject, nodeSession, nodeAgent}},
}

// nocKeymapKeys returns every raw key token the keymap advertises in the given
// group. The contract test asserts each is handled by the router.
func nocKeymapKeys(group nocKeyGroup) []string {
	var out []string
	for _, b := range nocKeyMap {
		if b.Group == group {
			out = append(out, b.Keys...)
		}
	}
	return out
}

// nocKeyHelpLines renders one help-overlay section (header + rows) from the map.
func nocKeyHelpLines(group nocKeyGroup, header string) []string {
	lines := []string{header}
	for _, b := range nocKeyMap {
		if b.Group != group {
			continue
		}
		lines = append(lines, "  "+padKeyDisplay(b.HelpDisplay)+b.Label)
	}
	return lines
}

// nocControlHelpLines is the CONTROL help section plus its confirm note.
func nocControlHelpLines() []string {
	lines := nocKeyHelpLines(keyGroupAction, "CONTROL (every mutating key previews + confirms before it touches a squad)")
	return append(lines,
		"",
		"Every mutating key opens a CONFIRM overlay showing the EXACT command;",
		"y/enter confirms; any other key or esc cancels with ZERO effect.")
}

// nocFooterLegend renders the compact one-line footer legend for a group. ascii
// picks the ASCII token; sep is the inter-token separator.
func nocFooterLegend(group nocKeyGroup, ascii bool, sep string) string {
	out := ""
	for _, b := range nocKeyMap {
		if b.Group != group {
			continue
		}
		tok := b.Footer
		if ascii {
			tok = b.FooterAscii
		}
		if tok == "" {
			continue
		}
		if out != "" {
			out += sep
		}
		out += tok
	}
	return out
}

// nocFooterNavLegend is the nav+view footer row (nav and view share one row).
func nocFooterNavLegend(ascii bool) string {
	sep := " · "
	if ascii {
		sep = " | "
	}
	nav := nocFooterLegend(keyGroupNav, ascii, sep)
	view := nocFooterLegend(keyGroupView, ascii, sep)
	return nav + sep + view
}

// appliesTo reports whether a control binding applies to a selected row kind.
func (b nocKeyBinding) appliesTo(kind nocNodeKind) bool {
	for _, s := range b.Scopes {
		if s == kind {
			return true
		}
	}
	return false
}

// padKeyDisplay left-aligns the key column so labels line up. Rune-width aware so
// the arrow glyphs do not misalign.
func padKeyDisplay(display string) string {
	const col = 18
	w := 0
	for range display {
		w++
	}
	for i := 0; i < col-w; i++ {
		display += " "
	}
	return display
}
