# amq-noc

NOC command center for AMQ and amq-squad sessions.

`amq-noc` owns operator visibility and control across many AMQ roots:

- multi-project NOC TUI
- machine-readable snapshots
- flat action queue
- tmux jump/focus
- AMQ inbox/DLQ/receipts/ops panes
- preview-first and confirm-gated controls that execute through `amq-squad`

`amq-squad` remains the project-local team lifecycle tool. `amq-noc` observes and
orchestrates those teams from above.

## Quick Start

```sh
go install github.com/omriariav/amq-noc/cmd/amq-noc@latest

amq-noc --root ~/Code
amq-noc --filter needs-you
amq-noc --once --root ~/Code
amq-noc --json --root ~/Code | jq .
amq-noc --actions --root ~/Code --filter needs-you
```

Requires Go 1.25+, `amq`, `amq-squad`, and `tmux`.
