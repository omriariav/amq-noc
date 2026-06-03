# amq-noc 0.1.0 / amq-squad 1.3.0 Split Plan

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
go build ./cmd/amq-noc
amq-noc --once --root /Users/omri.a/Code
amq-noc --json --root /Users/omri.a/Code
amq-noc --actions --root /Users/omri.a/Code --filter needs-you
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
