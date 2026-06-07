# amq-noc Release Plan

## amq-noc 0.2.1 Goal

Ship the focused status-language and resumed-session liveness fix.

- Treat a recorded PID that returns `EPERM` to signal-0 as alive: the process
  exists even if this user cannot signal it.
- Treat fresh active AMQ presence plus an alive recorded PID as an operational
  live agent, even when process-argument matching is unavailable or partial.
- Suppress old owned `at-risk` attention when the session has newer clear
  activity, while retaining the old evidence in thread/rollup detail.
- Rename the operator-facing live/no-wait status from `running` to `online`.
  `running` implied active work; `online` only claims deterministic liveness.
- Rename JSON `noc_snapshot` primary `state` and `reason_code` values from
  `running` to `online` for the same live/no-wait state; document this as a
  compatibility change for clients.
- Keep the simplified primary status model:
  `needs-you`, `waiting`, `online`, and `stale`.
- Keep `/ running` as a filter alias for `/ online` during the vocabulary
  transition.

## amq-noc 0.2.0 Goal

Work through the open GitHub issues and ship `amq-noc v0.2.0` as a more
trustworthy terminal operator client for many AMQ / amq-squad teams.

Open issue scope:

- [#2](https://github.com/omriariav/amq-noc/issues/2): harden the remaining
  `GO` block-clear signal against negated phrasing.
- [#3](https://github.com/omriariav/amq-noc/issues/3): make session detail
  `now` lead with active `needs-you`, otherwise latest/current activity.
- [#4](https://github.com/omriariav/amq-noc/issues/4): implement the Peirce TUI
  operator-story contract: truthful keymap/help, context-sensitive footer,
  visible-unit header counts, quiet tree, inline CTAs, and regression coverage.

0.2.0 slice status:

- Phase A (CTO): state/order/header foundation. DONE.
  - Route `go` through the same affirmative whole-word + negation guard as
    `approved`, `green`, `resolved`, and `unblocked`.
  - Count visible squad/project rows in the pulse line instead of raw thread
    buckets or waiting agents.
  - Make session detail reserve `now` priority for active `needs-you`; otherwise
    use newest activity and keep old at-risk evidence in thread history.
- Phase B (fullstack): keymap/help truthfulness. DONE.
  - Make `noc --help`, `?`, footer, and the router agree.
  - Remove or defer dead/future TUI keys such as palette, jump/open, alerts,
    timeline, context, DLQ, inbox, and read unless they are actually wired.
  - Add contract tests so advertised keys and handled keys cannot drift.
- Phase C (fullstack with CTO review): context-sensitive footer and tree IA. DONE.
  - Footer shows valid actions for the selected row and explains unavailable
    actions.
  - Left tree stays operational: project -> session -> agent, one line per row,
    no thread subjects in the left tree.
- Phase D: closeout. DONE.
  - Update README.md, README.html, PLAN.md, RETRO.md, and release checklist.
  - Close #2 and #3 because their regressions are implemented.
  - Close #4 as the 0.2.0 TUI contract slice.
  - Open #5 for deferred post-0.2 TUI follow-ups: jump/open, palette cleanup,
    stale comments, richer recovery diagnostics, and golden snapshots.
  - Build an RC binary and run smoke/manual TUI checks before commit/tag/release.

0.2.0 verification so far:

```sh
go test ./internal/console -run 'TestContextFooter|TestKeymapContract|TestTreeIA'
go test ./...
go vet ./...
go build ./...
git diff --check
make ci
/tmp/amq-noc-rc version
/tmp/amq-noc-rc noc --help
/tmp/amq-noc-rc noc --once --tree --root /Users/omri.a/Code/amq-noc/.agent-mail --filter amq-noc-0-1-0
/tmp/amq-noc-rc noc --json --root /Users/omri.a/Code/amq-noc/.agent-mail --filter amq-noc-0-1-0
```

## 0.1.0 Baseline

## Goal

Split the multi-root NOC/operator surface out of `amq-squad` into this repo.

- `amq-squad v1.3.0`: project-local team lifecycle and single-project status.
- `amq-noc v0.1.0`: multi-project visibility, control, orchestration, and AMQ
  dispatch/NOC workflows.

## Decisions

- Use copy-first extraction for 0.1.0. Do not create a shared library yet.
- Keep confirmed mutating actions preview-first and confirm-gated.
- Make confirmed squad lifecycle/config actions run through the installed
  `amq-squad` CLI.
- Remove the `noc` command and `console --root` forwarding from `amq-squad`.
- Keep `amq-squad console` as the project-scoped Mission Control surface.
- Treat thread status as evidence, not as the primary NOC status. The primary
  visible status belongs to projects, teams/sessions, and agents.
- Use four primary visible statuses for 0.1.0:
  `running`, `stale`, `waiting`, and `needs-you`.
- Derive `needs-you` deterministically from structural human-addressed AMQ
  evidence, not broad prose inference.
- Keep granular blocked/gated/at-risk signals as internal evidence/detail where
  useful, but collapse them to `waiting` in primary tree/header UX.

## amq-noc 0.1.0 Work

- Initialize module `github.com/omriariav/amq-noc`.
- Provide binary `amq-noc`.
- Make bare `amq-noc` and `amq-noc noc` open the NOC surface.
- Preserve NOC flags:
  `--root`, `--depth`, `--refresh`, `--filter`, `--hide-stale`, `--once`,
  `--tree`, `--json`, `--actions`, `--run-action`, `--set`, `--dry-run`,
  `--yes`.
- Preserve current NOC UX fixes:
  current-control-first status, historical needs-you demotion, bounded thread
  history, direct delete preview, and confirm-gated mutating actions.
- Keep copied support packages only as needed for 0.1.0; defer shared-package
  cleanup.
- Make the left tree quiet and operational:
  projects collapsed by default, parent rows summarize immediate child
  teams/sessions instead of thread/agent evidence counts, and thread subjects
  stay in the right detail pane.
- Project rows count child teams/sessions by visible status, for example
  `(1 waiting, 2 stale)`, not thread rollups such as blocked-stale or at-risk
  stale counts.
- Session rows may summarize child agents by visible status; detailed thread
  evidence belongs in the right pane.
- Remove unclear primary shortcuts from the 0.1.0 interactive UX:
  `p` palette, `A` alerts, `t` timeline, `c` context, `D`/DLQ, `i` inbox,
  `v` read, `o` open, and `j`/`J` jump.
- Keep primary navigation simple:
  move, expand/collapse, filter, hide stale, refresh, help, quit/back, and a
  delete shortcut where relevant.
- Defer tmux open/jump behavior to 0.2.0.
- Show human-action workflow in the right pane:
  thread/evidence, full ask/context, then visible CTAs below the context
  (`approve` + `deny` for approval/decision asks, or reply/answer for reply
  situations). Do not require separate context/read shortcuts.

## Current 0.1.0 RC Slices

- S1: First-class team/session/agent attention model.
  - Add explicit attention state to sessions and agents.
  - Add needs-you owner derivation so parent rows can say which agent needs the
    operator.
  - Committed on `feat/status-model` as `e611582`.
- S2: Primary NOC UX reorientation.
  - Collapse granular blocked/gated/at-risk to visible `waiting`.
  - Make needs-you structural and owner-led.
  - Collapse projects by default.
  - Make parent-row counts team/session based instead of thread-rollup based.
- S3: 0.1.0 keymap and human-action cleanup.
  - Stage (a): prune confusing keymap/footer/help entries and remove jump/open
    behavior from the primary interactive surface.
  - Stage (b): render context and approve/deny/reply CTAs inline in the right
    pane for human asks.

## Follow-up Issues

- Harden the remaining `GO` block-clear signal in `internal/state/coordination.go`.
  The 0.1.0 RC now guards `approved`, `green`, `resolved`, and `unblocked`
  with whole-word matching plus a small negation window, but the older `GO`
  signal still uses raw substring checks such as `go ` and `go for`. A spaced
  rejection like `no go for it` could still clear a prior block even though
  hyphenated `NO-GO` is protected by the block marker. This is non-blocking for
  0.1.0 because it is uncommon and pre-existing, but it should be tracked and
  tested.
- Fix right-pane/current-thread ordering for 0.2.0. The session detail pane can
  show an old at-risk thread under `now` while the actual current thread appears
  below under `threads: newest`. Operators expect the current/latest thread to
  lead unless there is an active `needs-you` CTA. Add a regression around the
  observed shape: a 1-day-old at-risk `decision/status-model` thread and a
  seconds-old clear `p2p/cto__fullstack` status/todo thread. Tracked in:
  https://github.com/omriariav/amq-noc/issues/3

## amq-squad 1.3.0 Work

- Re-scope module to `github.com/omriariav/amq-squad`.
- Remove `amq-squad noc` as an executable command and replace it with a migration
  error pointing to `amq-noc`.
- Remove `console --root` and NOC-only console flags.
- Remove NOC-specific README sections/examples.
- Keep lifecycle and project-local commands:
  `new`, `team`, `up`, `stop`, `down`, `resume`, `fork`, `status`, `console`,
  `threads`, `thread`, `doctor`, `history`, `agent`, `rm`, `archive`, `brief`,
  `roles`, `completion`, `version`.

## Verification

For `amq-noc`:

```sh
gofmt -l .
git diff --check
git diff --unified=0 -- . | rg '^\+[^+].*[—…→]'
go test ./...
go vet ./...
make ci
go build ./cmd/amq-noc
amq-noc --once --root /Users/omri.a/Code
amq-noc --once --tree --root /Users/omri.a/Code --depth 5
amq-noc --json --root /Users/omri.a/Code
amq-noc --actions --root /Users/omri.a/Code --filter needs-you
```

Manual RC checks:

```sh
go build -o /tmp/amq-noc-rc ./cmd/amq-noc
/tmp/amq-noc-rc --version
/tmp/amq-noc-rc noc --once --tree --root /Users/omri.a/Code --depth 5
/tmp/amq-noc-rc noc --once --tree --filter amq-noc-0-1-0 --root /Users/omri.a/Code/amq-noc/.agent-mail
/tmp/amq-noc-rc noc --once --tree --filter beta-vault-context --root /Users/omri.a/Code --depth 5
```

For `amq-squad`:

```sh
gofmt -l .
git diff --check
git diff --unified=0 -- . | rg '^\+[^+].*[—…→]'
go test ./...
make ci
amq-squad noc
amq-squad console --root /Users/omri.a/Code
amq-squad console --once
```

## Handoff Notes

- Do not commit or push without explicit user approval.
- The current `amq-noc` code is intentionally copied from the dirty amq-squad
  NOC branch so no QA fixes are lost.
- The next cleanup should remove copied amq-squad lifecycle commands from
  `amq-noc` once the NOC command surface is stable.
- Coordination rule for the active RC workstream:
  fullstack implements focused slices; CTO reviews, approves, advises, and
  handles user-facing release decisions. Avoid concurrent edits in
  `internal/console/*`; explicitly hand off ownership before changing those
  files.
