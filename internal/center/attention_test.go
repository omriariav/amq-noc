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
