# amq-noc Release Plan

## Orchestrator-client track (0.7.x - 0.9.x)

Updated: 2026-06-11. Status: **0.7.0 shipped** (PR #23, tag `v0.7.0`; #20,
#22 closed) and **0.8.0 shipped** (PR #26, tag `v0.8.0`, release-smoke
green, local install refreshed; #19, #21, #24, #25 closed on merge). The
lead-side directive norm is tracked upstream in amq-squad#117 (with #118,
#119 as the rest of the squad v1.8 wishlist). 0.9.x remains a proposal.

Goal: make amq-noc the human's client for orchestrated amq-squad teams. The
operator creates orchestrated squads through amq-squad (`new team
--orchestrated --lead ROLE`, the amq-team-setup wizard, the
amq-squad-orchestrator lead skill); the NOC is where the human supervises and
drives them: see which leads need attention, read child reports, answer gates,
send directives to the lead, and run lifecycle, all from one screen.

Producer-side contracts this consumes (shipped in amq-squad v1.6.0/v1.7.0):

- `team.json` schema 3 `orchestrated: true` + `lead: <role>` (exactly one
  lead, a member role, never the operator/NOC).
- The `[AGENT-EVENT]`-over-AMQ reporting protocol: children push real AMQ
  messages to the lead (`status` for progress/done, `question` for blocked,
  `review_request` for ready) on `p2p/<lead>__<child>` threads; human gates
  stay on `gate/<topic>` to the operator mailbox.
- Per-session briefs at `.amq-squad/briefs/<session>.md` (wizard-normalized
  Goal / Scope / Acceptance).

Naming rule: the squad concept is "lead" / "orchestrated" in NOC code and UX.
The existing `orchestrator:` filter key and `Message.Orchestrator` field are
AMQ message metadata for external orchestrators (Symphony/Kanban) and stay
unchanged; do not conflate the two.

### 0.7.0 - the NOC sees orchestrated squads (fixes + cleanup + read layer)

Fix first (live correctness bugs from field testing; the workstream-resolution
helper built here is the same effective-profile resolution the read layer
below needs for lead/orchestrated session rows):

- [#22](https://github.com/omriariav/amq-noc/issues/22): the new-session flow
  free-asks for a session name (`internal/console/noc_control.go`
  `beginNewSessionForProject`) with no default and no semantics in the prompt,
  then launches the squad under the typed name. A configured-but-never-launched
  team gets silently routed into a brand-new workstream with a stub brief while
  the reviewed brief sits unused under the configured name.
- Fix per the issue: derive the workstream from the effective profile (the
  unique member `session` value in team.json, or `up --dry-run --json`),
  prefill the prompt instead of free-asking, and preflight-warn on divergence:
  launching under a different name creates a new workstream with a stub brief.
  If member sessions disagree, fall back to prompting with that warning.
- Addendum from #22 testing: the copy-command panel already emits correct
  project-level actions with no `--session` override (resolution delegated to
  amq-squad). Fold in the enhancement: display the resolved workstream next
  to each project-level action so the operator can see where the squad will
  land before running anything; that information void is what made a
  free-typed session name feel plausible.
- Companion truthfulness fix, mirroring
  [amq-squad#109](https://github.com/omriariav/amq-squad/issues/109): a
  presence file with `status: "offline"` plus a dead recorded PID should
  classify as stale/dead in the NOC's own snapshot classifier, not
  `dead-mailbox-live`. Today a clean `stop` reads as online on the NOC board
  for the 90s presence-freshness window. Align final semantics with whatever
  fix amq-squad lands for #109.
Remove (shrinks the surface the rest lands on):

- Delete the unregistered copied amq-squad lifecycle command files in
  `internal/cli` (up/down/resume/fork/team*/brief*/roles/workstream/new/rm/
  agent*/launch*/bootstrap/status*/threads/thread/history/console/preflight/
  restore and friends). They are dead code: `dispatch()` only routes the NOC
  surface, and the fork has already drifted behind amq-squad v1.7. Audit
  first: extract any helpers the NOC path still compiles against, then delete
  the files and their tests. This closes the standing PLAN.md handoff note.
- Trim `internal/catalog`, `internal/launch`, `internal/role`, and the copied
  parts of `internal/team` down to what the NOC path actually uses.
- Fold in #20 (DRY the duplicated hard-stop recency helpers into state).

Add:

- Parse `orchestrated` + `lead` in `internal/team` (additive; mirror squad
  validation: lead required iff orchestrated, must name a member role).
- Thread it through the snapshot: session rows resolve orchestrated/lead from
  the effective profile (existing profile-resolution logic); agents gain an
  is-lead flag; the lead role resolves to the member handle for thread
  matching.
- TUI: lead badge on the agent row, lead sorts first under an orchestrated
  session, orchestrated marker on session rows. JSON adds `orchestrated`,
  `lead`, and per-agent `is_lead` (additive fields only).
- Orchestration digest in the right pane for orchestrated sessions: per
  child, the latest report to the lead (kind + subject + age) projected from
  the threads the NOC already collapses; no new I/O.
- Brief context in the right pane: Goal/Acceptance lines read from
  `.amq-squad/briefs/<session>.md`; silent degrade when absent.
- Lead-aware status hint: lead dead while children live shows a deterministic
  lead-down reason on the session row. Vocabulary stays
  needs-you/blocked/waiting/online/stale; no new primary state.
- Filters: `orchestrated` and `lead:<role>` tokens (CLI + TUI parity).

### 0.8.0 - the NOC drives the lead (write layer)

- Directive flow: first-class "direct the lead" control on orchestrated
  session/lead rows. Multi-line compose (existing form + paste), preview,
  confirm; deliver via the published member `send` action when
  `available:true` (pane delivery; surface the busy-pane refusal cleanly,
  never auto `--force`), else fall back to a durable AMQ message to the lead
  handle via the act package. Label which channel was used and that direct
  messages do not clear gates (existing 0.6.0 labeling).
- #19: executable `up` through the same preview/confirm/exec seam as
  down/resume; default exec target `new-session` so the NOC pane is never
  hijacked (current-window variants stay copy-only in the `C` picker).
  Parity audit for down/resume exec across project/session scopes.
- #21: bounded latest-output preview per agent plus approve/deny/message
  with an AMQ kind selector; kinds sourced from one shared constant list.
- Run available read-only runtime actions directly: focus / attach_control /
  status (focus gated on a usable tmux client, else copy).

### 0.9.0 - the conversation (implemented 2026-06-11, pending release)

Companion: amq-squad v1.9.0 (the DIRECTIVE norm ships there, so leads now
acknowledge NOC directives on the operator p2p thread - the traffic that
fills this release's headline view).

Decisions taken (recorded on #27):

- `m` on an orchestrated session or its lead row OPENS CONVERSATION MODE;
  `m` on worker rows keeps the one-shot kind-aware composer. `L` stays the
  quick fire-and-forget directive.
- Transcript scope is participant-filtered, not thread-filtered: every
  message between the operator and the lead, wherever it lives (the
  `p2p/<lead>__<operator>` thread plus lead-raised `gate/<topic>` asks),
  interleaved chronologically with thread badges. Child/peer traffic is
  out: it is the operator's conversation with the orchestrator, nothing
  more.
- Sends use an INLINE STAGED CONFIRM: enter stages the message as a visible
  about-to-send line (kind + thread + channel shown), enter again sends,
  esc edits. Two-step and truthful, no modal; this is the documented
  conversation-mode form of preview-first, not a relaxation.
- #17 stays incremental around the conversation view: no full IA rework.

Slices:

- A (#27 read half): conversation view skeleton - transcript via the
  read-only thread-context path (full bodies; ThreadSummary only carries
  the latest), bounded scrollback, refresh on tick/g, esc back to board.
- B (#27 write half): the inline composer - staged confirm, kind-aware
  defaults (`answer` when replying to an open gate, `todo`/DIRECTIVE
  otherwise), channel logic reused from the directive flow (pane when the
  lead is operational, busy-guard surfaced; durable AMQ otherwise), gate
  answers clear needs-you with existing semantics.
- C (#17 incremental): visual hierarchy/density pass, clearer state
  glyphs and colors (blocked vs waiting vs lead-down without alarming
  normal waits), orchestration emphasis, narrow/wide terminal validation,
  before/after captures per #17's acceptance.
- D: desktop notification opt-in for needs-you 0->N transitions (macOS
  notifier seam; terminal bell stays the default).
- E (stretch, consume-when-landed): squad #118 (drop the team.json file
  coupling) and #119 (drop the per-session status N+1); outbound channel
  symmetry follow-up filed upstream.
- F: docs, gates, RC dogfood against pm-copilot, operator-gated release.

### Upstream asks (amq-squad issues to open; NOC never forks runtime logic)

- Publish `orchestrated`/`lead` in session-scope `status --json` (and the
  board envelope) so external clients need no team.json file reads. The NOC
  ships with the local-read path regardless.
- Board/project-scope `data.actions[]` (already a known deferred gap in the
  squad roadmap) to remove the NOC's per-session status N+1.
- Watch the squad #31 lifecycle-verbs epic: keep NOC consumption behind
  published contracts so a squad reshape does not break this client.

### Decisions to confirm before scoping issues

- Directive AMQ thread convention for the mailbox fallback: proposal
  `p2p/<sorted lead__user>`; alternative `directive/<topic>`.
- Lead-down presentation: reason/badge on `waiting` (proposal) vs a new
  primary state (rejected by default; 0.6.0 settled the vocabulary).
- Team creation stays out of the NOC: the wizard owns goal->brief->team; the
  NOC offers the wizard invocation as copy text only. Confirm.

### Compatibility and tests

- Older squads (pre-v1.7 team.json, no orchestration fields) render exactly
  as today; orchestrated teams observed by older amq-squad binaries degrade
  to flat rendering plus whatever actions are published.
- Contract tests per repo convention: keymap/footer truth for new lead-scoped
  keys; additive JSON schema tests; directive preview/confirm seam tests
  (recorded sender, no real bus); digest projection goldens from seeded
  mailboxes; busy-refusal surfaced; gate-clear regression on a lead-raised
  gate; lead-down liveness regression.

## Released: amq-noc 0.6.0 integrated NOC polish

Updated: 2026-06-09.

Current release state:

- `amq-noc v0.6.0` is shipped and is the latest GitHub release.
- `amq-squad v1.5.4` is shipped and contains the fork-free liveness fix needed
  by the NOC board/status consistency checks.
- `amq-noc v0.6.0` consumes the `amq-squad v1.5.2+` top-level session
  `data.actions[]` catalog in CLI `--actions`, JSON snapshots, and the TUI
  `C copy-cmd` picker.

0.6.0 was the integrated "make the NOC feel right" release and closed the scoped
amq-noc issues:

- [#15](https://github.com/omriariav/amq-noc/issues/15): consumed
  `amq-squad v1.5.2+` session-scope `data.actions[]`.
  - Prefer published session actions over generated session controls.
  - Surface explicit `status`, `resume_preview`, `resume_current_window`,
    `resume_new_session`, and `stop` actions.
  - Suppress generated `restart` when published resume/stop variants make it
    redundant or unsafe.
  - Preserve fallback behavior for older `amq-squad v1.5.0/v1.5.1`
    `records[].actions[]` contracts and for partial/missing runtime metadata.
- [#16](https://github.com/omriariav/amq-noc/issues/16): reserved `blocked` for
  hard stops and render normal coordination dependencies as `waiting`.
  - Primary tree/header/JSON states should use `waiting` for awaiting QA,
    review, peer reply, revalidation, merge, or release artifact states.
  - `blocked` should remain for explicit hard stops such as `NO-GO`,
    `blocker:`, `cannot proceed`, broken environment, unsafe conflict, or
    unrecoverable preflight failure.
  - Older blocked evidence must remain visible in detail/history without
    dominating a newer clear/waiting signal.
- [#14](https://github.com/omriariav/amq-noc/issues/14): cleared `needs-you`
  after the operator approves, denies, or replies on the same gate thread, and
  make direct message / pane prompt results explicit when they do not clear a
  gate.
  - The board must stop showing `needs-you` once the same gate thread has a
    later operator answer/action.
  - Confirm the TUI refresh, JSON snapshot, and action result overlays all agree
    without requiring a manual agent-side Enter or separate inbox interaction.
- [#5](https://github.com/omriariav/amq-noc/issues/5): closed the deferred TUI
  controls and diagnostics cleanup from the 0.2.x line.
  - Remove or fully wire dormant controls; advertised keys, footer help, and
    handled actions must stay in sync.
  - Fold any remaining fallback-only right-pane helper lists into the canonical
    runtime action model, or delete them if the `C copy-cmd` picker supersedes
    them.
  - Keep diagnostics/action availability explicit when amq-squad does not
    publish `focus`, `send`, or `attach_control`.

Out of scope for 0.6.0:

- Fixing missing `focus` / `attach_control` publication in amq-squad is tracked
  upstream in
  [amq-squad #95](https://github.com/omriariav/amq-squad/issues/95). NOC should
  consume those actions when published, but amq-squad owns their availability.
- Broad visual redesign beyond the 0.6.0 status semantics and dormant-control
  cleanup is tracked in
  [#17](https://github.com/omriariav/amq-noc/issues/17).
- Additional multi-squad workflow features not already represented by 0.6.0
  issues are tracked in
  [#18](https://github.com/omriariav/amq-noc/issues/18).

Current squad/workstream:

- Project: `/Users/omri.a/Code/amq-noc`
- Session/workstream: `amq-noc-0-1-0`
- AMQ root:
  `/Users/omri.a/Code/amq-noc/.agent-mail/amq-noc-0-1-0`
- `amq-squad status --session amq-noc-0-1-0 --json` is the runtime-action
  contract smoke target. This paused workstream may show stale/degraded launch
  records until the agents are relaunched in tmux; that is expected and means
  `focus`/`send` runtime actions stay `available:false`.

Pause command:

```sh
amq-squad stop --project /Users/omri.a/Code/amq-noc --all --session amq-noc-0-1-0
```

Resume commands:

```sh
cd /Users/omri.a/Code/amq-noc

amq-squad status --project /Users/omri.a/Code/amq-noc --session amq-noc-0-1-0
amq-squad resume --project /Users/omri.a/Code/amq-noc --session amq-noc-0-1-0

# Open panes when ready to resume live work:
amq-squad resume --project /Users/omri.a/Code/amq-noc --exec --target current-window --session amq-noc-0-1-0
amq-squad resume --project /Users/omri.a/Code/amq-noc --exec --target new-session --terminal-session amq-squad-amq-noc-amq-noc-0-1-0 --session amq-noc-0-1-0

# Drain current workstream inboxes:
amq drain --root /Users/omri.a/Code/amq-noc/.agent-mail/amq-noc-0-1-0 --me cto --include-body
amq drain --root /Users/omri.a/Code/amq-noc/.agent-mail/amq-noc-0-1-0 --me fullstack --include-body

# Check the NOC integration targets and release comments:
gh issue view 15 --repo omriariav/amq-noc --comments
gh issue view 16 --repo omriariav/amq-noc --comments
gh issue view 14 --repo omriariav/amq-noc --comments
gh issue view 5 --repo omriariav/amq-noc --comments
```

Release closeout checklist completed for v0.6.0:

- Re-ran the full gates after commit:
  `git diff --check`, `go vet ./...`, `go test ./...`, `make ci`.
- Smoked the RC against this workstream:
  `amq-noc --actions --root /Users/omri.a/Code/amq-noc/.agent-mail --filter amq-noc-0-1-0 --scope session`.
- Confirmed published session `status` includes `--json` and the published resume
  variants map to the exact `amq-squad resume --session ...` commands.
- Confirmed the status pulse can show `waiting` without inflating `blocked` for an
  awaiting QA/review/peer dependency.
- Confirmed handled operator approve/reply/deny answers clear `needs-you` on the
  next refresh; direct message and pane prompt results are labeled as non-clearing.
- Confirmed no `focus`/`send`/`attach_control` rows unless amq-squad publishes them
  with `available:true`; this should be shown as unavailable runtime capability,
  not a NOC failure.
- Closed #5, #14, #15, and #16 on release.

## amq-noc 0.2.2 Goal

Ship the post-0.2.1 TUI operator polish patch.

- Show the resolved binary version in the live/static NOC header so operators
  can confirm which build they are looking at.
- Start the live TUI with stale/idle projects hidden by default; keep
  `--show-stale` and `h` as explicit reveal controls.
- Include the effective amq-squad named profile in generated helper commands.
- Prefer the single active session for project-row helper commands, avoiding
  accidental fallback to a profile's older configured workstream.
- Add wrapped right-pane helper commands plus `C copy-cmd`, a numbered
  clipboard picker that copies the exact command.
- Offer both tmux launch choices in helper commands:
  `--target current-window` and `--target new-session`.
- Make `Del` row-sensitive: project rows delete team profiles, named session
  rows remove sessions, and root AMQ mailbox rows explain that they are not
  removable sessions.

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
