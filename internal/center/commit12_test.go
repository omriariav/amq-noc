package center

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func mustParse(t *testing.T, doc string) Fleet {
	t.Helper()
	f, err := Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func mustReplace(t *testing.T, doc, old, new string) string {
	t.Helper()
	if !strings.Contains(doc, old) {
		t.Fatalf("fixture lacks %q", old)
	}
	return strings.Replace(doc, old, new, 1)
}

// Item 2: lead_handle is read first; the role-lead seat is the fallback.
func TestLeadHandleFirstThenRoleFallback(t *testing.T) {
	single := mustParse(t, string(goldenFixture)).Squads[0]
	if single.LeadHandle != "lead" || Lead(single) != "lead" {
		t.Fatalf("golden lead_handle = %q, Lead = %q", single.LeadHandle, Lead(single))
	}
	// lead_handle wins over a seat whose role is lead
	single.Seats = append(single.Seats, Seat{Handle: "boss", Role: "lead"})
	if got := Lead(single); got != "lead" {
		t.Fatalf("lead_handle must win, got %q", got)
	}
	// null lead_handle (the fleet golden) falls back to the role
	fleetSquad := mustParse(t, string(goldenFleet)).Squads[0]
	if Lead(fleetSquad) != "" {
		t.Fatalf("fleet golden has no lead, got %q", Lead(fleetSquad))
	}
	fleetSquad.Seats = append(fleetSquad.Seats, Seat{Handle: "boss", Role: "lead"})
	if got := Lead(fleetSquad); got != "boss" {
		t.Fatalf("role fallback = %q", got)
	}
}

// Item 3: a seat row whose live is false and whose bucket is not Inactive
// shows a marker, and the details pane names it. Inactive, live and absent
// `live` show none.
func TestNotLiveMarker(t *testing.T) {
	g := string(goldenFixture)
	i := strings.Index(g, `"handle": "impl-2"`)
	if i < 0 {
		t.Fatal("fixture lacks impl-2")
	}
	doc := g[:i] + strings.Replace(g[i:], `"live": true,`, `"live": false,`, 1)
	f := mustParse(t, doc)
	m, _ := newTestModel(f, 120, 30)
	v := m.View()
	if !strings.Contains(v, "⊘ impl-2 (implementer)") {
		t.Fatalf("no marker on the not-live ready seat:\n%s", v)
	}
	if strings.Count(v, "⊘") != 1 { // the row; the details pane shows the selected row only
		t.Fatalf("marker count %d", strings.Count(v, "⊘"))
	}
	press(m, "down", "down")
	if r, _ := m.selected(); r.Seat == nil || r.Seat.Handle != "impl-2" {
		t.Fatalf("selected %+v", r)
	}
	if !strings.Contains(m.View(), "not live") {
		t.Fatalf("details pane does not name it:\n%s", m.View())
	}
	// the golden itself (all live) has no marker
	mg, _ := newTestModel(mustParse(t, string(goldenFixture)), 120, 30)
	if strings.Contains(mg.View(), "⊘") {
		t.Fatal("marker on a live seat")
	}
	// Inactive bucket: the contract already says so, no marker
	inact := mustReplace(t, doc, `"bucket": "ready"`, `"bucket": "inactive"`)
	mi, _ := newTestModel(mustParse(t, inact), 120, 30)
	if strings.Contains(mi.View(), "⊘") {
		t.Fatal("marker on an Inactive seat")
	}
	// a producer without the field: nil, no marker
	nolive := strings.ReplaceAll(string(goldenFixture), "\"live\": true,", "")
	mn, _ := newTestModel(mustParse(t, nolive), 120, 30)
	if strings.Contains(mn.View(), "⊘") {
		t.Fatal("marker when live is absent")
	}
}

// Item 4: the fleet document's operator row is its own row at the top of the
// fleet list, and its handle is the conversation's operator handle.
func TestFleetOperatorRowAtTop(t *testing.T) {
	f := mustParse(t, string(goldenFleet))
	m, _ := newTestModel(f, 120, 40)
	lines := strings.Split(m.View(), "\n")
	var first []string
	for _, l := range lines[4:] { // after title, tabs, rule, column header
		l, _, _ = strings.Cut(l, " │ ") // the list column only
		if strings.TrimSpace(l) != "" {
			first = append(first, l)
		}
		if len(first) == 3 {
			break
		}
	}
	if !strings.HasPrefix(first[0], "Operator  1") || !strings.Contains(first[1], "user (operator)") || !strings.Contains(first[1], "Ready") {
		t.Fatalf("operator group is not first:\n%s", strings.Join(first, "\n"))
	}
	if !strings.Contains(first[2], "/Users/owner/code/app") {
		t.Fatalf("squad group does not follow:\n%s", strings.Join(first, "\n"))
	}
	if r, ok := m.selected(); !ok || r.Section != sectionOperator {
		t.Fatalf("cursor not on the operator row: %+v", r)
	}
	if got := Operator(f); got != "user" {
		t.Fatalf("operator handle = %q", got)
	}
	// the operator row follows the tabs by its bucket
	press(m, "tab", "tab", "tab") // Ready
	if !strings.Contains(m.View(), "user (operator)") {
		t.Fatal("operator row missing from Ready")
	}
	press(m, "tab") // Inactive
	if strings.Contains(m.View(), "user (operator)") {
		t.Fatal("operator row in Inactive")
	}
	// enter on the operator row opens no conversation
	press(m, "esc", "enter")
	if m.mode != modeList || !strings.Contains(m.note, "belongs to no squad") {
		t.Fatalf("mode=%d note=%q", m.mode, m.note)
	}
	// the conversation uses the operator row's handle when present, else user
	named := mustReplace(t, string(goldenFleet), `"handle": "user",`, `"handle": "omri",`)
	fn := mustParse(t, named)
	if Operator(fn) != "omri" {
		t.Fatalf("operator = %q", Operator(fn))
	}
	fn.Squads[0].LeadHandle = "cto"
	mc, fr := newTestModel(fn, 120, 40)
	for mc.items[mc.cursor].kind != itemRow || mc.items[mc.cursor].row.Section != sectionSquad {
		press(mc, "down")
	}
	_, cmd := mc.Update(keyMsg("enter"))
	if cmd == nil || mc.conv.operator != "omri" {
		t.Fatalf("conversation operator = %q", mc.conv.operator)
	}
	mc.Update(cmd())
	if got := strings.Join(fr.calls[len(fr.calls)-1], " "); !strings.Contains(got, "--id p2p/cto__omri --me omri") {
		t.Fatalf("argv = %q", got)
	}
	// without an operator row it is user, as before
	if Operator(mustParse(t, string(goldenFixture))) != "user" {
		t.Fatal("default operator is not user")
	}
	// new task from the operator row uses the first squad's action
	mo, _ := newTestModel(f, 120, 40)
	press(mo, "n")
	if mo.mode != modeNew {
		t.Fatalf("n on the operator row: mode %d note %q", mo.mode, mo.note)
	}
}

// Item 4: waiting[] rows render in a Waiting group with wait_reason and squad.
func TestWaitingGroup(t *testing.T) {
	f := mustParse(t, string(goldenFleet))
	m, _ := newTestModel(f, 120, 40)
	v := m.View()
	if !strings.Contains(v, "Waiting  1") || !strings.Contains(v, "docs") || !strings.Contains(v, "fleet_cap") {
		t.Fatalf("waiting group missing:\n%s", v)
	}
	if strings.Index(v, "Waiting  1") < strings.Index(v, "/Users/owner/code/app  3") {
		t.Fatal("Waiting group must follow the squads")
	}
	if m.counts[TabAll] != 4 || m.counts[TabReady] != 2 || m.counts[TabAll] != m.counts[TabNeedsYou]+m.counts[TabWorking]+m.counts[TabReady]+m.counts[TabInactive] {
		t.Fatalf("counts = %v", m.counts)
	}
	for tab := TabNeedsYou; tab < tabCount; tab++ {
		press(m, "tab")
		if strings.Contains(m.View(), "Waiting  1") {
			t.Fatalf("waiting row in tab %d", tab)
		}
	}
	press(m, "tab", "G")
	if r, ok := m.selected(); !ok || r.Waiting == nil {
		t.Fatalf("G did not land on the waiting row: %+v", r)
	}
	dv := m.View()
	for _, want := range []string{"Waiting squad", "fleet_cap", "max_squads", "owner/docs", "2 squads live, max_squads 2", "/Users/owner/code/docs"} {
		if !strings.Contains(dv, want) {
			t.Errorf("waiting details lack %q\n%s", want, dv)
		}
	}
	press(m, "enter")
	if m.mode != modeList {
		t.Fatal("a waiting row opened a conversation")
	}
}

// Fleet-mode snapshots of the vendored fleet golden at 120 and 80 columns.
func TestGoldenFleetSnapshots(t *testing.T) {
	f := mustParse(t, string(goldenFleet))
	for _, w := range []int{120, 80} {
		m, _ := newTestModel(f, w, 28)
		m.now = func() time.Time { return f.Squads[0].GeneratedAt }
		golden(t, fmt.Sprintf("fleet-%d", w), m.View())
	}
}

// Item 5: the help overlay never exceeds the terminal height; a fold names the
// rest and the arrow keys scroll to it.
func TestHelpClampedToTerminalHeight(t *testing.T) {
	s := testSquad("/p/a", BucketWorking)
	for i := 0; i < 40; i++ {
		s.Warnings = append(s.Warnings, fmt.Sprintf("warning line %02d", i))
	}
	m, _ := newTestModel(Fleet{Squads: []Status{s}}, 100, 20)
	press(m, "?")
	v := m.View()
	if got := len(strings.Split(v, "\n")); got != 20 {
		t.Fatalf("help frame is %d lines, want 20:\n%s", got, v)
	}
	if !strings.Contains(v, "Keys") || !strings.Contains(v, "more)") {
		t.Fatalf("no fold in the clamped help:\n%s", v)
	}
	if strings.Contains(v, "warning line 39") {
		t.Fatal("last warning visible before scrolling")
	}
	for i := 0; i < 5; i++ {
		press(m, "pgdown")
	}
	v = m.View()
	if got := len(strings.Split(v, "\n")); got != 20 {
		t.Fatalf("scrolled help frame is %d lines", got)
	}
	if !strings.Contains(v, "warning line 39") || !strings.Contains(v, "above)") {
		t.Fatalf("end of the help not reachable:\n%s", v)
	}
	press(m, "k")
	if m.helpTop <= 0 {
		t.Fatal("up did not scroll")
	}
	// a tall terminal needs no fold
	mt, _ := newTestModel(Fleet{Squads: []Status{s}}, 100, 120)
	press(mt, "?")
	if tv := mt.View(); strings.Contains(tv, "up/down scroll") || !strings.Contains(tv, "warning line 39") {
		t.Fatalf("tall help folded:\n%s", tv)
	}
	// reopening starts at the top
	press(m, "esc", "?")
	if m.helpTop != 0 {
		t.Fatal("help did not reopen at the top")
	}
}
