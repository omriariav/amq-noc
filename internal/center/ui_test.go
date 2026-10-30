package center

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

var update = flag.Bool("update", false, "rewrite the golden text snapshots")

func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.Ascii)
	os.Exit(m.Run())
}

// fakeRunner records argv and answers from canned output by program and verb.
type fakeRunner struct {
	calls  [][]string
	status string
	thread string
	err    error
}

func (f *fakeRunner) Run(_ context.Context, argv []string) ([]byte, error) {
	f.calls = append(f.calls, argv)
	if f.err != nil {
		return nil, f.err
	}
	switch {
	case argv[0] == "amq-squad" && argv[1] == "status":
		return []byte(f.status), nil
	case argv[0] == "amq" && argv[1] == "thread":
		return []byte(f.thread), nil
	}
	return []byte("t9 added\n"), nil
}

func newTestModel(f Fleet, w, h int) (*Model, *fakeRunner) {
	fr := &fakeRunner{thread: `[]`}
	m := New(Client{Runner: fr}, func() time.Time { return testNow })
	m.SetSize(w, h)
	m.SetFleet(f)
	return m, fr
}

func press(m *Model, keys ...string) {
	for _, k := range keys {
		m.Update(keyMsg(k))
	}
}

func keyMsg(k string) tea.KeyMsg {
	switch k {
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEscape}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	// trailing spaces carry no information in a text snapshot
	var lines []string
	for _, l := range strings.Split(got, "\n") {
		lines = append(lines, strings.TrimRight(l, " "))
	}
	got = strings.Join(lines, "\n") + "\n"
	p := filepath.Join("testdata", "golden", name+".txt")
	if *update {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("%v (run go test -update)", err)
	}
	if string(want) != got {
		t.Fatalf("%s differs from snapshot:\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

// eightProjects mirrors the grouping of the reference screenshot: eight
// project paths, one with seven rows (over the fold), counts per group.
func eightProjects() Fleet {
	projects := []struct {
		path string
		n    int
	}{
		{"/p/scratch", 5}, {"/p/spike/main-repo", 2}, {"/p/spike/wt-a", 3}, {"/p/wt/agent-a88", 1},
		{"/p/wt/agent-ac2", 1}, {"/p/amq-squad", 7}, {"/p/ios_organizer", 1}, {"/p/private/notify-probe", 1},
	}
	var f Fleet
	for _, p := range projects {
		bs := make([]string, p.n)
		for i := range bs {
			bs[i] = BucketInactive
		}
		if p.path == "/p/ios_organizer" {
			bs[0] = BucketWorking
		}
		f.Squads = append(f.Squads, testSquad(p.path, bs...))
	}
	return f
}

// Acceptance 2: the vendored fixture renders to a committed text snapshot at
// 120 and at 80 columns.
func TestGoldenFixtureSnapshots(t *testing.T) {
	f, err := Parse(goldenFixture)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []int{120, 80} {
		m, _ := newTestModel(f, w, 28)
		m.now = func() time.Time { return f.Squads[0].GeneratedAt }
		golden(t, fmt.Sprintf("fixture-%d", w), m.View())
	}
}

// Acceptance 4: eight projects group and count as the screenshot does; a
// group over the fold shows "Show more".
func TestGroupingEightProjects(t *testing.T) {
	m, _ := newTestModel(eightProjects(), 120, 60)
	v := m.View()
	for _, want := range []string{"/p/scratch  5", "/p/spike/main-repo  2", "/p/spike/wt-a  3", "/p/amq-squad  7", "/p/ios_organizer  1", "/p/private/notify-probe  1"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing group header %q", want)
		}
	}
	if n := strings.Count(v, "Show more"); n != 1 {
		t.Errorf("Show more appears %d times, want 1 (only amq-squad has 7 rows)", n)
	}
	if !strings.Contains(v, "Show more (2)") {
		t.Errorf("fold must name the 2 hidden rows")
	}
	if m.counts[TabAll] != 21 || m.counts[TabWorking] != 1 || m.counts[TabInactive] != 20 {
		t.Errorf("counts = %v", m.counts)
	}
	golden(t, "eight-projects-120", v)
	m80, _ := newTestModel(eightProjects(), 80, 40)
	golden(t, "eight-projects-80", m80.View())
}

func TestFoldExpandsOnEnter(t *testing.T) {
	m, _ := newTestModel(Fleet{Squads: []Status{testSquad("/p/a", repeat(BucketWorking, 8)...)}}, 120, 40)
	rowsShown := func() int { return strings.Count(m.View(), "● task t") }
	before := rowsShown()
	press(m, "j", "j", "j", "j", "j") // down onto the fold
	if !strings.Contains(m.View(), "› Show more") && !strings.Contains(m.View(), "›   Show more") {
		t.Fatalf("cursor not on the fold:\n%s", m.View())
	}
	press(m, "enter")
	if after := rowsShown(); after <= before {
		t.Fatalf("fold did not expand: %d -> %d", before, after)
	}
	if strings.Contains(m.View(), "Show more") {
		t.Fatal("fold still shown after expanding")
	}
}

func repeat(s string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = s
	}
	return out
}

// Acceptance 5: every field of amendment 24 shows; a null token count shows a
// dash.
func TestDetailsPaneFields(t *testing.T) {
	f, _ := Parse(goldenFixture)
	m, _ := newTestModel(f, 140, 40)
	// rows are ordered needs_you (t2), working (t1), ready (impl-2); select t1.
	press(m, "down")
	v := m.View()
	for _, want := range []string{"Task details", "Fix the parser", "Working", "Model: sonnet", "Reasoning: medium",
		"/Users/owner/code/app", "Tokens: 18.2K in · 2210 out", "starting t1: parser fix", "Prompt", "running"} {
		if !strings.Contains(v, want) {
			t.Errorf("details missing %q\n%s", want, v)
		}
	}
	press(m, "down") // impl-2: tokens null
	v = m.View()
	for _, want := range []string{"Seat details", "Model: gpt-5.4", "Tokens: - in · - out", "idle"} {
		if !strings.Contains(v, want) {
			t.Errorf("seat details missing %q\n%s", want, v)
		}
	}
	press(m, "up", "up") // the gate task
	if v = m.View(); !strings.Contains(v, "g-release-1 tag open") {
		t.Errorf("gate not shown for the needs-you task:\n%s", v)
	}
}

// Acceptance 6: each key has a test.
func TestKeyArrowsMove(t *testing.T) {
	m, _ := newTestModel(Fleet{Squads: []Status{testSquad("/p/a", BucketWorking, BucketWorking, BucketWorking)}}, 120, 30)
	sel := func() string { r, _ := m.selected(); return r.Title }
	first := sel()
	press(m, "down")
	if sel() == first {
		t.Fatal("down did not move")
	}
	press(m, "up")
	if sel() != first {
		t.Fatal("up did not move back")
	}
	press(m, "up") // at the top: stays
	if sel() != first {
		t.Fatal("up at the top moved")
	}
	press(m, "G")
	if r, _ := m.selected(); r.Task.ID != "t3" {
		t.Fatalf("G selected %s", r.Task.ID)
	}
	press(m, "g")
	if sel() != first {
		t.Fatal("g did not go to the top")
	}
}

func TestKeyTabCyclesFilters(t *testing.T) {
	m, _ := newTestModel(Fleet{Squads: []Status{testSquad("/p/a", BucketNeedsYou, BucketWorking, BucketReady, BucketInactive)}}, 120, 30)
	want := []Tab{TabNeedsYou, TabWorking, TabReady, TabInactive, TabAll}
	for _, w := range want {
		press(m, "tab")
		if m.tab != w {
			t.Fatalf("tab -> %d, want %d", m.tab, w)
		}
		if w != TabAll {
			if r, ok := m.selected(); !ok || r.Bucket != tabBucket[w] {
				t.Fatalf("tab %d shows %+v", w, r)
			}
		}
	}
	press(m, "shift+tab")
	if m.tab != TabInactive {
		t.Fatalf("shift+tab from All = %d", m.tab)
	}
	if !strings.Contains(m.View(), "[Inactive 1]") {
		t.Fatalf("active tab not marked:\n%s", m.View())
	}
}

func TestKeyEscGoesBack(t *testing.T) {
	m, _ := newTestModel(Fleet{Squads: []Status{testSquad("/p/a", BucketNeedsYou, BucketWorking)}}, 120, 30)
	press(m, "tab")
	press(m, "esc")
	if m.tab != TabAll {
		t.Fatal("esc on a filter tab must go back to All")
	}
	press(m, "?")
	if m.mode != modeHelp || !strings.Contains(m.View(), "Keys") {
		t.Fatal("? did not open help")
	}
	press(m, "esc")
	if m.mode != modeList {
		t.Fatal("esc did not close help")
	}
	press(m, "n", "esc")
	if m.mode != modeList {
		t.Fatal("esc did not cancel the new form")
	}
}

// A hostile status document naming another program: n refuses, the footer
// shows the named error, and nothing runs.
func TestNewRefusesForeignProgram(t *testing.T) {
	s := testSquad("/p/a", BucketWorking)
	s.Actions.NewTask.Argv = []string{"/tmp/x/amq-squad", "task", "add"}
	m, fr := newTestModel(Fleet{Squads: []Status{s}}, 120, 30)
	before := len(fr.calls)
	press(m, "n")
	if m.mode != modeList || !strings.Contains(m.View(), "runner: program not allowed: /tmp/x/amq-squad") {
		t.Fatalf("mode=%d note=%q", m.mode, m.note)
	}
	if len(fr.calls) != before {
		t.Fatal("something ran")
	}
}

func TestKeyQuit(t *testing.T) {
	m, _ := newTestModel(Fleet{Squads: []Status{testSquad("/p/a", BucketWorking)}}, 120, 30)
	if _, cmd := m.Update(keyMsg("q")); cmd == nil || fmt.Sprintf("%T", cmd()) != "tea.QuitMsg" {
		t.Fatal("q must quit")
	}
	if _, cmd := m.Update(keyMsg("ctrl+c")); cmd == nil {
		t.Fatal("ctrl+c must quit")
	}
}

func TestKeyNRunsContractArgv(t *testing.T) {
	m, fr := newTestModel(Fleet{Squads: []Status{testSquad("/p/a", BucketWorking)}}, 120, 30)
	press(m, "n")
	if m.mode != modeNew {
		t.Fatal("n did not open the form")
	}
	press(m, "enter") // empty required title is refused
	if m.mode != modeNew || !strings.Contains(m.note, "title is required") {
		t.Fatalf("empty title accepted: mode=%d note=%q", m.mode, m.note)
	}
	for _, r := range "Fix it" {
		press(m, string(r))
	}
	press(m, "enter") // to class
	press(m, "right") // review
	_, cmd := m.Update(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("no command on submit")
	}
	msg := cmd()
	if nt, ok := msg.(newTaskMsg); !ok || nt.err != nil {
		t.Fatalf("result %#v", msg)
	}
	got := strings.Join(fr.calls[len(fr.calls)-1], " ")
	if want := "amq-squad task add --root /amq//p/a --title=Fix it --class=review"; got != want {
		t.Fatalf("argv = %q, want %q", got, want)
	}
	// the result triggers a refresh
	if _, cmd := m.Update(msg); cmd == nil {
		t.Fatal("no refresh after new task")
	}
}

func TestKeyEnterOpensLeadConversation(t *testing.T) {
	m, fr := newTestModel(Fleet{Squads: []Status{testSquad("/p/a", BucketWorking)}}, 120, 30)
	fr.thread = `[{"id":"m1","from":"lead","to":["user"],"created":"2026-10-24T11:58:00Z","body":"need your call"}]`
	_, cmd := m.Update(keyMsg("enter"))
	if m.mode != modeConversation || cmd == nil {
		t.Fatal("enter did not open the conversation")
	}
	m.Update(cmd())
	last := strings.Join(fr.calls[len(fr.calls)-1], " ")
	if want := "amq thread --id p2p/lead__user --me user --root /amq//p/a --json --include-body --limit 60"; last != want {
		t.Fatalf("argv = %q", last)
	}
	if v := m.View(); !strings.Contains(v, "Conversation with lead") || !strings.Contains(v, "need your call") {
		t.Fatalf("conversation view:\n%s", v)
	}
	// staged confirm: first enter stages, second sends
	for _, r := range "yes" {
		press(m, string(r))
	}
	if _, cmd := m.Update(keyMsg("enter")); cmd != nil {
		t.Fatal("first enter must only stage")
	}
	if !strings.Contains(m.View(), "enter again to send to lead on p2p/lead__user") {
		t.Fatalf("staged line missing:\n%s", m.View())
	}
	_, cmd = m.Update(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("second enter must send")
	}
	m.Update(cmd())
	sent := strings.Join(fr.calls[len(fr.calls)-1], " ")
	if want := "amq send --root /amq//p/a --me user --to lead --thread p2p/lead__user --body yes"; sent != want {
		t.Fatalf("send argv = %q", sent)
	}
	press(m, "esc")
	if m.mode != modeList {
		t.Fatal("esc did not leave the conversation")
	}
}

// With no lead in the status, enter opens nothing and the details pane says why.
func TestEnterWithoutLead(t *testing.T) {
	s := testSquad("/p/a", BucketWorking)
	s.LeadHandle = ""
	m, _ := newTestModel(Fleet{Squads: []Status{s}}, 120, 30)
	if !strings.Contains(m.View(), "no lead seat in status") {
		t.Fatalf("details pane lacks the reason:\n%s", m.View())
	}
	press(m, "enter")
	if m.mode != modeList || m.note != "no lead seat in status" {
		t.Fatalf("mode=%d note=%q", m.mode, m.note)
	}
	// a seat with role lead is the lead
	s.Seats = append(s.Seats, Seat{Handle: "boss", Role: "lead"})
	if Lead(s) != "boss" {
		t.Fatal("role lead not found")
	}
}

// lead_handle in the status wins; the test uses a local document, not the
// vendored fixture.
func TestLeadHandleField(t *testing.T) {
	doc := strings.Replace(string(goldenFixture), `"lead_handle": "lead",`, `"lead_handle": "cto",`, 1)
	f, err := Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if got := Lead(f.Squads[0]); got != "cto" {
		t.Fatalf("lead = %q", got)
	}
}

func TestRefreshKeepsSelectionAndReportsErrors(t *testing.T) {
	m, _ := newTestModel(Fleet{Squads: []Status{testSquad("/p/a", BucketWorking, BucketWorking)}}, 120, 30)
	press(m, "down")
	before, _ := m.selected()
	m.Update(statusMsg{fleet: Fleet{Squads: []Status{testSquad("/p/a", BucketWorking, BucketWorking)}}})
	after, _ := m.selected()
	if after.Task.ID != before.Task.ID {
		t.Fatal("selection lost on refresh")
	}
	m.Update(statusMsg{err: fmt.Errorf("amq-squad: not found")})
	if v := m.View(); !strings.Contains(v, "status unavailable") || !strings.Contains(v, "task t1") {
		t.Fatalf("error must keep the last data and say so:\n%s", v)
	}
}

func TestEmptyStates(t *testing.T) {
	m, _ := newTestModel(Fleet{Squads: []Status{testSquad("/p/a")}}, 120, 30)
	if !strings.Contains(m.View(), "no seats or tasks in status") {
		t.Fatal("empty All")
	}
	press(m, "tab")
	if !strings.Contains(m.View(), "nothing in Needs you") {
		t.Fatal("empty tab")
	}
}

// Acceptance 8: 50 seats and 200 tasks render in under 100 ms per frame.
func TestFrameTimeUnder100ms(t *testing.T) {
	f := bigFleet(10, 50, 200)
	m, _ := newTestModel(f, 120, 40)
	const frames = 50
	start := time.Now()
	for i := 0; i < frames; i++ {
		m.Update(statusMsg{fleet: f}) // refresh: rows, counts, items rebuilt
		press(m, "down")
		_ = m.View()
	}
	per := time.Since(start) / frames
	t.Logf("refresh + key + View per frame: %v (rows=%d)", per, len(m.rows))
	if per > 100*time.Millisecond {
		t.Fatalf("frame %v exceeds 100ms", per)
	}
}

func BenchmarkFrame(b *testing.B) {
	f := bigFleet(10, 50, 200)
	m, _ := newTestModel(f, 120, 40)
	b.Run("refresh+view", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			m.Update(statusMsg{fleet: f})
			_ = m.View()
		}
	})
	b.Run("view", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = m.View()
		}
	})
}
