# amq-noc

NOC command center for AMQ and amq-squad teams.

`amq-noc` is the operator console for many AMQ and amq-squad workstreams. It
discovers projects under one or more roots, shows which teams are alive, stale,
waiting, or waiting for the human operator, and exposes preview-first controls
for recovery and messaging.

`amq-squad` remains the project-local lifecycle tool. `amq-noc` observes and
orchestrates those teams from above.

## What it shows

The primary visible statuses are intentionally simple:

- `running`: at least one agent in the team/session is active.
- `needs-you`: an agent sent a structural question to the operator mailbox.
- `waiting`: work is not actively running and is not waiting on the operator.
- `stale`: the team/session is dead or old enough to demote.

Thread-level evidence such as blocked, gated, and at-risk is still collected for
detail panes and diagnostics, but the main tree leads with the operational state
of projects, sessions, and agents.

## Features

- multi-project NOC TUI
- machine-readable snapshots
- flat action queue
- AMQ inbox, DLQ, receipts, and ops commands
- preview-first and confirm-gated controls that execute through `amq-squad`
- structural operator gates through a virtual operator mailbox, usually `user`
- support for `amq-squad v1.4.1` operator metadata and custom operator handles

## Install

```sh
go install github.com/omriariav/amq-noc/cmd/amq-noc@v0.1.0
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
