# TUI Keymap Contract

`amq-noc` treats the live TUI keymap as product surface. Help text, footer
legends, and the router must agree because operators use the console to control
live AMQ / amq-squad teams.

## Source of Truth

The authoritative live keymap is `internal/console/noc_keymap.go`.

It defines:

- navigation keys: read-only movement and expand/collapse
- view keys: filter, hide stale, flow, refresh, help, quit/back
- action keys: preview-first controls such as approve, reply, deny, message,
  broadcast, lifecycle, new-team, and new-session

The help overlay and footer render from that manifest. Do not add a live TUI key
by editing help or footer strings directly.

## Context Footer

The footer has two rows:

- navigation/view keys that are always available
- action keys that are valid for the selected row now

Action availability is stateful. For example:

- approve/reply/deny require an active `needs-you` thread
- lifecycle and broadcast require a resolvable squad
- delete requires a configured team profile
- new-session requires a launchable profile
- message/drain require an agent row

If an invalid action key is pressed anyway, the handler should set a short
`actNote` explaining why it cannot run.

## Tests

Use these guards when changing TUI behavior:

```sh
go test ./internal/console -run 'TestKeymapContract|TestContextFooter|TestTreeIA'
go test ./internal/cli -run TestNOCHelpHasNoDeadKeys
```

The tests enforce:

- every advertised manifest key is handled
- removed/deferred keys stay unadvertised and inert
- footer/help render from the manifest
- `noc --help` does not mention dead keys
- the context footer hides unavailable actions
- the left tree stays one line per row and does not leak thread subjects

Deferred features such as palette, jump/open, inbox/read, DLQ, alert mute, and
timeline must be reintroduced by first wiring behavior, then adding the key to
the manifest and tests.
