package center

import (
	_ "embed"
	"strings"
	"testing"
)

// launchFailSample is real `amq-squad status --json` output from the
// amq-squad 3.0.1 tree: seat rev-1 failed to launch (a seat_launch_failed
// item), task t1 waits on the same reason (a task_waiting item), and a
// usage_limit item names no seat and no task. Test input, not a golden.
//
//go:embed testdata/status-301-launchfail.json
var launchFailSample []byte

// selectRow puts the cursor on the first row that match accepts.
func selectRow(t *testing.T, m *Model, match func(Row) bool) {
	t.Helper()
	for i, it := range m.items {
		if it.kind == itemRow && match(it.row) {
			m.cursor = i
			m.fix()
			return
		}
	}
	t.Fatalf("no row matched")
}

func details(m *Model) string { return strings.Join(m.detailsLines(80), " ") }

func TestSeatRowShowsLaunchFailureAttention(t *testing.T) {
	m, _ := newTestModel(mustParse(t, string(launchFailSample)), 140, 40)
	selectRow(t, m, func(r Row) bool { return !r.IsTask() && r.Seat != nil && r.Seat.Handle == "rev-1" })
	d := details(m)
	for _, want := range []string{"Attention (1)", "seat_launch_failed", "medium", "stage apply_failed",
		"binding_present_without_resumable_conversation", "the session binding is present"} {
		if !strings.Contains(d, want) {
			t.Errorf("seat details missing %q:\n%s", want, d)
		}
	}
	if strings.Contains(d, "task_waiting") {
		t.Errorf("a task's item shows on the seat row:\n%s", d)
	}
}

func TestTaskRowShowsItsOwnAttention(t *testing.T) {
	m, _ := newTestModel(mustParse(t, string(launchFailSample)), 140, 40)
	selectRow(t, m, func(r Row) bool { return r.IsTask() && r.Task.ID == "t1" })
	d := details(m)
	if !strings.Contains(d, "task_waiting") || !strings.Contains(d, "t1 waits") {
		t.Errorf("task details miss its task_waiting item:\n%s", d)
	}
	if strings.Contains(d, "seat_launch_failed") {
		t.Errorf("a seat's item shows on the task row:\n%s", d)
	}
}

func TestSquadAttentionOnEveryRowOfTheSquad(t *testing.T) {
	m, _ := newTestModel(mustParse(t, string(launchFailSample)), 140, 40)
	for _, match := range []func(Row) bool{
		func(r Row) bool { return r.IsTask() },
		func(r Row) bool { return !r.IsTask() },
	} {
		selectRow(t, m, match)
		d := details(m)
		if !strings.Contains(d, "Squad attention (1)") || !strings.Contains(d, "usage_limit: a usage-limit event stopped new seats") {
			t.Errorf("squad attention missing:\n%s", d)
		}
	}
}

func TestSquadAttentionFoldsExtraItems(t *testing.T) {
	f := mustParse(t, string(launchFailSample))
	f.Squads[0].Attention = append(f.Squads[0].Attention, Attention{ID: "alert:x", Kind: "owner_alert", Subject: "second"})
	m, _ := newTestModel(f, 140, 40)
	selectRow(t, m, func(r Row) bool { return r.IsTask() })
	if d := details(m); !strings.Contains(d, "Squad attention (2)") || !strings.Contains(d, "(+1 more)") {
		t.Errorf("squad attention fold missing:\n%s", d)
	}
}

// A gate item stays in the Gate section only; it is not repeated as attention.
func TestGateItemNotRepeatedAsAttention(t *testing.T) {
	m, _ := newTestModel(mustParse(t, string(goldenFixture)), 140, 40)
	selectRow(t, m, func(r Row) bool { return r.IsTask() && m.gateFor(r) != nil })
	d := details(m)
	if !strings.Contains(d, "g-release-1 tag open") {
		t.Fatalf("gate missing:\n%s", d)
	}
	for _, a := range m.attentionFor(m.items[m.cursor].row) {
		if a.GateID != "" {
			t.Errorf("gate item %s listed as attention", a.ID)
		}
	}
}

// A seat-level item (no task id) for a seat that holds a task shows on that
// task's row: the busy seat has no row of its own.
func TestBusySeatItemShowsOnItsTaskRow(t *testing.T) {
	f := mustParse(t, string(goldenFixture))
	s := &f.Squads[0]
	var task *Task
	for i := range s.Tasks {
		if s.Tasks[i].LeaseHolder != "" {
			task = &s.Tasks[i]
			break
		}
	}
	if task == nil {
		t.Fatal("fixture has no leased task")
	}
	s.Attention = append(s.Attention, Attention{ID: "q1", Kind: "question", Severity: "high",
		Subject: "busy seat asks a question", Seat: task.LeaseHolder})
	m, _ := newTestModel(f, 140, 40)
	selectRow(t, m, func(r Row) bool { return r.IsTask() && r.Task.ID == task.ID })
	if d := details(m); !strings.Contains(d, "question") || !strings.Contains(d, "busy seat asks a question") {
		t.Fatalf("busy seat's item missing on its task row:\n%s", d)
	}
	// every item of the document shows on some row
	assertEveryItemShown(t, m)
}

// A seat-level item for a seat the status does not list shows as squad
// attention, not nowhere.
func TestItemOfUnlistedSeatShowsAsSquadAttention(t *testing.T) {
	f := mustParse(t, string(launchFailSample))
	f.Squads[0].Attention = append(f.Squads[0].Attention, Attention{ID: "x", Kind: "seat_launch_failed",
		Subject: "seat ghost did not launch", Seat: "ghost"})
	m, _ := newTestModel(f, 140, 40)
	selectRow(t, m, func(r Row) bool { return r.IsTask() })
	if d := details(m); !strings.Contains(d, "Squad attention (2)") {
		t.Fatalf("unlisted seat's item missing:\n%s", d)
	}
	assertEveryItemShown(t, m)
}

// assertEveryItemShown checks that each non-gate attention item of every
// squad appears in the details pane of at least one row.
func assertEveryItemShown(t *testing.T, m *Model) {
	t.Helper()
	for si, s := range m.fleet.Squads {
		for _, a := range s.Attention {
			if a.GateID != "" {
				continue
			}
			found := false
			for _, it := range m.items {
				if it.kind != itemRow || it.row.Squad != si {
					continue
				}
				for _, x := range append(m.attentionFor(it.row), m.squadAttention(it.row)...) {
					found = found || x.ID == a.ID
				}
			}
			if !found {
				t.Errorf("attention item %s (%s) shows on no row", a.ID, a.Kind)
			}
		}
	}
}

func TestEveryItemShownInSamples(t *testing.T) {
	for _, doc := range [][]byte{goldenFixture, goldenFleet, launchFailSample} {
		m, _ := newTestModel(mustParse(t, string(doc)), 140, 40)
		assertEveryItemShown(t, m)
	}
}

// The narrow pane is a few lines high: one long launch failure must still
// leave room for the squad attention line.
func TestNarrowPaneKeepsSquadAttention(t *testing.T) {
	m, _ := newTestModel(mustParse(t, string(launchFailSample)), 80, 30)
	selectRow(t, m, func(r Row) bool { return !r.IsTask() && r.Seat != nil && r.Seat.Handle == "rev-1" })
	v := m.View()
	for _, want := range []string{"seat_launch_failed", "Squad attention (1)", "usage_limit"} {
		if !strings.Contains(v, want) {
			t.Errorf("narrow frame lacks %q:\n%s", want, v)
		}
	}
}
