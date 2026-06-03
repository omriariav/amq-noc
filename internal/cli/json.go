package cli

import (
	"encoding/json"
	"io"
	"os"
)

// JSONSchemaVersion is the envelope schema version for amq-squad's
// machine-readable read outputs (brief, brief seed, status, history, team show /
// up --dry-run, team init --dry-run, version, roles, team profiles). Bump only
// on a breaking change to the envelope shape; field additions inside `data` do
// not require a bump.
const JSONSchemaVersion = 1

// jsonEnvelope is the wrapping shape every --json output uses. Kind names
// the produced view (e.g. "brief", "brief_seed", "status", "history", "team_plan",
// "team_profile_plan", "version", "roles", "team_profiles",
// "brief_candidate"); Data is the kind-specific payload. Stdout receives the
// envelope alone; diagnostics stay on stderr.
type jsonEnvelope struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	Data          any    `json:"data"`
}

// jsonEncoder returns a stdout JSON encoder with readable indentation.
func jsonEncoder() *json.Encoder {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc
}

// printJSONEnvelope writes a versioned envelope to stdout.
func printJSONEnvelope(kind string, data any) error {
	return writeJSONEnvelope(os.Stdout, kind, data)
}

func writeJSONEnvelope(w io.Writer, kind string, data any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(jsonEnvelope{SchemaVersion: JSONSchemaVersion, Kind: kind, Data: data})
}
