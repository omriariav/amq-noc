# FABLE-PLAN: amq-noc roadmap review

Date: 2026-07-02
Author: fable-planner (Claude Fable 5), workstream `amq-noc-plan-2026-07-02`
Scope: full-repo review of docs, code, issue history, and release state through
`v0.12.1`, producing a prioritized roadmap and a GitHub issue plan.

## 1. Product goal

The goal is stable and consistently documented across README.md,
`.amq-squad/team-rules.md`, and `docs/runtime-actions.md`:

`amq-noc` is the optional, cross-project operator client over durable AMQ and
amq-squad state. Its one job is to let the human operator answer, at a glance:

- what needs me now
- who needs to act
- which AMQ thread/session/profile is involved
- how old the signal is
- what evidence exists
- what command is safe to run or copy next

Hard boundaries that define the product:

- AMQ and amq-squad own protocol truth, runtime control, task state, receipts,
  and merge/release authority. The NOC reads, correlates, and renders.
- Status is structural, never prose-inferred: five primary states
  (`needs-you`, `blocked`, `waiting`, `online`, `stale`).
- Mutating controls are preview-first and confirm-gated, and execute by
  delegating to `amq-squad`/`amq` commands, never raw tmux.
- The NOC must stay optional: every workflow must remain possible through the
  AMQ and amq-squad CLIs alone.

## 2. Current state assessment

### What is shipped and solid (through v0.12.1)

- The read layer: multi-root discovery, project/session/agent tree, the
  five-state attention model, deterministic operator gates, offline-presence
  truth, and JSON snapshots aligned with the TUI.
- The orchestration client: lead badges, orchestrated session markers,
  lead-down detection, conversation mode, `L` directives with dual channel
  (busy-guarded pane send vs durable AMQ), and gate answering with clearing
  semantics.
- The runtime-consumption boundary: published `amq-squad status --json`
  actions rendered verbatim, deterministic fallbacks otherwise, `C copy-cmd`
  picker, executable lifecycle (up/stop/resume/restart) through the
  preview/confirm/exec seam.
- The 0.10 correlation layer: capability/version health checks, AMQ
  presence/ops/receipts surfaces, the operator attention queue, task-store
  ingestion, worker-report history, task/report mismatch signals, and
  release-evidence blockers in the JSON attention queue (deepened in 0.12.0
  with named-profile `--project`/`--profile` correlation).
- Test discipline: ~22k lines of tests against ~36k lines of code, with
  contract tests keeping keymap/footer/help/JSON surfaces truthful.

### Gap A: closed-but-unshipped 0.12.0 scope (trust gap, highest priority)

Issues #45, #46, #48, #49, #50, and #51 were all closed on 2026-06-28 with the
comment "Shipped in amq-noc v0.12.0 via PR #52". The actual PR #52 diff is
about 530 lines touching only `internal/cli/noc.go`,
`internal/noc/correlation.go`, and docs (named-profile correlation plus
release-evidence blockers). Verified against the current tree:

- #46 (detail pane context-first): no console changes in the release diff.
- #49 (fail-closed project control view for execution modes): no
  `execution_mode`/`coordination_boundary`/fail-closed code exists anywhere in
  `internal/`.
- #50 (operator-loop leases and status contracts): no lease or operator-loop
  consumption exists in the code.
- #51 (directive backlog and conversation delivery state): no backlog or
  delivery-state surface exists.
