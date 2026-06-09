# Runtime actions: the consume/orchestrate boundary

amq-noc is a read-only operator console. It supports amq-squad *runtime actions*
(start / stop / resume / focus / open / prompt-delivery), but it does not
perform the runtime mechanics itself. There is a hard architectural line:

- **amq-squad owns runtime orchestration.** Starting, stopping, resuming,
  focusing, opening, and delivering prompts to a session or agent, including all
  tmux/window/process mechanics, is amq-squad's job.
- **amq-noc consumes, renders, and selects.** The NOC consumes published
  `amq-squad v1.5` action metadata when available, renders it as operator UI,
  lets the operator pick and copy the exact command, and degrades to
  deterministic fallback commands when runtime support is absent or partial.

The NOC never drives the runtime. It surfaces what amq-squad offers and hands the
operator a command; amq-squad (or the operator running that command) does the work.

## What the NOC consumes vs generates

- **Capabilities** (`team.Capabilities`, surfaced on `noc.ProjectSnapshot`): the
  machine-readable flags a project advertises, e.g. operator gates and runtime
  action support. The NOC consumes these when present but also accepts v1.5.0
  `records[].actions[]` without requiring a capability flag.
- **Published action metadata**: in v0.5 the NOC consumes available
  `amq-squad status --session --json` actions. Session `status` and `resume`
  replace fallback commands while preserving stable action IDs; agent `focus`
  and `send` are added when `available:true`.
- **Command templates**: when published action metadata is absent or partial,
  the NOC still generates deterministic fallback commands from the read-only
  session/coordination snapshot: `amq` fallbacks (drain, send) and
  `amq-squad` delegations (resume here / open new session / agent resume) whose
  execution amq-squad owns.

Either way the boundary holds: the NOC produces a command that DELEGATES to
amq-squad (or amq) and never executes the runtime mechanics. It derives every
command from structural inputs (the snapshot plus capability flags), not from
prose, and does not synthesize an action amq-squad cannot perform.

## What the NOC renders

- The right-pane command helper for the selected row, wrapped to the pane width.
  This inline helper remains deterministic fallback in v0.5 and is tracked for
  possible published-action folding under #5.
- The `C` copy-cmd picker: a numbered list of the exact commands for the
  selection, including published runtime actions when available, copied verbatim
  to the clipboard via the injectable clipboard seam (pbcopy in production).
  The operator runs the copied command; the NOC does not execute the runtime
  action itself.
- Context-sensitive footer actions: only the actions whose guards would actually
  proceed on the selected row (see `docs/tui-keymap.md`).

## Fallback mode

When a project or amq-squad build does not advertise runtime-action support
(older amq-squad, missing capability, or metadata absent), the NOC must degrade
gracefully rather than break:

- It still renders the project/session/agent operational state and the
  needs-you / blocked / waiting / online / stale status model.
- It offers the commands it can derive deterministically (kick/recover, the
  bundled `new`/`resume`/`archive`/`rm` helpers) for manual copy/run.
- It does not show runtime-action affordances that the metadata does not back,
  and it does not error or hang waiting on runtime support.

The rule: absence of runtime metadata reduces the affordances offered, never the
correctness or stability of the read-only view.

## The tmux boundary

The runtime-orchestration lock is about CONTROL: amq-squad owns start / stop /
resume / focus / open / prompt-delivery and all the tmux/window/process mechanics
behind them. amq-noc must not add tmux orchestration to drive runtime actions.

One pre-existing, separate exception: `internal/noc/tmux.go` shells
`tmux list-panes -a` READ-ONLY to resolve panes for the legacy jump/focus view
movement. That is read-only discovery, not runtime control, and it is tracked for
cleanup with the rest of the dormant jump/palette surface (issue #5). It must not
be expanded, and new runtime-action work must not reach for tmux.

## Dependencies

The NOC-side consumption depends on amq-squad publishing the runtime-action and
capability metadata: amq-squad #61, #62, #47, plus the follow-up JSON contract
polish tracked in amq-squad #79. NOC issues #6 (runtime actions), #7 (published
action consumption), and #5 (dormant control/diagnostics cleanup, including the
legacy tmux jump path and inline helper follow-up)
track the NOC side.

## Determinism

Every rendered field is verbatim-or-structural: capability flags come from
amq-squad/team metadata, and command templates either come from future
amq-squad-published action metadata or from NOC's deterministic fallback
generator. The visible status model is computed from structural signals
(operator-addressed asks, declared blocks, aging thresholds, agent liveness),
never from prose interpretation or an LLM. The contract and availability tests
(`internal/console/noc_keymap_test.go`, `noc_footer_test.go`) keep the
advertised surface in lockstep with what the handlers will actually do.
