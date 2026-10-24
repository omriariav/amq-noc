package center

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Item 1: a status document that names python3 -c must not run it, in the
// runner or through the new-task action.
func TestRunnerRefusesPython3DashC(t *testing.T) {
	logPath := fakeBin(t, string(goldenFixture))
	py := "#!/bin/sh\necho \"python3 $*\" >> '" + logPath + "'\necho executed\n"
	if err := os.WriteFile(filepath.Join(filepath.Dir(logPath), "python3"), []byte(py), 0o755); err != nil {
		t.Fatal(err)
	}
	argv := []string{"python3", "-c", "print('executed')"}
	if out, err := (ExecRunner{}).Run(context.Background(), argv); err == nil || len(out) != 0 ||
		!strings.Contains(err.Error(), "runner: program not allowed: python3") {
		t.Fatalf("runner: out=%q err=%v", out, err)
	}
	a := Action{ID: "task.new", Label: "new task", Argv: argv}
	if _, err := (Client{Runner: ExecRunner{}}).NewTask(context.Background(), a, nil); err == nil {
		t.Fatal("action with python3 -c ran")
	}
	if got := readLog(t, logPath); len(got) != 0 {
		t.Fatalf("python3 ran: %v", got)
	}
}

// Item 2: an action with no inputs (null or empty) does not panic on n or on
// n then esc; it runs without a form.
func TestZeroInputActionDoesNotPanic(t *testing.T) {
	for name, inputs := range map[string][]ActionInput{"null": nil, "empty": {}} {
		t.Run(name, func(t *testing.T) {
			s := testSquad("/p/a", BucketWorking)
			s.Actions.NewTask.Inputs = inputs
			m, fr := newTestModel(Fleet{Squads: []Status{s}}, 120, 30)
			_, cmd := m.Update(keyMsg("n"))
			if cmd == nil || m.mode != modeList {
				t.Fatalf("zero-input action must run without a form (mode %d)", m.mode)
			}
			if msg := cmd(); msg == nil {
				t.Fatal("no result")
			}
			if got := strings.Join(fr.calls[len(fr.calls)-1], " "); got != "amq-squad task add --root /amq//p/a" {
				t.Fatalf("argv = %q", got)
			}
			press(m, "esc") // the old crash: n then esc
			// and a form forced open on an action without inputs closes on any key
			m.form = formState{action: s.Actions.NewTask, values: map[string]string{}}
			m.mode = modeNew
			press(m, "esc")
			m.mode = modeNew
			press(m, "x", "enter", "left")
		})
	}
}

// An unknown bucket does not refuse the document: the row is shown as
// inactive with the unknown marker, and a warning names the row and the value.
func TestParseMapsUnknownBucketToInactiveWithWarning(t *testing.T) {
	doc := strings.Replace(string(goldenFixture), `"bucket": "needs_you",
      "bucket_marker": "",
      "pool": null`, `"bucket": "surprise",
      "bucket_marker": "",
      "pool": null`, 1)
	if doc == string(goldenFixture) {
		t.Fatal("test document not changed")
	}
	doc = strings.Replace(doc, `"bucket": "ready"`, `"bucket": "later"`, 1)
	f, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("one bad row refused the document: %v", err)
	}
	s := f.Squads[0]
	if s.Tasks[1].Bucket != BucketInactive || s.Tasks[1].BucketMarker != MarkerUnknown {
		t.Fatalf("task t2 = %q/%q", s.Tasks[1].Bucket, s.Tasks[1].BucketMarker)
	}
	if s.Seats[1].Bucket != BucketInactive || s.Seats[1].BucketMarker != MarkerUnknown {
		t.Fatalf("seat impl-2 = %q/%q", s.Seats[1].Bucket, s.Seats[1].BucketMarker)
	}
	w := strings.Join(s.Warnings, "\n")
	for _, want := range []string{"task t2", `"surprise"`, "seat impl-2", `"later"`} {
		if !strings.Contains(w, want) {
			t.Errorf("warnings lack %q: %s", want, w)
		}
	}
	// the rows land in a tab and All is still the sum
	c := Counts(BuildRows(f))
	if c[TabAll] != c[TabNeedsYou]+c[TabWorking]+c[TabReady]+c[TabInactive] {
		t.Fatalf("counts %v", c)
	}
	// in a fleet, the bad squad does not blank the good one
	fleet := `{"schema_version":1,"kind":"fleet","squads":[` + doc + `,` + string(goldenFixture) + `]}`
	if ff, err := Parse([]byte(fleet)); err != nil || len(ff.Squads) != 2 {
		t.Fatalf("fleet with a bad row: %v", err)
	}
}

