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

// Item 3: an unknown bucket is an error naming the row and the value.
func TestParseRejectsUnknownBucket(t *testing.T) {
	doc := strings.Replace(string(goldenFixture), `"bucket": "needs_you",
      "bucket_marker": "",
      "pool": null`, `"bucket": "surprise",
      "bucket_marker": "",
      "pool": null`, 1)
	if doc == string(goldenFixture) {
		t.Fatal("test document not changed")
	}
	_, err := Parse([]byte(doc))
	if err == nil || !strings.Contains(err.Error(), "t2") || !strings.Contains(err.Error(), `"surprise"`) {
		t.Fatalf("err = %v", err)
	}
	seat := strings.Replace(string(goldenFixture), `"bucket": "ready"`, `"bucket": "later"`, 1)
	if _, err := Parse([]byte(seat)); err == nil || !strings.Contains(err.Error(), "impl-2") {
		t.Fatalf("seat err = %v", err)
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
