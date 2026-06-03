package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// testJSONEnvelope is a generic decoder shape for JSON-envelope tests.
type testJSONEnvelope[T any] struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	Data          T      `json:"data"`
}

// decodeJSONEnvelope parses an --json output buffer into a typed envelope.
// Failures call t.Fatal so the test body stays linear.
func decodeJSONEnvelope[T any](t *testing.T, raw string) testJSONEnvelope[T] {
	t.Helper()
	var env testJSONEnvelope[T]
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		t.Fatalf("decode JSON envelope: %v\nraw: %s", err, raw)
	}
	if env.SchemaVersion == 0 {
		t.Fatalf("envelope missing schema_version: %s", raw)
	}
	if strings.Contains(raw, "\n#") {
		// JSON outputs must not have human comment lines on stdout.
		t.Fatalf("envelope leaked human comment lines on stdout:\n%s", raw)
	}
	return env
}
