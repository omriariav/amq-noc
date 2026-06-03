// Package act is the operator's AMQ-write layer: a typed, preview-first API for
// the human operator to write into AMQ (reply, approve, deny, broadcast).
//
// This is the mutating counterpart to the read-only state/console/noc packages.
// Because it WRITES to the bus, it is built safe-by-construction around three
// invariants (see GOAL.md "Mutation is deliberate + safe"):
//
//   - Preview-first. Every action renders the EXACT command it would run via
//     Preview, without executing anything. What Preview shows is byte-for-byte
//     what Send runs (Preview is shellQuote(m.argv()), Send runs m.argv()).
//   - Inject-the-seam. Send shells `amq` only through the package-level sendExec
//     seam (mirroring noc.switchExec). Tests swap the seam so they NEVER touch
//     the real amq binary or the live bus.
//   - Identity-stripped. Send clears inherited AM_ROOT/AM_BASE_ROOT/AM_ME from
//     the child environment (mirroring cli.envWithoutAMQIdentity) so a stale
//     shell identity cannot silently redirect the write away from the explicit
//     --root/--me passed on the wire.
//   - Parent-AMQ compatible. Send emits only flags supported by the upstream
//     `amq send` command. Reply metadata is stamped by AMQ itself when --root is
//     a session root.
//
// Nothing in this package runs at import or construction time. Building an
// OpMessage (directly or via the Reply/Approve/Deny/Broadcast convenience
// builders) is pure; only Send executes, and only via the seam.
package act

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omriariav/amq-noc/internal/state"
)

// OpMessage is a single operator-authored AMQ write. The zero value is not
// useful; build one via the Reply/Approve/Deny/Broadcast convenience builders,
// or populate the fields directly. Me defaults to state.DefaultOperatorHandle
// ("user") when left empty — the operator always writes as the human mailbox.
//
// An OpMessage is inert data: holding one performs no I/O. It becomes a command
// only through argv/Preview (pure) and Send (the only executing path).
type OpMessage struct {
	// Root is the .agent-mail root to write into (--root). When empty, no --root
	// flag is emitted and amq resolves the root from its own environment; the
	// convenience builders always populate it from the session's root so the
	// write is pinned, not ambient.
	Root string
	// Me is the sender mailbox handle (--me). Empty means DefaultOperatorHandle.
	Me string
	// To is the recipient list (--to). Multiple handles are comma-joined by the
	// builders; a caller setting this directly should pass an already-joined
	// "h1,h2" string. Required for a meaningful send.
	To string
	// Subject is the message subject (--subject).
	Subject string
	// Body is the message body (--body).
	Body string
	// Thread pins the message into an existing thread (--thread). Optional.
	Thread string
	// Kind is the AMQ message kind (--kind): answer, status, etc. Optional.
	Kind string
	// Priority is the message priority (--priority). Optional.
	Priority string
}

// sender is the injectable subprocess seam for Send. The default runs the real
// amq binary with AMQ identity stripped from its environment; tests swap it for
// a recorder so they never shell real amq. It mirrors noc.switchExec: a
// package-level var so the Send signature stays clean.
//
// name is the binary ("amq"), args is the full argv tail, and env is the child
// environment Send has already sanitized (AM_ROOT/AM_BASE_ROOT/AM_ME removed).
type sender func(name string, args []string, env []string) error

// sendExec is the production seam. Tests override it and restore it via defer.
var sendExec sender = defaultSendExec

// defaultSendExec runs the real command with the supplied (already-stripped)
// environment and discards stdout, returning amq's stderr on failure so a
// rejected write is legible to the operator.
func defaultSendExec(name string, args []string, env []string) error {
	cmd := exec.Command(name, args...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, msg)
		}
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

// me returns the sender handle, defaulting an empty Me to the operator handle.
func (m OpMessage) me() string {
	if h := strings.TrimSpace(m.Me); h != "" {
		return h
	}
	return state.DefaultOperatorHandle
}

