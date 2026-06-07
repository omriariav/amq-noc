# amq-noc Sprint Retro

## 0.2.2 Notes

- Operator helper commands need to be first-class UI, not decorative text. Long
  commands now wrap, and `C copy-cmd` copies the exact underlying shell command.
- Session cleanup must match the selected row. `Del` now deletes team profiles
  only on project rows, removes named sessions on session rows, and explicitly
  refuses `(root)` because it is the AMQ base mailbox.
- Generated recovery commands must carry enough context to be safe: named
  profiles, the active session, and whether the operator wants the current tmux
  window or a detached new tmux session.

## 0.2.1 Notes

- `running` was too strong for the deterministic signal we actually have. A
  live agent can be standing by with no active task, so the operator-facing
  status is now `online`.
- Keep status labels grounded in observable facts: `needs-you` for structural
  operator gates, `waiting` for live non-human coordination waits, `online` for
  live/no-current-wait, and `stale` for dead or old context.
- JSON and TUI need to agree. The 0.2.1 fix keeps primary `state`,
  `reason_code`, session `attention`, and agent `attention` aligned while
  preserving superseded at-risk evidence in thread detail.

## 0.2.0 Notes

This sprint focused on making the TUI more trustworthy as an operator control
surface, not adding more labels or speculative status inference.

What worked:

- Treating `running`, `needs-you`, `waiting`, and `stale` as the only visible
  states kept the interface deterministic. Thread evidence still matters, but it
  belongs in detail panes and tests, not the primary scan path.
- The keymap manifest made help/footer/router drift testable. Once `noc --help`,
  `?`, the footer, and handled keys shared one source, dead keys stopped leaking
  into user-facing copy.
- Context-sensitive footer actions were the right UX compromise. The full action
  surface remains available in help, but the operator sees only actions that can
  actually run on the selected row.
- Regression tests around old at-risk evidence versus current activity captured
  a real trust issue: `now` must mean current work unless an active human CTA
  takes priority.

What hurt:

- The copied console surface still carries dormant 0.1.x code and comments for
  deferred features such as palette, jump/open, inbox/read, timeline, and DLQ.
  We can keep that code dormant, but release-facing help must stay generated from
  live behavior.
- Footer availability needed a second pass because row-kind filtering was too
  coarse. For control clients, advertised actions must mirror handler guards,
  including data availability.
- The sprint included more handoffs than ideal in `internal/console/*`. Explicit
  ownership plus AMQ drains prevented damage, but the package remains a collision
  hotspot.

Recommendations for 0.3.0:

- Split dormant/deferred console features into explicit follow-up issues before
  re-advertising them.
- Add golden TUI snapshots for 80x24, 120x40, ASCII, and no-color modes so visual
  regressions are caught before manual RC.
- Continue reducing copied lifecycle code that is not part of the public
  `amq-noc` operator surface.
- Keep mutating actions preview-first and generated from the same data used for
  execution, so preview/exec drift cannot return.

## 0.1.0 Context

This sprint shipped the first `amq-noc` release: a multi-project NOC for AMQ and
amq-squad teams, with deterministic operator gates, simplified visible statuses,
and release documentation.

## 0.1.0 CTO Notes

- The operator mailbox model was the right pivot. Treating the human as a
  first-class non-runnable mailbox gave `needs-you` a structural signal instead
  of relying on prose or LLM interpretation.
- Keeping visible status simple helped the product: `running`, `waiting`,
  `needs-you`, and `stale` are easier to reason about than thread-level
  blocked/gated/at-risk labels.
- Pair review caught real release-facing issues late in the sprint, especially
  legacy runnable `user` collisions and false block-clearing from broad
  approval text.
- We should continue separating NOC product behavior from copied amq-squad
  support code. The 0.1.0 copy-first path worked, but it leaves cleanup work.

## 0.1.0 Fullstack Notes

What worked:

- Deterministic, structural classification beat prose every time. The operator
  gate as a real `to:user` ask, `addressedTo(operator)`, and asker-derived
  `needs-you` ownership were all testable and stable. Every place we leaned on
  prose or substrings (block markers, the `approved`/`green` block-clear) later
  produced false positives that needed a structural guard.
- Splitting the two axes, work state versus liveness/infra health, removed most
  of the status confusion. Once `dead-mailbox-live` was treated as infra health
  (not running or waiting) and `waiting` was gated on an operational agent, the
  header, tree, and JSON finally agreed on one number.
- Reading the implementation before filing a review finding paid off. Several
  flags turned out to be false alarms (`new.go --sync` is amq-noc's own command,
  `Broadcast` delegates to `BroadcastAs`), and skipping that step would have
  generated churn.

Surprises:

- Capability-gating nearly regressed a just-shipped behavior. Defaulting NOC
  operator gates to schema-3-only silently dropped `needs-you` for every
  existing schema-2 squad, including our own session. Inverting to default-on
  with a schema-3 opt-out preserved it.
- The legacy runnable-`user` collision only surfaced under stable-tag
  validation: deriving operator `user` for a legacy team that has a real `user`
  member agent manufactured false gates.

What hurt:

- The NOC's generated command surface was coupled to a moving amq-squad CLI.
  Across RC2/RC3/1.4.1 we chased `--sync`, `--project` resolution, archive/rm
  cwd behavior, and missing verbs by hand smoke instead of by test.
- Preview/exec drift was a recurring footgun. The broadcast `--thread` bug and
  the `--me` operator-handle handling both came from the displayed command and
  the executed action living in two code paths that can diverge.
- One concurrent-edit collision in `internal/console` cost near-duplicate work,
  because I had not drained before starting an investigation in the same file.

Recommendations for 0.2.0:

- Gate NEW behavior behind capabilities, never re-gate EXISTING behavior. For
  anything visibility or safety relevant, default-on with an explicit opt-out.
- Add a generated-command conformance test (or a pinned CLI version matrix) so
  an amq-squad surface change fails a NOC test rather than a manual smoke.
- Derive the action preview string and the executed args from one source so
  they cannot drift.
- Make ping-before-edit / explicit file ownership the default, with a tighter
  drain cadence, so review latency does not push either side to self-patch.
- Treat unit and regression fixtures as the spec for states live data cannot
  reproduce (zero-operational `dead-mailbox-live`, most gate transitions).

## 0.1.0 Follow-ups

- Harden the remaining `GO` block-clear signal. Tracked in:
  https://github.com/omriariav/amq-noc/issues/2
- Decide whether to add GitHub CI for `go vet`, `go test ./...`, and release
  smoke so future PRs do not rely only on local verification.
- Continue removing or isolating copied lifecycle code that is not part of the
  public `amq-noc` surface.
