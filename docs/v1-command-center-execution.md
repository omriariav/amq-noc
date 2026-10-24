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

## Commit 2: the command center view, wired as the default

Built:

- `internal/center/ui.go`, `view.go`, `actions.go`: the bubbletea model.
  List grouped by project path with a count per group, one row per task (title,
  status, relative updated time), seats without a task as their own rows, a
  per-group "Show more (N)" fold after 5 rows (enter expands it), tabs with
  counts (All, Needs you, Working, Ready, Inactive) cycled by tab and
  shift-tab, the details pane (status with state and reason, model, reasoning
  effort, project path, tokens in and out with a dash for null, last message,
  prompt state, and the gate the attention queue ties to the row), side by side
  at 100 columns and above and stacked in compact form below.
- Keys: up/down (j/k), g/G, tab, shift-tab, enter, n, esc, ?, r, q, ctrl-c.
  enter on a row opens the AMQ conversation with the lead. The exact amq argv:
  read: `amq thread --id p2p/<a>__<b> --me <operator> --root <status root>
  --json --include-body --limit 60`; send: `amq send --root <root> --me
  <operator> --to <lead> --thread p2p/<a>__<b> --body <text>`, with a staged
  confirm (the first enter shows recipient and thread, the second sends). No
  amq-squad command is involved. The lead is the contract's optional
  `lead_handle`, else the seat whose role is `lead`, else enter is disabled and
  the details pane says "no lead seat in status" (lead ruling). The operator
  handle is `user`.
  n opens a form with one field per input of `actions.new_task`, then runs the
  contract's argv plus `--flag value` for each input; it composes nothing else.
- `internal/cli/center.go`: `amq-noc` (no arguments, or `--fleet`, `--once`)
  starts the command center. `cli.Run` no longer dispatches `noc`.
- Status refresh every 5 s and on `r`; a failed status call keeps the last data
  and says so in the header.

Tests: golden snapshots at 120 and 80 columns of the vendored fixture
(acceptance 2), eight-project grouping and the fold with snapshots (4), buckets
(3), details fields and the dash for null tokens (5), one test per key (6),
the lead conversation argv, the staged confirm, no-lead behavior, the
`lead_handle` field on a local document (the vendored fixture is untouched),
refresh selection and error handling, empty states, frame time (8).

Measured (acceptance 8): 50 seats and 200 tasks over 10 projects (250 rows,
120x40). A full frame (status refresh that rebuilds rows, counts and layout,
one key, one View) takes 0.29 ms in `TestFrameTimeUnder100ms`; benchmark
`BenchmarkFrame`: refresh+view 216 us/op, view alone 171 us/op (Apple M-series,
go1.27.1). The limit is 100 ms, so the margin is more than 300 times.

Files deleted in this commit: `internal/cli/noc_help_test.go` (51 lines) and
`internal/cli/amq_noc_cli_test.go`, which tested the old board's help and its
bare `--once` board run; they are replaced by `internal/cli/cli_test.go`
(version text, help names only the command center, `noc` is an unknown
command). They were the only old tests that failed once `Run` started the
command center, so the gate stays green with the rest of the old board
unreachable until commit 3.

Behavior change: `amq-noc` now starts the command center and `amq-noc noc ...`
is an unknown command. The old board code is still in the tree, unreachable;
commit 3 deletes it (delete list in
`docs/v1-command-center-evidence/delete-list.txt`, 124 files, 57610 lines).

## Commit 3: the old board is deleted

Files deleted (exact list with per-file line counts:
`docs/v1-command-center-evidence/delete-list.txt`; derived from `deadcode
./cmd/amq-noc` after commit 2 wired the command center):

| tree | files | lines |
| --- | --- | --- |
| internal/console (the whole board TUI) | 44 | 23776 |
| internal/cli (noc.go, status_board.go, status.go, doctor, preflight, team_resume, threads, list, history, workstream, briefs_seed, completion, noc_runtime_actions, team_launch_tmux*, agent_args, amq_env, squad_helpers and their tests) | 26 | 18392 |
| internal/state (the classifier and attention queue that 2b-1 ported into amq-squad: collapse, attention, attention_queue, coordination, snapshot, message, match ...) | 25 | 6538 |
| internal/noc (collect, correlation, health, tmux, discover, runtime, namespace) | 17 | 5336 |
| internal/team | 4 | 926 |
| internal/launch | 2 | 895 |
| internal/act | 2 | 783 |
| internal/rules | 2 | 623 |
| internal/catalog | 2 | 341 |
| total | 124 | 57610 |

Also deleted: `README.html` (186 lines, generated from the old README),
`docs/tui-keymap.md` (69), `docs/operator-gate.md` (65),
`docs/runtime-actions.md` (119), which described the removed board. Earlier, in
commit 2: `internal/cli/noc_help_test.go` (27) and
`internal/cli/amq_noc_cli_test.go` (58). `internal/cli/test_helpers_test.go`
keeps only `captureOutput`. `go mod tidy` dropped `bubbles` and `fsnotify`
from go.mod (no module added). Kept in `internal/cli`: cli.go, exit.go,
output.go, json.go, center.go. The two historical evidence documents
(`docs/0.10-operator-client-audit.md`, `docs/0.12-release-candidate-evidence.md`)
stay as history.