// argv builds the EXACT `amq send ...` argument vector (without the leading
// "amq" binary name) that Send will execute and Preview will render. The flag
// order is fixed and deterministic so Preview output and tests are stable:
//
//	send --root R --me M --to T --subject S --body B --thread T --kind K
//	     --priority P
//
// Only --to/--subject/--body and --me are always present (--me is always
// emitted because the operator identity is load-bearing and must never be left
// to a stale environment). The optional flags (--root/--thread/--kind/
// --priority) are emitted only when their field is non-empty.
func (m OpMessage) argv() []string {
	args := []string{"send"}
	if r := strings.TrimSpace(m.Root); r != "" {
		args = append(args, "--root", r)
	}
	// --me is always emitted (defaulted to the operator handle) so identity is
	// explicit on the wire, never inherited.
	args = append(args, "--me", m.me())
	args = append(args, "--to", m.To)
	args = append(args, "--subject", m.Subject)
	args = append(args, "--body", m.Body)
	if t := strings.TrimSpace(m.Thread); t != "" {
		args = append(args, "--thread", t)
	}
	if k := strings.TrimSpace(m.Kind); k != "" {
		args = append(args, "--kind", k)
	}
	if p := strings.TrimSpace(m.Priority); p != "" {
		args = append(args, "--priority", p)
	}
	return args
}

// Preview renders the human-readable "this will run: amq send ..." string for
// an OpMessage WITHOUT executing anything. It is exact: the rendered command is
// shellQuote applied to the same argv() Send runs, so the operator confirms
// precisely what will hit the bus. No I/O, no side effects.
func Preview(m OpMessage) string {
	parts := append([]string{"amq"}, m.argv()...)
	quoted := make([]string, len(parts))
	for i, p := range parts {
		quoted[i] = shellQuote(p)
	}
	return strings.Join(quoted, " ")
}

// Send shells `amq send ...` via the injectable seam, writing the message to the
// bus. It is the ONLY executing path in this package.
//
// Identity safety: Send strips AM_ROOT/AM_BASE_ROOT/AM_ME from the child
// environment (mirroring cli.envWithoutAMQIdentity) so a stale shell identity
// cannot redirect the write — the explicit --root/--me on the argv are the sole
// source of truth.
//
// Send validates that --to is non-empty before invoking the seam: a write with
// no recipient is a no-op on the bus and almost certainly an operator mistake,
// so it fails fast rather than emitting a malformed command.
func Send(m OpMessage) error {
	if strings.TrimSpace(m.To) == "" {
		return fmt.Errorf("act: refusing to send with empty --to (no recipient)")
	}
	env := envWithoutAMQIdentity(os.Environ())
	return sendExec("amq", m.argv(), env)
}

// envWithoutAMQIdentity returns env with the AMQ identity variables removed, so
// a stale AM_ROOT/AM_BASE_ROOT/AM_ME from a previous shell session cannot
// override the explicit --root/--me the operator put on the wire. It mirrors
// cli.envWithoutAMQIdentity (that one is unexported in another package); the
// behavior is identical and intentionally kept in lockstep.
func envWithoutAMQIdentity(env []string) []string {
	remove := map[string]bool{
		"AM_ROOT":      true,
		"AM_BASE_ROOT": true,
		"AM_ME":        true,
	}
	out := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || !remove[key] {
			out = append(out, entry)
		}
	}
	return out
}

// shellQuote renders s as a single safe POSIX shell token for Preview. An empty
// string becomes ” (an explicit empty argument). Tokens containing only safe
// characters are returned bare; anything else is single-quoted with embedded
// single quotes escaped as '\”. Preview is display-only, but quoting keeps the
// shown command copy-pasteable and unambiguous.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if isShellSafe(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// isShellSafe reports whether s consists solely of characters that need no shell
// quoting: alphanumerics and the small set of punctuation common in amq tokens
// (handles, flags, thread ids, addresses, paths).
func isShellSafe(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case strings.ContainsRune("-_./@:,=+", r):
		default:
			return false
		}
	}
	return true
}

// --- Convenience builders: produce an OpMessage from NOC context ---
//
// Each builder takes the project's .agent-mail root and the amq session name
// plus (where relevant) a state.ThreadSummary, and returns a fully-pinned
// OpMessage. They are pure: building one performs no I/O. The operator still
// Previews and then Sends.