- #48 (command surface audit): partially delivered afterwards by v0.12.1
  (PR #53, public action queue removal), but the closing predates that PR.
- #45 (stale/historical needs-you clarity): possibly covered by older
  behavior; needs a behavior-level check rather than a grep.

For a product whose core value is truthful status, the tracker itself being
untruthful is the most important thing to fix. This also motivates a release
checklist guard (see issue N1 and section 6).

### Gap B: upstream contract lag (amq 0.39, amq-squad 2.11 to 2.14)

Code floors are `amq >= 0.37.1` (preferred 0.38.0) and `amq-squad >= 2.5.0`;
the README recommends amq-squad 2.10.0. The live environment is already
amq 0.39.0 and amq-squad 2.14.0, and upstream tracks a bump of the minimum
amq to 0.39.0 (amq-squad #310). Contracts added in 2.11 to 2.14 that the NOC
does not consume today:

- operator delivery metadata (`durable_amq`, `wake_supported`,
  `poll_required`): the NOC cannot tell the operator whether gates will wake
  anyone or must be polled/drained.
- `amq-squad notify`: the attention-signal command for new/stale operator
  gates; a natural copy-command and health input.
- task lifecycle additions (`task fail`, `task block`): correlation currently
  models pending/in-progress/blocked/failed/done from files, and should be
  re-audited against the current store schema.
- `amq-squad dispatch` / `collect`: team norms now require dispatch over raw
  sends; NOC-suggested copy commands should prefer these forms.
- orchestrator registration (`lead register`, global orchestrator handle):
  already tracked as #55, blocked on the published contract.

Separately, upstream amq-squad #118 and #119 are now CLOSED: session-scope
`status --json` publishes `orchestrated`/`lead`, and the board envelope
carries per-session `data.actions[]`. The NOC still reads `team.json` files
directly and performs a per-session status N+1 that these were meant to
remove.

### Gap C: evidence model is best-effort prose extraction (#39 remainder)

`internal/noc/correlation.go` derives PR URLs, head SHAs, test/review
evidence, and verify-merge verdicts from regexes over task titles and message
text. The 0.10 audit already called this out: structured
`amq-squad verify merge --evidence` consumption and an evidence schema are
still missing, which is exactly the remaining scope of #39 (and the 0.13.0
milestone brief at `.amq-squad/briefs/codex-v0-13-0/v0-13-0.md`).

### Gap D: no CI

There is no `.github/` directory. `make ci` is local-only, while RELEASING.md
step 7 instructs checking `statusCheckRollup` on the release PR, which can
never be green because no checks exist. This was flagged as a follow-up in
RETRO.md after 0.1.0 and never picked up.

### Gap E: no golden TUI snapshots

RETRO.md (0.2.0 recommendations) called for golden snapshots at 80x24,
120x40, ASCII, and no-color. Never implemented. The 0.12.x narrow-terminal
truthfulness work landed without them, so visual regressions still rely on
manual RC checks.

### Gap F: documentation hygiene

- `QA-02-06-26.md` lists QA-001 through QA-011 as "Open triage", but most
  shipped between 0.2.x and 0.6.0 (delete modes, status simplification,
  needs-you freshness, latest-signal preview). The file misreads as a live
  backlog.
- `README.html` duplicates README.md by hand and drifts.
- PLAN.md carries the full history back to 0.1.0; fine as an archive, but the
  active tracks should stay at the top (currently true; keep it that way).

### Small known items

- #54: the live TUI right pane still says `actions (C copies command)`
  (`internal/console/noc_view.go:1340`) after v0.12.1 removed the public
  actions surface. One-line rename plus test updates.

## 3. Prioritized roadmap

### P0: 0.13.0 "truthful tracker, deeper evidence" (existing milestone)

1. N1: reconcile the v0.12.0 closed issues with the shipped diff. Reopen or
   refile #45/#46/#48/#49/#50/#51 remnants, and add the release checklist
   guard so closure claims are verified against the merge diff. Do this
   before building new scope on top.
2. #54: terminology rename (quick win, restores UI/docs consistency).
3. #39: structured merge/review/release evidence (consume
   `verify merge --evidence` output where published; keep prose extraction
   as labeled fallback; group evidence by release/session/profile).
4. #40: task/report correlation deepening per its acceptance criteria,
   re-audited against the amq-squad 2.14 task store (fail/block states).
5. N2: GitHub Actions CI (gofmt, vet, test; release-smoke on tags). Cheap,
   and it makes RELEASING.md step 7 truthful. Fits 0.13.0.

### P1: 0.14.0 "amq 0.39 / amq-squad 2.14 alignment"

6. N3: bump floors and audit contracts: amq minimum 0.39.0, amq-squad
   recommended 2.14.x; surface operator delivery mode
   (wake_supported/poll_required) as a health signal; prefer
   dispatch/collect/notify in generated copy commands where team norms say so.
7. N4: consume board-envelope per-session `data.actions[]` and session-scope
   `orchestrated`/`lead` from `status --json` (upstream #118/#119 shipped).
   Removes the per-session status N+1 and the direct team.json coupling;
   keep file reads only as labeled fallback for older squads.
8. #55: recognize the registered global orchestrator handle (unblocks once
   amq-squad publishes the registration contract; verify against 2.14).

### P2: hardening and hygiene (0.15.0 or opportunistic)

9. N5: golden TUI snapshots (80x24, 120x40, ASCII, no-color).
10. N6: docs reconciliation: annotate QA-02-06-26.md statuses, decide the
    README.html fate (generate or delete), keep PLAN.md active-track-first.
11. Refiled remnants from N1 land here or in 0.14.0 by size (detail-pane
    context-first, fail-closed control view, directive backlog, operator-loop
    contract consumption if still relevant under 2.14 vocabulary).

## 4. GitHub issue plan

Existing issues to keep (no changes needed beyond milestone confirmation):

- #39 (0.13.0): add merge, review, release evidence visibility. Remaining
  scope: structured verify-merge consumption, evidence grouping, SHA-match
  blockers already partially shipped.
- #40 (0.13.0): correlate task state with worker reports. Remaining scope:
  re-audit against 2.14 task store, dependency-gated review task flags,
  mismatch taxonomy completion.
- #54 (0.13.0): rename live TUI action terminology.
- #55 (no milestone): orchestrator handle recognition. Suggest milestone
  0.14.0, blocked on the upstream contract.

