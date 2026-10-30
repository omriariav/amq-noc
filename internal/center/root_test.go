package center

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// --root DIR passes through as `amq-squad status --root DIR --json`.
func TestStatusArgvPassesRoot(t *testing.T) {
	logPath := fakeBin(t, string(goldenFixture))
	if _, err := (Client{Runner: ExecRunner{}, Root: "/srv/amq/squad"}).Status(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := readLog(t, logPath); len(got) != 1 || got[0] != "amq-squad status --root /srv/amq/squad --json" {
		t.Fatalf("calls = %v", got)
	}
	// the fleet form never carries a root
	if got := StatusArgv(true, "/srv/amq/squad"); strings.Join(got, " ") != "amq-squad status --fleet --json" {
		t.Fatalf("fleet argv = %v", got)
	}
}

// amq-squad 3.0's own output for an empty fleet.yaml renders as an empty
// board, not an error.
func TestEmptyFleetRendersEmptyBoard(t *testing.T) {
	doc := `{"schema_version":1,"kind":"fleet","producer":"amq-squad","producer_version":"v3.0.0",` +
		`"generated_at":"2026-10-30T20:14:01Z","squads":[],"waiting":[],"operator":null}`
	f, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("empty fleet refused: %v", err)
	}
	m, _ := newTestModel(f, 120, 20)
	if v := m.View(); !strings.Contains(v, "no squads in the fleet") || !strings.Contains(v, "[All 0]") {
		t.Fatalf("frame:\n%s", v)
	}
	// a one-squad document with no rows keeps its own message
	m, _ = newTestModel(Fleet{Squads: []Status{testSquad("/p/a")}}, 120, 20)
	if v := m.View(); !strings.Contains(v, "no seats or tasks in status") {
		t.Fatalf("frame:\n%s", v)
	}
}

type errRunner struct{ err error }

func (r errRunner) Run(context.Context, []string) ([]byte, error) { return nil, r.err }

// A failed status read carries the hint; the original error still matches.
func TestStatusErrorCarriesHint(t *testing.T) {
	base := errors.New("amq-squad: error: status: no store root")
	_, err := (Client{Runner: errRunner{base}, Hint: "pass --root DIR"}).Status(context.Background())
	if err == nil || !errors.Is(err, base) || !strings.Contains(err.Error(), "(pass --root DIR)") {
		t.Fatalf("err = %v", err)
	}
	if _, err := (Client{Runner: errRunner{base}}).Status(context.Background()); err == nil || err.Error() != base.Error() {
		t.Fatalf("no hint: err = %v", err)
	}
}

// A one-squad document that grows a waiting or operator key is still read as
// one squad; only kind "fleet" or squads[] makes a fleet.
func TestOneSquadWithFleetKeysStaysOneSquad(t *testing.T) {
	doc := strings.Replace(string(goldenFixture), "{", `{"waiting":[],"operator":null,`, 1)
	f, err := Parse([]byte(doc))
	if err != nil || f.IsFleet || len(f.Squads) != 1 || len(f.Squads[0].Seats) == 0 {
		t.Fatalf("err=%v fleet=%v squads=%d", err, f.IsFleet, len(f.Squads))
	}
}
