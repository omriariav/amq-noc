# Operator-gate protocol

When a squad is holding for a human-only decision (manual RC test, approval,
commit go/no-go, or any operator-owned call), the NOC must show **needs-you**, not
waiting. The workstream is blocked on the operator, and that is a human action
item, not an agent-to-agent wait.

This is enforced **structurally and deterministically**. The NOC never infers a
human gate from prose over a peer-to-peer status. A hold ACK between agents (even
one whose body says "holding for manual RC") is not a gate. The only signal the
NOC treats as a gate is an ask **addressed to the operator**.

## How amq-noc detects a gate (observer side)

`coordinateSession` (internal/state/snapshot.go) scans every agent mailbox in a
session and, in addition, the operator mailbox at
`<sessionRoot>/agents/<operatorHandle>` (default `user`) when it exists. So a
message sent `--to user` lands in `agents/user/inbox/new/...` and is scanned like
any other.

The collapse layer (internal/state/collapse.go) then classifies a thread as
`needs-you` when its latest message is `addressedTo(operator)` and
`operatorMessageNeedsAction` matches: an action subject/body marker (approval,
approve, decide, confirm, "?", "please", "ok to", ...), a `declaresUserWait`
marker, or a `review_request` kind, and it is not a bare status/ack notice. The
`NeedsYouOwner` is the asker (the agent that is holding), so the gate surfaces at
project, session, and agent level, and the right pane renders the full ask body
plus an approve/deny/reply CTA.

The gate clears automatically: once the operator replies on the thread, the latest
message is no longer addressed to the operator, so `needs-you` falls away. No
separate "resolve" step is required. AMQ thread resolution
(`answer` / `review_response`) is honored the same way.

## How an agent raises a gate (producer side, squad convention)

When you must hold for an operator decision:

1. Send a structural ask **to the operator**, with an action subject:

   ```
   amq send --to user --thread gate/<topic> \
     --kind question \
     --subject "APPROVAL: <the decision you need>" \
     --body "<context> + the explicit ask + how to act"
   ```

2. Use a **stable thread id** (`gate/<topic>`) and reuse it rather than opening a
   new gate each time, so the NOC shows one needs-you, not a pile of duplicates.

3. Do **not** rely on a peer-to-peer ACK ("holding for manual RC") as the gate.
   That stays useful as evidence, but it is not the signal. Without a `to: user`
   ask the NOC correctly shows the team as waiting/stale, not needs-you.

4. Let the operator's reply clear it. Do not fabricate a resolution.

## Regression coverage

`internal/state/operator_gate_test.go` locks the three invariants:

- a structural operator gate (`to: user`, action subject) is needs-you, owned by
  the asker;
- a bare p2p hold ACK, even one that mentions manual RC or the user in prose, is
  not needs-you;
- the gate clears after the operator responds.