New issues (approved on gate/fable-plan-issues 2026-07-02 and created:
N1=#56, N2=#57, N3=#58, N4=#59, N5=#60, N6=#61; milestones 0.14.0 and
0.15.0 created alongside):

- N1 = #56 "Reconcile v0.12.0 issue closures with the shipped diff" (0.13.0).
  Rationale: #45/#46/#48/#49/#50/#51 were closed as shipped via PR #52, but
  the diff only contains correlation work. Acceptance: each of the six issues
  is re-verified behavior-level; missing scope is reopened or refiled with a
  link trail; RELEASING.md gains a step requiring closure claims to cite the
  merged diff; a short correction note lands in PLAN.md.
- N2 = #57 "Add GitHub Actions CI: gates on PRs, release smoke on tags" (0.13.0).
  Rationale: no checks exist; RELEASING.md references statusCheckRollup.
  Acceptance: PR workflow runs gofmt -l, go vet, go test; tag workflow runs
  make release-smoke; README badge optional; RELEASING.md updated to match.
- N3 = #58 "Bump upstream floors and consume amq 0.39 / amq-squad 2.14 operator
  contracts" (0.14.0). Rationale: code floors are 0.37.1/2.5.0 while the
  ecosystem is 0.39.0/2.14.0 with new operator-delivery, notify, dispatch/
  collect, and task-lifecycle contracts. Acceptance: floors raised with
  capability-gap (not hard-fail) behavior for older installs; operator
  delivery mode surfaced per session (poll_required warning when wake is
  unsupported); generated copy commands prefer dispatch/collect/notify where
  the published contract supports them; correlation handles task fail/block.
- N4 = #59 "Consume board-envelope session actions and status-json orchestrated/
  lead" (0.14.0). Rationale: upstream #118/#119 shipped; NOC still does a
  per-session status N+1 and reads team.json directly. Acceptance: board
  envelope actions used when published; orchestrated/lead read from status
  JSON with team.json as labeled fallback; snapshot latency improvement
  measurable on multi-session roots; older-squad rendering unchanged.
- N5 = #60 "Add golden TUI snapshot tests" (0.15.0). Rationale: RETRO 0.2.0
  recommendation, still open; narrow-terminal work shipped without them.
  Acceptance: golden snapshots for 80x24 and 120x40 in color, ASCII, and
  NO_COLOR modes, with an update flag and CI enforcement.
- N6 = #61 "Docs reconciliation: QA-02-06-26 statuses, README.html, PLAN archive"
  (0.15.0). Acceptance: each QA item annotated shipped/superseded/open with
  issue links; README.html generated from README.md or removed; no stale
  "Open triage" labels remain.

Execution order: #56 and #57 first (0.13.0 with #39/#40/#54), then #58 and
#59 (0.14.0 with #55), then #60 and #61 (0.15.0).

## 5. Risks and open questions

- The v0.12.0 closure gap is not explained by unmerged branches: every
  `feat/*` release branch (local and remote) is fully merged into `main`
  (`git log main..feat/v0.12.0` is empty). The missing scope was never
  implemented. N1 still re-verifies each issue at the behavior level before
  refiling, since #45 in particular may be satisfied by older code.
- #50's "operator-loop leases" vocabulary may not match what amq-squad 2.14
  actually publishes. Refile against the current contract rather than
  copying the 2026-06-28 text.
- #55 depends on an amq-squad registration contract that was "pending" at
  filing time; confirm what 2.14 ships before scheduling.
- Evidence schema ownership: #39's structured consumption depends on
  amq-squad publishing a stable verify-merge/evidence schema. If it does not
  exist yet, the NOC issue should spawn an upstream ask instead of parsing
  more prose (0.10 audit boundary: missing upstream APIs become amq-squad
  issues, not brittle parsing).
- Version floors: raising the amq minimum to 0.39.0 should degrade to
  capability-gap warnings, not break older roots, consistent with the
  "absence of metadata reduces affordances, never correctness" rule.

## 6. Validation and release suggestions

- Keep the existing local gates for every slice: `gofmt -l .`,
  `git diff --check`, `go test ./...`, `go vet ./...`, `make ci`, plus a live
  smoke (`go run ./cmd/amq-noc --once --root ~/Code` and a `--json` filter
  against a real workstream).
- Add to RELEASING.md (via N1): before closing a milestone issue as shipped,
  cite the PR diff hunk or test that delivers it; issues without such a
  citation move to the next milestone instead of closing.
- Add CI (N2) so RELEASING.md step 7's status-check verification is real.
- For 0.13.0 specifically, reuse the release-evidence dogfood loop: the NOC
  observing its own release workstream is the best acceptance test for #39
  and #40 (the 0.12.0 evidence doc shows this pattern working).
- Golden snapshots (N5) become part of `make ci` once landed.