README.md is rewritten for the command center.

Tests added: a bare `Run` renders the command center from the status call
(acceptance 7), the old directories and files are absent, `noc` is an unknown
command.

Behavior changes: everything the old board did is gone on purpose: the
multi-root discovery and scan, snapshot JSON (`--json`, `--once` as a board
snapshot), tmux focus, up/stop/resume/restart controls, gate answering, the
copy-command picker, notifications. `--once` now prints one command-center
frame. The module path, version command and global flags (`--quiet`,
`--verbose`, `--color`) are unchanged.

### Removed verbs and where each job lives in 3.0

The table the owner reads. Every verb that read `.amq-squad/`, the AMQ
stores or tmux directly, or composed an amq-squad command, went with the board.

| removed from amq-noc | where the job lives in 3.0 |
| --- | --- |
| `amq-noc noc` board, multi-root discovery and scan | the command center, fed by `amq-squad status --json` (`--fleet` for many squads) |
| `noc up`, `U up`, `S stop`, `R resume`, `X restart`, team launch (`team_launch_tmux*`, `team_resume`) | `amq-squad lead --team` |
| `doctor`, `preflight` | `amq-squad doctor` |
| runtime and pane controls (`o focus`, `v output`, tmux focus, send-to-pane, `L direct-lead` pane delivery) | dropped (R6, no pane control); the conversation with the lead goes over AMQ (`enter` here) |
| gate answering and the operator gate flow | shown here (details pane), answered on the signed path (brief 4-5) |
| JSON snapshots (`--json`, `--once` as a board snapshot), `list`, `history`, `threads` | `amq-squad status --json` itself; `amq thread` for a conversation |
| `workstream`, `briefs_seed`, completion helpers | amq-squad's own verbs (brief drafting is amq-squad's, phase 5) |
| classifier, attention queue, correlation, health | amq-squad `internal/status` (2b-1); this renderer reads the result |
| `--notify`, terminal bell | dropped |

### go.mod after the deletion

`go mod tidy` removed two modules that nothing imports now:
`github.com/charmbracelet/bubbles` and `github.com/fsnotify/fsnotify` (and
their go.sum lines). No module was added. Direct requirements left: bubbletea,
lipgloss, termenv.

### Acceptance 1 across the whole binary

`TestPackageTouchesNoFiles` now scans `internal/center`, `internal/cli` and
`cmd/amq-noc`: no file-system calls, no `io/fs` or `ioutil`, no string naming
`.amq-squad` or tmux, and `os/exec` only in `internal/center/runner.go`.
`TestClientRunsOnlyContractArgv` and `TestBareRunRendersCommandCenter` run with
a fake PATH that logs every argv.

### Rulings applied

Fleet document: `{"schema_version":1,"kind":"fleet",...,"squads":[<status>...]}`,
parsed as a fleet when `squads` is present, tested on a local document. The
operator handle is `user`; there is no `operator_handle` field (removed from
the parser).

## Commit 5: live run on a real v3 status

Built amq-squad from the v3 tip (5509f31e) to a scratch path and put it first
on PATH for that one session only; the AMQ root and project were scratch
directories under `/tmp/noc4-live` (no `~/.amq-squad`, `~/.claude` or `~/.codex`
touched). Tasks were made with `amq-squad task add`, claimed and one blocked
with `--route owner`. amq-noc, started with no arguments in a tmux pane
(120x34), rendered the real `status --json`:

- Needs you 1 (the owner-routed blocked task), Working 2, Inactive 2: the two
  seats with no launch record or signal come through as `unknown` and render
  "Inactive (unknown)", as ruled.
- `n`, title typed, class chosen with the arrow keys, enter: the contract's
  argv ran (`amq-squad task add --root /tmp/noc4-live/q --title "Plan the
  migration" --class plan`), the footer showed amq-squad's own reply
  (`Added t4 ... pending`), and the next refresh showed the new task (All 5 to 6).
- The status call lists no seat role, so enter says "no lead seat in status"
  (lead_handle is not produced yet).

Evidence: `docs/v1-command-center-evidence/live-command-center.png` (the frame
after the new task, next to the reference
`docs/v3-research/codex-agent-command-center-2026-10-24.png` in amq-squad),
`live-frame-all.txt`, `live-frame-needs-you.txt` (the Needs you tab). No gate
was run while the session was up.

## Open rulings and deviations

- Status labels follow the contract's bucket labels. The reference shows
  working rows with a green dot; colors here are minimal (no color in goldens).
- Seat rows with no `project_path` fall back to the squad's `project_path`
  (in the live run that is the directory amq-squad was started in).
- The row's "updated" time for a seat-only row is `last_activity_at`
  (a dash when null); for a task row it is `updated_at`.
- Lead conversation read/write uses `amq thread` and `amq send`; there is no
  contract field for the operator handle, so it is `user`.
- Fleet form is tested on a local document only; `status --fleet --json` from
  4-3 is not merged, so the live run was one-squad mode.
- Waiting tasks (queued, not leased) land in Working per the contract table, so
  a queued task shows a green Working dot with `waiting queued` in the details.
