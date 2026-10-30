package center

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"strings"
	"testing"
)

// goldenFixture is the vendored copy of amq-squad's
// internal/status/testdata/status-v1.golden.json, byte for byte.
//
//go:embed testdata/status-v1.golden.json
var goldenFixture []byte

//go:embed testdata/status-v1.golden.json.sha256
var goldenSHA string

// The embedded copy equals the committed digest of amq-squad's fixture. When
// amq-squad changes the fixture, re-vendor the file and the digest together.
func TestVendoredFixtureMatchesCommittedSHA(t *testing.T) {
	sum := sha256.Sum256(goldenFixture)
	if got, want := hex.EncodeToString(sum[:]), strings.TrimSpace(goldenSHA); got != want {
		t.Fatalf("vendored fixture sha256 = %s, committed %s", got, want)
	}
}

// goldenFleet is the vendored copy of amq-squad's
// internal/status/testdata/status-fleet-v1.golden.json, byte for byte.
//
//go:embed testdata/status-fleet-v1.golden.json
var goldenFleet []byte

//go:embed testdata/status-fleet-v1.golden.json.sha256
var goldenFleetSHA string

func TestVendoredFleetFixtureMatchesCommittedSHA(t *testing.T) {
	sum := sha256.Sum256(goldenFleet)
	if got, want := hex.EncodeToString(sum[:]), strings.TrimSpace(goldenFleetSHA); got != want {
		t.Fatalf("vendored fleet fixture sha256 = %s, committed %s", got, want)
	}
}

func TestParseGolden(t *testing.T) {
	f, err := Parse(goldenFixture)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Squads) != 1 || len(f.Squads[0].Seats) != 2 || len(f.Squads[0].Tasks) != 2 {
		t.Fatalf("fixture shape: %+v", f)
	}
	if f.Squads[0].Actions.NewTask.ID != "task.new" {
		t.Fatalf("new_task action missing")
	}
}

func TestParseRefusesOtherSchema(t *testing.T) {
	if _, err := Parse([]byte(`{"schema_version":2}`)); err == nil {
		t.Fatal("schema 2 must be refused")
	}
	if _, err := Parse([]byte(`null`)); err == nil {
		t.Fatal("null must be refused")
	}
}

func TestParseFleetForm(t *testing.T) {
	f, err := Parse(goldenFleet)
	if err != nil || len(f.Squads) != 1 {
		t.Fatalf("fleet parse: %v %+v", err, len(f.Squads))
	}
	if f.Operator == nil || f.Operator.Handle != "user" || f.Operator.Role != "operator" {
		t.Fatalf("operator = %+v", f.Operator)
	}
	if len(f.Waiting) != 1 || f.Waiting[0].WaitReason != "fleet_cap" || f.Waiting[0].Squad != "docs" {
		t.Fatalf("waiting = %+v", f.Waiting)
	}
	if f.Squads[0].LeadHandle != "" {
		t.Fatalf("fleet golden lead_handle is null, got %q", f.Squads[0].LeadHandle)
	}
}
