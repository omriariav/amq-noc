# amq-noc

NOC command center for AMQ and amq-squad teams.

`amq-noc` is the operator console for many AMQ and amq-squad workstreams. It
discovers projects under one or more roots, shows which teams are alive, stale,
waiting, or waiting for the human operator, and exposes preview-first controls
for recovery and messaging.

`amq-squad` remains the project-local lifecycle tool. `amq-noc` observes and
orchestrates those teams from above.

## Project links

- `amq-noc`: <https://github.com/omriariav/amq-noc>
- `amq`: <https://github.com/omriariav/agent-message-queue>
- `amq-squad`: <https://github.com/omriariav/amq-squad>

Relevant maintainers:

- [`@omriariav`](https://github.com/omriariav): `amq-noc` and `amq-squad`
- [`@avivsinai`](https://github.com/avivsinai): AMQ / agent-message-queue

## How the pieces fit

The AMQ stack has three layers:

- `amq`: the mailbox and routing layer maintained by
  [`@avivsinai`](https://github.com/avivsinai). It stores messages, threads,
  inboxes, DLQ entries, receipts, and operator replies.
- `amq-squad`: the project-local team layer maintained by
  [`@omriariav`](https://github.com/omriariav). It creates team profiles,
  launches and resumes agents, writes team rules, and exposes project-scoped
  status.
- `amq-noc`: the cross-project operator layer maintained by
  [`@omriariav`](https://github.com/omriariav). It scans many AMQ roots, shows
  which teams need attention, and runs preview-first controls through `amq` and
  `amq-squad`.

Use `amq-squad` when you are inside one project and managing that team. Use
`amq-noc` when you want a network-operations view across many projects and
sessions.

## What it shows

The primary visible statuses are intentionally simple:

- `online`: at least one agent in the team/session is live, with no current wait detected.
- `needs-you`: an agent sent a structural question to the operator mailbox.
- `blocked`: an agent declared a current hard stop such as no-go, unsafe, or
  cannot-proceed.
- `waiting`: a live agent is waiting on QA, review, peer reply, merge, release
  artifact, or another normal non-human coordination step.
- `stale`: the team/session is dead or old enough to demote.

Thread-level evidence such as gated and at-risk is still collected for
detail panes and diagnostics, but the main tree leads with the operational state
of projects, sessions, and agents.

Older blocked evidence remains visible in thread detail/history, but a newer
clear or waiting signal softens the primary row back to `online` or `waiting`.

JSON snapshots use the same primary vocabulary as the TUI. In `v0.2.1`,
`state` and `reason_code` values for live/no-wait rows changed from `running` to
`online`; filters still accept `running` as an alias for `online`.

The `agents_alive` count is the visible-online count: it includes fresh-presence
agents (dead-mailbox-live, reachable without a verified pid) and matches what the
TUI renders as online. Non-human waits (blocked, gated, at-risk) stay gated to
operationally live agents, so fresh presence alone never promotes a wait.

## Features

- multi-project NOC TUI
- machine-readable snapshots
- flat action queue
- AMQ inbox, DLQ, receipts, and ops commands
- preview-first and confirm-gated controls that execute through `amq-squad`
- structural operator gates through a virtual operator mailbox, usually `user`
- support for `amq-squad` operator metadata and published v1.5 runtime actions
- truthful TUI help/footer generated from the handled keymap
- context-sensitive footer actions that only advertise controls valid for the
  selected row
- session detail ordering that leads with active `needs-you`, otherwise newest
  current activity
- canonical `C copy-cmd` picker for exact command copy, with right-pane fallback
  commands kept as deterministic preview hints
- tmux recovery helpers for both current-window and new-session launch targets
- row-sensitive delete behavior for team profiles, named sessions, and root AMQ
  mailbox rows
- runtime-action helper commands for project, session, and agent rows
- published `amq-squad v1.5` runtime-action consumption in CLI action JSON and
  the `C copy-cmd` picker, including the v1.5.2+ session action catalog, with
  fallback generation for older contracts
- JSON/TUI status alignment for fresh-presence `dead-mailbox-live` agents
- clipboard paste support in filter and action input prompts
- lead-agent orchestration awareness for `amq-squad v1.7` teams: a `(lead)`
  badge and lead-first ordering, an `(orchestrated)` session marker, a
  per-child lead-exchange digest, brief Goal context in the session pane, a
  deterministic lead-down hint, additive JSON fields, and `orchestrated` /
  `lead:<role>` filters
- configured-workstream derivation in the new-session flow: the prompt leads
  with the team's configured workstream, warns when a typed name diverges
  (a divergent name creates a NEW workstream with a stub brief), and the
  copy-command panel shows the resolved workstream per launch action
- offline-presence truth: a cleanly stopped agent (presence `offline` plus a
  dead recorded PID) classifies stale instead of reading online for the 90s
  freshness window
- executable lifecycle from the NOC: `U up` joins `S stop` / `R resume` /
  `X restart` through the preview + confirm + exec seam; up pins no session
  (amq-squad derives the workstream from the team config), and a successful
  resume/restart/up copies the exact `tmux -CC attach` command to the
  clipboard so the new session is one paste away
- `L direct-lead`: the directive flow for orchestrated squads, delivered to
  the lead's pane when it is live (busy-guarded, never forced) or to its AMQ
  inbox when it is down, with the channel labeled either way
- message kind selector sourced from the recognized AMQ kinds (status, todo,
  answer, review_request, review_response, decision, question)
- `v output`: read-only latest-output preview per agent (live pane tail via
  the published pane id, else the agent's newest AMQ message)
- `o focus`: read-only pane focus through the published runtime contract,
  confirm-gated, with a copyable fallback outside tmux

## Install

```sh
go install github.com/omriariav/amq-noc/cmd/amq-noc@v0.8.0
```

Requirements:

- Go 1.25+
- `amq`
- `amq-squad` v1.5.2 or newer for the session action catalog; older builds keep
  deterministic fallback commands
- `tmux`

## Quick Start

```sh
amq-noc --root ~/Code
amq-noc --filter needs-you
amq-noc --once --root ~/Code
amq-noc --json --root ~/Code | jq .
amq-noc --actions --root ~/Code --filter needs-you
```

Bare `amq-noc` opens the live TUI. `amq-noc noc` is the explicit form of the same
command. `amq-noc version` prints the installed version.

Useful one-shot views:

```sh
# Full tree snapshot without entering the TUI.
amq-noc --once --tree --root ~/Code

# Only teams currently waiting on the operator.
amq-noc --once --root ~/Code --filter needs-you

# Hide old/dead sessions from the TUI.
amq-noc --root ~/Code --hide-stale
```

## TUI model

The left tree is intentionally quiet: project -> session -> agent, one line per
row. Thread subjects and historical evidence belong in the right pane, where the
selected row can explain `now`, recent threads, agents, and recovery commands.

The footer has two rows:

- navigation and view keys that are always available, such as movement, expand,
  filter, hide stale, refresh, help, and quit
- context-sensitive control keys for the selected row, such as approve/reply/deny
  on a `needs-you` ask, message/drain on an agent, lifecycle on a squad, or
  new-team/new-session on a project

Mutating controls open preview/confirm flows before anything is executed.

For contributor guidance on TUI keys, help, and footer tests, see
[`docs/tui-keymap.md`](docs/tui-keymap.md).

## Operator gates

`needs-you` is deterministic. NOC does not infer human action from broad prose in
agent-to-agent status messages. A team needs the human only when an agent sends a
question, review request, or decision request to the configured operator handle.

Default operator handle:

```text
user
```

Example gate:

```sh
amq send \
  --to user \
  --thread gate/manual-rc \
  --kind question \
  --subject "APPROVAL: manual RC test + commit decision" \
  --body "Please test the RC, then approve commit or request changes."
```

The operator replies on the same thread:

```sh
amq send \
  --me user \
  --to fullstack \
  --thread gate/manual-rc \
  --kind answer \
  --subject "APPROVED: manual RC test + commit decision" \
  --body "Approved after RC test."
```

After the reply, the gate clears automatically because the latest message is no
longer addressed to the operator.

For more detail, see [`docs/operator-gate.md`](docs/operator-gate.md).

## Orchestrated squads

`amq-squad v1.7` teams can be lead-agent orchestrated: `team.json` carries
`orchestrated: true` plus `lead: <role>`, one member drives the others, and
children push reports to the lead over AMQ (`status`, `question`,
`review_request` on `p2p/<lead>__<child>` threads). The NOC is the human's
client for supervising those squads:

- the lead carries a `(lead)` badge and sorts first within its state tier;
  the session row is marked `(orchestrated)`
- the session pane leads with the lead's identity and the brief's Goal line
  (`.amq-squad/briefs/<session>.md`), then a per-child digest of the newest
  lead exchange
- a dead lead with live children surfaces an explicit lead-down warning;
  the five primary states are unchanged
- `--filter orchestrated` and `--filter lead:<role>` narrow to orchestrated
  workstreams; JSON snapshots add `orchestrated`, `lead`, `lead_handle`,
  `lead_down`, and per-agent `is_lead` (all additive)

Directing the lead is first-class: `L` on an orchestrated session (or the
lead's row) opens a multi-line directive compose with preview + confirm. A
live lead receives it in its pane through amq-squad's busy-guarded `send`
(the NOC never passes `--force`); a down lead receives a durable AMQ message
on the `p2p/<lead>__<operator>` thread instead, read on its next drain or
wake. Directives never clear operator gates, and the result note names the
channel that delivered.

Teams without the v1.7 fields render exactly as before.

## Runtime actions

amq-squad owns runtime orchestration (start / stop / resume / focus / open /
prompt-delivery and the tmux mechanics behind them). amq-noc consumes published
`amq-squad v1.5` action metadata when available, renders it as operator UI,
lets you pick and copy the exact command (`C`), and falls back to deterministic
NOC-generated commands when runtime support is absent or partial. With
`amq-squad v1.5.2+`, session-row actions come from the published `data.actions[]`
catalog. Published-but-unavailable actions such as agent `focus`, `send`, or
session `attach_control` are omitted instead of replaced with raw tmux
orchestration. The NOC never drives the runtime itself.

For the consume/orchestrate boundary and fallback behavior, see
[`docs/runtime-actions.md`](docs/runtime-actions.md).

## Machine-readable snapshots

Use JSON mode for scripts and dashboards:

```sh
amq-noc --root ~/Code --json | jq '.data.rollup'
```

Use the flat action queue to inspect controls without entering the TUI:

```sh
amq-noc --root ~/Code --actions --json | jq '.data.actions[] | {name, command}'
```

Mutating actions are confirm-gated. Dry-run first:

```sh
amq-noc --filter project:amq-noc \
  --run-action new_session \
  --set session=issue-97 \
  --dry-run \
  --json
```

## Team compatibility

`amq-noc` reads `amq-squad` team profiles and supports the v1.4 operator contract:

- schema 3 teams advertise `operator` and `capabilities.operator_gates`
- legacy schema 1/2 teams default to a non-runnable `user` operator gate
- teams can opt out with `--no-operator`
- custom operator handles are honored in reads and generated AMQ actions
- legacy teams with a runnable member named `user` do not get an implicit
  operator gate, avoiding false `needs-you`

Recommended companion version for published runtime actions and the
orchestrated/lead team contract:

```sh
go install github.com/omriariav/amq-squad/cmd/amq-squad@v1.7.0
```

Older `amq-squad` builds keep deterministic fallback action commands.

Recommended health check:

```sh
amq-squad version
amq-squad team profiles --project /path/to/project --json
amq-noc --root ~/Code --json | jq '.data.rollup'
```

## Release checks

```sh
gofmt -l .
git diff --check
go test ./...
go vet ./...
make ci
go build -o /tmp/amq-noc-rc ./cmd/amq-noc
/tmp/amq-noc-rc --once --root ~/Code
```