// Item 4: the fleet envelope is validated before its squads.
func TestParseFleetEnvelopeValidation(t *testing.T) {
	one := string(goldenFixture)
	for name, doc := range map[string]string{
		"envelope schema 2":   `{"schema_version":2,"kind":"fleet","squads":[` + one + `]}`,
		"envelope no schema":  `{"kind":"fleet","squads":[` + one + `]}`,
		"empty squads":        `{"schema_version":1,"kind":"fleet","squads":[]}`,
		"bare empty squads":   `{"squads":[]}`,
		"inner schema 1 only": `{"schema_version":2,"kind":"fleet","squads":[{"schema_version":1}]}`,
	} {
		if _, err := Parse([]byte(doc)); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
	if _, err := Parse([]byte(`{"schema_version":1,"kind":"fleet","squads":[` + one + `]}`)); err != nil {
		t.Fatalf("valid fleet: %v", err)
	}
}

// Item 5: a staged send runs once however many times enter is pressed before
// the runner returns.
func TestStagedSendRunsOnce(t *testing.T) {
	m, fr := newTestModel(Fleet{Squads: []Status{testSquad("/p/a", BucketWorking)}}, 120, 30)
	_, cmd := m.Update(keyMsg("enter"))
	m.Update(cmd())
	for _, r := range "go" {
		press(m, string(r))
	}
	press(m, "enter") // stage
	_, send := m.Update(keyMsg("enter"))
	if send == nil {
		t.Fatal("second enter must send")
	}
	_, again1 := m.Update(keyMsg("enter"))
	_, again2 := m.Update(keyMsg("enter"))
	if again1 != nil || again2 != nil {
		t.Fatal("enter while a send is in flight must do nothing")
	}
	msg := send()
	sends := 0
	for _, c := range fr.calls {
		if c[0] == "amq" && c[1] == "send" {
			sends++
		}
	}
	if sends != 1 {
		t.Fatalf("amq send ran %d times", sends)
	}
	m.Update(msg) // result frees the composer
	if m.conv.sending {
		t.Fatal("still sending after the result")
	}
}

// Item 6: results of a conversation that is no longer open are discarded.
func TestStaleConversationResultsDropped(t *testing.T) {
	f := Fleet{Squads: []Status{testSquad("/p/a", BucketWorking), testSquad("/p/b", BucketWorking)}}
	m, fr := newTestModel(f, 120, 40)
	fr.thread = `[{"id":"a1","from":"lead","to":["user"],"created":"2026-10-24T11:58:00Z","body":"from A"}]`
	_, cmdA := m.Update(keyMsg("enter")) // conversation A
	if m.conv.root != "/amq//p/a" {
		t.Fatalf("root %s", m.conv.root)
	}
	press(m, "esc", "down", "down") // to squad B's row
	fr.thread = `[{"id":"b1","from":"lead","to":["user"],"created":"2026-10-24T11:59:00Z","body":"from B"}]`
	for m.items[m.cursor].kind != itemRow || m.items[m.cursor].row.Squad != 1 {
		press(m, "down")
	}
	_, cmdB := m.Update(keyMsg("enter")) // conversation B
	if m.conv.root != "/amq//p/b" {
		t.Fatalf("root %s", m.conv.root)
	}
	for _, r := range "draft" {
		press(m, string(r))
	}
	staleRead := cmdA() // A's read, answered late (it ran with A's snapshot of the thread)
	m.Update(staleRead)
	if len(m.conv.msgs) != 0 {
		t.Fatalf("A's messages landed in B: %+v", m.conv.msgs)
	}
	m.Update(sentMsg{gen: m.conv.gen - 1}) // A's send result
	if m.conv.body != "draft" {
		t.Fatalf("A's send cleared B's draft: %q", m.conv.body)
	}
	m.Update(cmdB()) // B's own read applies
	if len(m.conv.msgs) != 1 || m.conv.msgs[0].ID != "b1" {
		t.Fatalf("B's read lost: %+v", m.conv.msgs)
	}
}

// Item 7: RELEASING.md names the surviving command surface.
func TestReleasingDocsSurvivingSurface(t *testing.T) {
	b, err := os.ReadFile("../../RELEASING.md")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "amq-noc noc") {
		t.Fatal("RELEASING.md still requires `amq-noc noc`")
	}
}

