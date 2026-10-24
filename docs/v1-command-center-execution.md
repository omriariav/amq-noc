# amq-noc 1.0 command center: execution log

Brief 4-4 of amq-squad 3.0. Work branch `v1-command-center` from main 9bedf33.
Contract: amq-squad `v3` at 5509f31e (`docs/status-contract.md`, schema 1).
Gate evidence for every commit is under `docs/v1-command-center-evidence/`.

## Commit 1: contract, rows and the call surface

Built (new package `internal/center`, no UI yet):

- `contract.go`: the schema 1 document types and `Parse`. Reads one squad's
  `status --json` or a fleet document of the form `{"squads":[<status>...]}`
  (the fleet header shape is brief 4-3's and is not merged; this form is an
  assumption, see open rulings). Refuses a schema_version other than 1.
- `model.go`: rows (one per task, one per seat that holds no task), tab counts,
  tab filters, grouping by project path, status labels, relative time. Buckets
  come from the contract's `bucket` and `bucket_marker` fields; nothing is
  reclassified.
- `runner.go`, `ops.go`: the only reach outside the process. `Client` has four
  calls: the status read (`amq-squad status --json` or `--fleet --json`), the
  contract's `new_task` argv plus the flag/value of each input the user gave,
  and the two AMQ primitives of the lead conversation (`amq thread`,
  `amq send`). argv only, never a shell; the new-task action is validated
  (bare program name, no shell interpreter, `--flag` inputs, choices).
- The vendored fixture `internal/center/testdata/status-v1.golden.json`
  (byte for byte from amq-squad's `internal/status/testdata`) and its committed
  sha256 file; a test compares the embedded copy to the digest.

Tests: fixture digest, parse (one squad, fleet, wrong schema, null), counts
(one item per bucket, All equals the sum), no reclassification, unknown as
Inactive with a marker, seat rows versus task rows, relative time, and
acceptance 1 (fake PATH run logging every argv; static check that the package
opens no file, names no `.amq-squad` path and starts processes only in
`runner.go`).

Files deleted: none yet (the delete list is the halfway checkpoint).
Behavior changes: none yet (the package is not wired in).
