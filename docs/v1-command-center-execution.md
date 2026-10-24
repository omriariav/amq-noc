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

## Commit 6: reference polish

Column header row (Tasks, Status, Updated, dim) above the first group; the selected row is reverse-video across the list width and the active tab is a reverse-video chip (both already in place since commit 2, invisible in the plain-text goldens, so the tab keeps its brackets and the row its marker). Goldens at 120 and 80 regenerated (one header line added); frame-time test unchanged and passing.

## Commit 7 and 8: program allowlist, live screenshot retaken

Runner fix (cd22be9): ExecRunner and the new-task action accept only argv[0] equal to bare `amq-squad` or `amq`, compared before exec (LookPath resolution unchanged); anything else returns "runner: program not allowed: <name>", shown in the footer. Tests cover "/tmp/x/amq-squad", "./amq-squad", "../amq", "touch" and a trailing-space name, for both the runner and the composed action, and that nothing runs.

Live screenshot retaken with the header row (same scratch root /tmp/noc4-live, v3 amq-squad first on PATH for that session only, no gate run while it was up): `live-command-center.png` and `live-frame-all.txt` replaced.

## Commit 9: Codex pass fixes

Seven items, each with a test in `internal/center/hardening_test.go` (the three contract and docs ones were checked to fail when reverted; items 5 and 6 reference the new conversation fields and do not compile against the old code): TestRunnerRefusesPython3DashC (exact argv python3 -c "print('executed')" is refused in the runner and the action, nothing ran), TestZeroInputActionDoesNotPanic (null and empty inputs run without a form; n then esc and a forced empty form do not panic), TestParseRejectsUnknownBucket (error names the row id and the value), TestParseFleetEnvelopeValidation (envelope schema_version checked before squads; empty squads is an error), TestStagedSendRunsOnce (an in-flight flag; a slow runner and three enters give one amq send), TestStaleConversationResultsDropped (a generation id on every conversation command and result; stale results are discarded), TestReleasingDocsSurvivingSurface (RELEASING.md no longer requires `amq-noc noc`; it and its notes describe the command center).

## Commit 10: review fixes from the repository session

1. Unknown bucket: maps to inactive with the unknown marker plus a warning naming the row and value; the document and the other squads are not refused (supersedes Codex item 3). TestParseMapsUnknownBucketToInactiveWithWarning.
2. Rows are keyed by squad root and session, not fleet position; the dead squad index fields of the form and conversation are gone. TestFleetReorderKeepsSelectionAndConversation.
3. New-task in-flight flag (also covers a zero-input action). TestZeroInputNewTaskNotDuplicated.
4. NewTaskArgv emits --flag=value so a dash-leading value is one argument. TestNewTaskArgvDashValue (the argv expectations in the older tests changed to --flag=value).
5. PLAN.md marked superseded with a pointer to this log; RELEASING.md says only v2 and later need the /vN migration. TestRepoDocsMarkedAndFixed.
6. printUsage double blank line removed; dispatch is a single return. TestHelpHasNoDoubleBlankLine.

## Commit 11: warnings on screen

The first warning shows in a line above the footer with "(+N more)" when there are several (the body loses one line while any warning exists); the help overlay lists every warning. TestWarningsShownInFooterAndHelp, and cli TestOnceFrameShowsBucketWarning asserts "task t1: unknown bucket" in the --once frame. A new task is also refused in startNew, before the form opens, while one is in flight (TestNewRefusedBeforeFormWhileRunning).

## Commit 12: contract v3 follow-ups (lead_handle, live, operator, waiting, help clamp)

Vendored from amq-squad v3 (96a76bf0) byte for byte: `status-v1.golden.json` (sha256 9dd2e35b418280ff...; now carries `lead_handle` and per-seat `live`) and `status-fleet-v1.golden.json` (sha256 6d6efa48a1a61d23...), each with its own committed digest test. The fleet golden replaces the hand-written fleet document in `TestParseFleetForm` and `TestFleetStatusArgv`; the envelope-validation cases stay on local documents because the golden does not cover them.

1. `lead_handle` is read first; the first seat with role `lead` remains the fallback (`Lead`). TestLeadHandleFirstThenRoleFallback, TestLeadHandleField. Rendering the single golden now drops the "no lead seat" line from its 120-column details pane, the one golden diff.
2. `live` (a `*bool`, nil for a producer without the field): a seat row with `live` false and a bucket other than Inactive shows a `⊘` glyph in place of the bucket glyph and a "not live" line in the details pane. The contract classifies; nothing is recomputed. TestNotLiveMarker.
3. Fleet `operator` (handle user, role operator) renders as an "Operator" group at the top of the list, filtered by its own bucket like any seat row; enter on it opens no conversation (it belongs to no squad), `n` uses the first squad's action. The conversation's operator handle is `operator.handle` when present, else `user` (`Operator(Fleet)`). TestFleetOperatorRowAtTop.
4. `waiting[]` rows render in a "Waiting" group after the squads: the squad name, `wait_reason` in the status column, and `repository`, `project_path`, `cap`, `since`, `detail` in the details pane. They carry no bucket, so they appear under All only and All counts them (the contract's `counts` do not). TestWaitingGroup.
5. The help overlay is clamped to the body height. When longer, the last line is a fold, "(+N more)" (or "(M above, +N more)"), and up/down/j/k/pgup/pgdn scroll it; it reopens at the top. TestHelpClampedToTerminalHeight.

Fleet snapshots `fleet-120.txt` and `fleet-80.txt` are new (TestGoldenFleetSnapshots); `fixture-120.txt` changed by one line (above); `fixture-80.txt` and the eight-projects goldens are unchanged. TestFrameTimeUnder100ms is untouched and passing. Gate output: `docs/v1-command-center-evidence/commit12-make-ci.txt` and `commit12-cross.txt`.

## Commit 13: waiting rows are not counted in All

Waiting rows still show under All, but `Counts` no longer adds them to the All number, so All equals the sum of the four bucket tabs (acceptance 3) and the "Waiting N" group shows its own count. On the fleet golden All is 4, not 3: the contract's `counts.all` of 3 covers the squad's rows only, and the operator row (Ready) is counted in its bucket, so the four tabs sum to 4 (Needs you 1, Working 1, Ready 2, Inactive 0). TestWaitingGroup asserts both the value and the sum; `fleet-120.txt` and `fleet-80.txt` regenerated (the tab line only). Gate: `commit13-make-ci.txt`, `commit13-cross.txt`.

## Commit 14: review fixes (new task on no-squad rows, waiting-only fleet, README)

1. `n` on the operator row or a waiting row is refused with "no new task for this row: it belongs to no squad" instead of falling back to squad 0 (supersedes the commit 12 item 3 "n uses the first squad's action"); on a group fold in fleet mode it is refused with "no new task here: the fold is not a squad row". TestNewTaskRefusedOnRowsWithoutSquad (operator and waiting rows), TestNewTaskRefusedOnFleetFold; the operator assertion in TestFleetOperatorRowAtTop now expects the refusal.
2. `Parse` refuses a fleet only when it has no squads, no waiting entries and no operator; a fleet with only waiting entries or only the operator row renders them. TestFleetWithOnlyWaitingParses (squads:[] plus one waiting entry renders a Waiting group, no error; squads:[] alone is still refused).
3. README states that waiting rows show under All without being counted in it, so All equals the sum of the four bucket tabs.

No golden changed. Gate: `commit14-make-ci.txt`, `commit14-cross.txt`.