// A zero-input new_task runs once however often n is pressed before it returns.
func TestZeroInputNewTaskNotDuplicated(t *testing.T) {
	s := testSquad("/p/a", BucketWorking)
	s.Actions.NewTask.Inputs = nil
	m, fr := newTestModel(Fleet{Squads: []Status{s}}, 120, 30)
	_, first := m.Update(keyMsg("n"))
	_, second := m.Update(keyMsg("n"))
	_, third := m.Update(keyMsg("n"))
	if first == nil || second != nil || third != nil {
		t.Fatalf("n while running must do nothing: %v %v %v", first != nil, second != nil, third != nil)
	}
	msg := first()
	adds := 0
	for _, c := range fr.calls {
		if len(c) > 2 && c[1] == "task" && c[2] == "add" {
			adds++
		}
	}
	if adds != 1 {
		t.Fatalf("task add ran %d times", adds)
	}
	m.Update(msg)
	if _, again := m.Update(keyMsg("n")); again == nil {
		t.Fatal("n must work again after the result")
	}
}

// A value that starts with a dash stays one argument and cannot be read as a flag.
func TestNewTaskArgvDashValue(t *testing.T) {
	a := testSquad("/p").Actions.NewTask
	argv, err := NewTaskArgv(a, map[string]string{"title": "--root=/etc", "class": "review"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"amq-squad", "task", "add", "--root", "/amq//p", "--title=--root=/etc", "--class=review"}
	if strings.Join(argv, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv = %q", argv)
	}
}

// Selection and the open conversation follow the squad's root, not its
// position in the fleet, when the order changes between refreshes.
func TestFleetReorderKeepsSelectionAndConversation(t *testing.T) {
	a, b := testSquad("/p/a", BucketWorking), testSquad("/p/b", BucketWorking)
	m, _ := newTestModel(Fleet{Squads: []Status{a, b}}, 120, 40)
	for m.items[m.cursor].kind != itemRow || m.items[m.cursor].row.Squad != 1 {
		press(m, "down")
	}
	m.Update(keyMsg("enter"))
	if m.conv.root != "/amq//p/b" {
		t.Fatalf("root %s", m.conv.root)
	}
	press(m, "esc")
	m.Update(statusMsg{fleet: Fleet{Squads: []Status{b, a}}}) // order swapped
	r, ok := m.selected()
	if !ok || r.Project != "/p/b" {
		t.Fatalf("selection jumped to %+v", r)
	}
	if m.conv.root != "/amq//p/b" {
		t.Fatalf("conversation root changed to %s", m.conv.root)
	}
}

func TestRepoDocsMarkedAndFixed(t *testing.T) {
	plan, err := os.ReadFile("../../PLAN.md")
	if err != nil || !strings.Contains(string(plan), "Superseded") || !strings.Contains(string(plan), "docs/v1-command-center-execution.md") {
		t.Fatalf("PLAN.md not marked superseded: %v", err)
	}
	rel, err := os.ReadFile("../../RELEASING.md")
	if err != nil || strings.Contains(string(rel), "1.x and beyond") {
		t.Fatalf("RELEASING.md still says 1.x needs /vN: %v", err)
	}
}