// Reply builds a reply into an existing thread. It addresses the thread's
// non-operator participants, sets kind=answer, pins --thread to the thread id,
// and relies on AMQ to stamp reply_to from --me and the session --root. Subject
// is "Re: <thread subject>".
func Reply(root, _ string, th state.ThreadSummary, body string) OpMessage {
	return OpMessage{
		Root:    root,
		Me:      state.DefaultOperatorHandle,
		To:      strings.Join(nonOperatorParticipants(th), ","),
		Subject: replySubject(th.Subject),
		Body:    body,
		Thread:  th.ID,
		Kind:    string(state.KindAnswer),
	}
}

// Approve builds an approval answer into a thread: an "APPROVED" body addressed
// to the thread's non-operator participants, kind=answer, pinned to the thread.
// This is the operator side of the ⏸ APPROVE needs-you tier.
func Approve(root, _ string, th state.ThreadSummary) OpMessage {
	return OpMessage{
		Root:    root,
		Me:      state.DefaultOperatorHandle,
		To:      strings.Join(nonOperatorParticipants(th), ","),
		Subject: replySubject(th.Subject),
		Body:    "APPROVED",
		Thread:  th.ID,
		Kind:    string(state.KindAnswer),
	}
}

// Deny builds a denial answer into a thread: a "DENIED" body carrying the
// operator's reason, addressed to the thread's non-operator participants,
// kind=answer, pinned to the thread. An empty reason yields a bare "DENIED".
func Deny(root, _ string, th state.ThreadSummary, reason string) OpMessage {
	body := "DENIED"
	if r := strings.TrimSpace(reason); r != "" {
		body = "DENIED: " + r
	}
	return OpMessage{
		Root:    root,
		Me:      state.DefaultOperatorHandle,
		To:      strings.Join(nonOperatorParticipants(th), ","),
		Subject: replySubject(th.Subject),
		Body:    body,
		Thread:  th.ID,
		Kind:    string(state.KindAnswer),
	}
}

// Broadcast builds a status broadcast to a set of squad handles. The operator
// handle is filtered out (you do not broadcast to yourself) and the remaining
// handles are sorted+deduped for a deterministic --to. Kind=status, no thread
// (a broadcast opens its own). AMQ stamps reply_to from --me and the session
// --root.
func Broadcast(root, _ string, handles []string, subject, body string) OpMessage {
	return OpMessage{
		Root:    root,
		Me:      state.DefaultOperatorHandle,
		To:      strings.Join(squadRecipients(handles), ","),
		Subject: subject,
		Body:    body,
		Kind:    string(state.KindStatus),
	}
}

// replySubject prefixes "Re: " to a subject unless it already carries one
// (case-insensitive). An empty subject yields a bare "Re:".
func replySubject(subject string) string {
	s := strings.TrimSpace(subject)
	if s == "" {
		return "Re:"
	}
	if strings.HasPrefix(strings.ToLower(s), "re:") {
		return s
	}
	return "Re: " + s
}

// nonOperatorParticipants returns the thread's participants with the operator
// handle removed, sorted and deduped, so a reply/approve/deny addresses the
// agents in the thread and never echoes back to the operator's own mailbox.
func nonOperatorParticipants(th state.ThreadSummary) []string {
	return filterDedupeSort(th.Participants, true)
}

// squadRecipients returns the broadcast handle list with the operator handle
// removed, sorted and deduped.
func squadRecipients(handles []string) []string {
	return filterDedupeSort(handles, true)
}

// filterDedupeSort trims, drops empties, optionally drops the operator handle,
// dedupes, and sorts the input for a deterministic recipient list.
func filterDedupeSort(in []string, dropOperator bool) []string {
	seen := map[string]bool{}
	var out []string
	for _, h := range in {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		if dropOperator && h == state.DefaultOperatorHandle {
			continue
		}
		if seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

// cleanRoot normalizes a root path for the safety guard in tests and any future
// caller that wants to compare roots. It is absolute+cleaned; empty stays empty.
// Kept unexported and small; used by the smoke-test guard.
func cleanRoot(root string) string {
	if root == "" {
		return ""
	}
	if abs, err := filepath.Abs(root); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(root)
}
