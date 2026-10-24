# amq-noc

Command center for amq-squad squads.

`amq-noc` 1.0 shows every seat and task of your squads in one terminal screen:
rows grouped by project path, four status buckets as tab filters, a details
pane for the selected row, and keyboard navigation. It is the only view; the
multi-root board of 0.x is gone.

```
Agent command center  Group: Project
[All 3]  Needs you 1   Working 1   Ready 1   Inactive 0               tab/⇧tab filter
─────────────────────────────────────────────────────────────────────────────────────
/Users/owner/code/app  3                                  │ Task details
› ! Release v1.2.0               Needs you        5m ago  │
  ● Fix the parser               Working         30s ago  │ Release v1.2.0
  ◌ impl-2 (implementer)         Ready           10m ago  │ ● Needs you  needs_you gate_open
```

## Project links

- `amq-noc`: <https://github.com/omriariav/amq-noc>
- `amq`: <https://github.com/omriariav/agent-message-queue>
- `amq-squad`: <https://github.com/omriariav/amq-squad>

## Install

```sh
go install github.com/omriariav/amq-noc/cmd/amq-noc@latest
```

Requirements: Go 1.25 or newer to build, `amq-squad` 3.0 or newer and `amq`
on `PATH` to run.

## What it reads

One input: `amq-squad status --json` (schema 1, the status contract of
amq-squad 3.0), or `amq-squad status --fleet --json` with `--fleet`. It runs
the call every 5 seconds.

amq-noc never reads `.amq-squad/` files and never composes an amq-squad
command. The buckets (Needs you, Working, Ready, Inactive) come from the
contract's `bucket` and `bucket_marker` fields; a row the contract could not
classify shows as Inactive with an `(unknown)` marker.

Its controls are three:

- `n` runs the contract's `actions.new_task` argv, adding `--flag value` for
  each input you fill in.
- `enter` opens the AMQ conversation with the squad's lead, read with
  `amq thread` and answered with `amq send` (a staged confirm shows the
  recipient and thread before it sends). The lead is the status's
  `lead_handle`, else the seat whose role is `lead`; with neither, the details
  pane says "no lead seat in status".
- A gate that the attention queue ties to a row shows in the details pane.
  Answering a gate is not done here.

## Keys

| key | action |
| --- | --- |
| up / down, j / k | move |
| g / G | first / last row |
| tab / shift+tab | cycle All, Needs you, Working, Ready, Inactive |
| enter | open the lead conversation; on "Show more", expand the group |
| n | new task |
| esc | back; from a filter tab, back to All |
| r | refresh now |
| ? | help |
| q, ctrl+c | quit |

## Usage

```sh
amq-noc            # one squad: amq-squad status --json
amq-noc --fleet    # all squads: amq-squad status --fleet --json
amq-noc --once     # print one frame and exit (COLUMNS and LINES set the size)
amq-noc version
```

`amq-squad` (3.0 or later) and `amq` must be on `PATH`.

## Develop

```sh
make ci                       # gofmt check, go vet, go test
GOOS=linux go build ./...
GOOS=windows go build ./...
go test ./internal/center -update   # rewrite the golden text snapshots
```

`internal/center/testdata/status-v1.golden.json` is amq-squad's
`internal/status/testdata/status-v1.golden.json`, vendored byte for byte; a
test compares it to the committed sha256 next to it. Re-vendor both together.

The design is in amq-squad's `docs/v3-plan.md` (4.6, 4.7) and
`docs/status-contract.md`.
