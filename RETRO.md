# amq-noc 0.1.0 Sprint Retro

## Context

This sprint shipped the first `amq-noc` release: a multi-project NOC for AMQ and
amq-squad teams, with deterministic operator gates, simplified visible statuses,
and release documentation.

## CTO Notes

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

## Fullstack Notes

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

## Follow-ups

- Harden the remaining `GO` block-clear signal. Tracked in:
  https://github.com/omriariav/amq-noc/issues/2
- Decide whether to add GitHub CI for `go vet`, `go test ./...`, and release
  smoke so future PRs do not rely only on local verification.
- Continue removing or isolating copied lifecycle code that is not part of the
  public `amq-noc` surface.
