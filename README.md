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
- `waiting`: a live agent is waiting on another agent or non-human coordination.
- `stale`: the team/session is dead or old enough to demote.

Thread-level evidence such as blocked, gated, and at-risk is still collected for
detail panes and diagnostics, but the main tree leads with the operational state
of projects, sessions, and agents.

JSON snapshots use the same primary vocabulary as the TUI. In `v0.2.1`,
`state` and `reason_code` values for live/no-wait rows changed from `running` to
`online`; filters still accept `running` as an alias for `online`.

## Features

- multi-project NOC TUI
- machine-readable snapshots
- flat action queue
- AMQ inbox, DLQ, receipts, and ops commands
- preview-first and confirm-gated controls that execute through `amq-squad`
- structural operator gates through a virtual operator mailbox, usually `user`
- support for `amq-squad v1.4.1` operator metadata and custom operator handles
- truthful TUI help/footer generated from the handled keymap
- context-sensitive footer actions that only advertise controls valid for the
  selected row
- session detail ordering that leads with active `needs-you`, otherwise newest
  current activity
- wrapped right-pane helper commands with `C copy-cmd` for exact clipboard copy
- tmux recovery helpers for both current-window and new-session launch targets
- row-sensitive delete behavior for team profiles, named sessions, and root AMQ
  mailbox rows

## Install

```sh
go install github.com/omriariav/amq-noc/cmd/amq-noc@v0.2.2
```

Requirements:

- Go 1.25+
- `amq`
- `amq-squad` v1.4.1 or newer
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

Minimum supported companion version:

```sh
go install github.com/omriariav/amq-squad/cmd/amq-squad@v1.4.1
```

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
