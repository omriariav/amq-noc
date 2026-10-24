package center

import (
	"strings"
	"testing"
)

// Commit 14: n on a row that belongs to no squad refuses; a fleet with only
// waiting entries is valid.
func TestNewTaskRefusedOnRowsWithoutSquad(t *testing.T) {
	for name, keys := range map[string][]string{
		"operator": {"g"},
		"waiting":  {"G"},
	} {
		m, _ := newTestModel(mustParse(t, string(goldenFleet)), 120, 40)
		press(m, keys...)
		r, ok := m.selected()
		if !ok || (name == "operator" && r.Section != sectionOperator) || (name == "waiting" && r.Waiting == nil) {
			t.Fatalf("%s: wrong row %+v", name, r)
		}
		press(m, "n")
		if m.mode != modeList || !strings.Contains(m.note, "no new task for this row: it belongs to no squad") {
			t.Errorf("%s: mode %v note %q", name, m.mode, m.note)
		}
	}
}

func TestNewTaskRefusedOnFleetFold(t *testing.T) {
	m, _ := newTestModel(mustParse(t, string(goldenFleet)), 120, 40)
	m.items = append(m.items, item{kind: itemFold, group: "x"})
	m.cursor = len(m.items) - 1
	press(m, "n")
	if m.mode != modeList || !strings.Contains(m.note, "fold is not a squad row") {
		t.Fatalf("mode %v note %q", m.mode, m.note)
	}
}

func TestFleetWithOnlyWaitingParses(t *testing.T) {
	doc := `{"schema_version":1,"kind":"fleet","squads":[],"waiting":[{"squad":"docs","project_path":"/p/docs","wait_reason":"fleet_cap"}]}`
	f := mustParse(t, doc)
	m, _ := newTestModel(f, 120, 30)
	v := m.View()
	if !strings.Contains(v, "Waiting  1") || strings.Contains(strings.ToLower(v), "error") {
		t.Fatalf("frame:\n%s", v)
	}
	if _, err := Parse([]byte(`{"schema_version":1,"kind":"fleet","squads":[]}`)); err == nil {
		t.Fatal("empty fleet parsed")
	}
}
