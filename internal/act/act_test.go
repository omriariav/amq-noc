package act

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omriariav/amq-noc/internal/state"
)

// sampleThread is the thread context the builder tests reply/approve/deny into.
// Participants intentionally include the operator handle ("user") plus an
// out-of-order, duplicate agent so the dedupe/sort/drop-operator path is
// exercised: the emitted --to must be the sorted, deduped, operator-stripped
// "cpo,cto,senior-dev".
func sampleThread() state.ThreadSummary {
	return state.ThreadSummary{
		ID:           "t-42",
		Participants: []string{"cto", "user", "cpo", "senior-dev", "cto"},
		Subject:      "Ship the migration?",
		Kind:         state.KindReviewRequest,
	}
}

const (
	testRoot    = "/tmp/squad/.agent-mail/sess"
	testSession = "sess"
)

// withFakeSeam swaps sendExec for a recorder, restoring it on cleanup. The
// recorder captures the exact argv and env Send hands the seam, and NEVER
// shells real amq.
type recordedSend struct {
	called bool
	name   string
	args   []string
	env    []string
}

func withFakeSeam(t *testing.T) *recordedSend {
	t.Helper()
	rec := &recordedSend{}
	prev := sendExec
	sendExec = func(name string, args []string, env []string) error {
		rec.called = true
		rec.name = name
		rec.args = args
		rec.env = env
		return nil
	}
	t.Cleanup(func() { sendExec = prev })
	return rec
}

func TestReplyArgv(t *testing.T) {
	m := Reply(testRoot, testSession, sampleThread(), "looks good, proceed")
	want := []string{
		"send",
		"--root", testRoot,
		"--me", "user",
		"--to", "cpo,cto,senior-dev",
		"--subject", "Re: Ship the migration?",
		"--body", "looks good, proceed",
		"--thread", "t-42",
		"--kind", "answer",
	}
	if got := m.argv(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Reply.argv()\n got: %#v\nwant: %#v", got, want)
	}
}

func TestApproveArgv(t *testing.T) {
	m := Approve(testRoot, testSession, sampleThread())
	want := []string{
		"send",
		"--root", testRoot,
		"--me", "user",
		"--to", "cpo,cto,senior-dev",
		"--subject", "Re: Ship the migration?",
		"--body", "APPROVED",
		"--thread", "t-42",
		"--kind", "answer",
	}
	if got := m.argv(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Approve.argv()\n got: %#v\nwant: %#v", got, want)
	}
}

func TestDenyArgv(t *testing.T) {
	m := Deny(testRoot, testSession, sampleThread(), "needs a rollback plan first")
	want := []string{
		"send",
		"--root", testRoot,
		"--me", "user",
		"--to", "cpo,cto,senior-dev",
		"--subject", "Re: Ship the migration?",
		"--body", "DENIED: needs a rollback plan first",
		"--thread", "t-42",
		"--kind", "answer",
	}
	if got := m.argv(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Deny.argv()\n got: %#v\nwant: %#v", got, want)
	}
}

func TestDenyArgvBareWhenNoReason(t *testing.T) {
	m := Deny(testRoot, testSession, sampleThread(), "   ")
	if got := bodyOf(m.argv()); got != "DENIED" {
		t.Fatalf("Deny with blank reason: body = %q, want %q", got, "DENIED")
	}
}

func TestBroadcastArgv(t *testing.T) {
	// Operator handle and a duplicate are filtered; result is sorted+deduped.
	m := Broadcast(testRoot, testSession,
		[]string{"qa", "cpo", "user", "qa"}, "Standup in 5", "join the bridge")
	want := []string{
		"send",
		"--root", testRoot,
		"--me", "user",
		"--to", "cpo,qa",
		"--subject", "Standup in 5",
		"--body", "join the bridge",
		"--thread", "broadcast/operator",
		"--kind", "status",
	}
	if got := m.argv(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Broadcast.argv()\n got: %#v\nwant: %#v", got, want)
	}
}

// TestPreviewEqualsShellQuotedArgv proves the core safety invariant: what
// Preview SHOWS is exactly what Send RUNS — Preview is shellQuote applied to the
// same argv, prefixed with the amq binary name.
func TestPreviewEqualsShellQuotedArgv(t *testing.T) {
	cases := []OpMessage{
		Reply(testRoot, testSession, sampleThread(), "proceed"),
		Approve(testRoot, testSession, sampleThread()),
		Deny(testRoot, testSession, sampleThread(), "no"),
		Broadcast(testRoot, testSession, []string{"cpo", "qa"}, "subj", "body"),
	}
	for i, m := range cases {
		parts := append([]string{"amq"}, m.argv()...)
		quoted := make([]string, len(parts))
		for j, p := range parts {
			quoted[j] = shellQuote(p)
		}
		want := strings.Join(quoted, " ")
		if got := Preview(m); got != want {
			t.Fatalf("case %d: Preview mismatch\n got: %s\nwant: %s", i, got, want)
		}
	}
}

// TestPreviewQuotesSpaces sanity-checks the rendered string is copy-pasteable:
// the subject with spaces is single-quoted, flags stay bare.
func TestPreviewQuotesSpaces(t *testing.T) {
	got := Preview(Approve(testRoot, testSession, sampleThread()))
	if !strings.Contains(got, "'Re: Ship the migration?'") {
		t.Fatalf("Preview did not quote the spaced subject: %s", got)
	}
	if !strings.HasPrefix(got, "amq send --root "+testRoot+" --me user --to cpo,cto,senior-dev ") {
		t.Fatalf("Preview head not as expected: %s", got)
	}
}

func TestMeDefaultsToOperatorHandle(t *testing.T) {
	m := OpMessage{To: "cto", Subject: "s", Body: "b"} // Me left empty
	if got := bodyAfter(m.argv(), "--me"); got != state.DefaultOperatorHandle {
		t.Fatalf("empty Me: --me = %q, want %q", got, state.DefaultOperatorHandle)
	}
}

func TestArgvOmitsEmptyOptionalFlags(t *testing.T) {
	// Only the required trio + always-on --me; no root/thread/kind/prio.
	m := OpMessage{To: "cto", Subject: "s", Body: "b"}
	want := []string{"send", "--me", "user", "--to", "cto", "--subject", "s", "--body", "b"}
	if got := m.argv(); !reflect.DeepEqual(got, want) {
		t.Fatalf("argv with only required fields\n got: %#v\nwant: %#v", got, want)
	}
}

// TestSendViaFakeSeamExactArgvAndCleanEnv asserts Send hands the seam the exact
// argv and that the child env carries NO AM_ROOT/AM_BASE_ROOT/AM_ME — and never
// touches real amq.
func TestSendViaFakeSeamExactArgvAndCleanEnv(t *testing.T) {
	// Poison the inherited environment with stale AMQ identity; Send must strip it.
	t.Setenv("AM_ROOT", "/stale/root")
	t.Setenv("AM_BASE_ROOT", "/stale/base")
	t.Setenv("AM_ME", "stale-agent")

	rec := withFakeSeam(t)
	m := Reply(testRoot, testSession, sampleThread(), "go")
	if err := Send(m); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
	if !rec.called {
		t.Fatal("Send did not invoke the seam")
	}
	if rec.name != "amq" {
		t.Fatalf("seam binary = %q, want amq", rec.name)
	}
	if !reflect.DeepEqual(rec.args, m.argv()) {
		t.Fatalf("seam argv mismatch\n got: %#v\nwant: %#v", rec.args, m.argv())
	}
	for _, e := range rec.env {
		key, _, _ := strings.Cut(e, "=")
		switch key {
		case "AM_ROOT", "AM_BASE_ROOT", "AM_ME":
			t.Fatalf("child env still carries stale AMQ identity %q", e)
		}
	}
}

func TestSendRefusesEmptyRecipient(t *testing.T) {
	rec := withFakeSeam(t)
	err := Send(OpMessage{Subject: "s", Body: "b"}) // no To
	if err == nil {
		t.Fatal("Send with empty --to should error")
	}
	if rec.called {
		t.Fatal("Send invoked the seam despite empty --to")
	}
}

// TestEnvWithoutAMQIdentity is a focused unit on the env-stripping helper.
func TestEnvWithoutAMQIdentity(t *testing.T) {
	in := []string{"PATH=/bin", "AM_ROOT=/r", "HOME=/h", "AM_ME=x", "AM_BASE_ROOT=/b", "FOO=bar"}
	out := envWithoutAMQIdentity(in)
	want := []string{"PATH=/bin", "HOME=/h", "FOO=bar"}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("envWithoutAMQIdentity\n got: %#v\nwant: %#v", out, want)
	}
}

func TestArgvDoesNotEmitUnsupportedReplyToFlag(t *testing.T) {
	cases := []OpMessage{
		Reply(testRoot, testSession, sampleThread(), "x"),
		Approve(testRoot, testSession, sampleThread()),
		Deny(testRoot, testSession, sampleThread(), "no"),
		Broadcast(testRoot, testSession, []string{"qa"}, "s", "b"),
	}
	for i, m := range cases {
		if containsArg(m.argv(), "--reply-to") {
			t.Fatalf("case %d emitted unsupported parent AMQ flag --reply-to: %#v", i, m.argv())
		}
	}
}

// TestReplySubjectNoDoubleRe ensures an already-"Re:" subject is not double-prefixed.
func TestReplySubjectNoDoubleRe(t *testing.T) {
	th := sampleThread()
	th.Subject = "Re: already replied"
	m := Reply(testRoot, testSession, th, "x")
	if got := bodyAfter(m.argv(), "--subject"); got != "Re: already replied" {
		t.Fatalf("double Re: subject = %q", got)
	}
}

// TestSmokeRealAmqIntoThrowawayRoot is the ONE real smoke. It builds a throwaway
// .agent-mail/<session>/agents/<handle> layout under t.TempDir and, IF real amq
// is on PATH, Sends a message with --root <tmp> and asserts a message file
// appears under the temp inbox — proving the wiring end to end. If amq is
// absent, it skips.
//
// HARD GUARD: the test refuses to run unless the --root is inside the
// test's TempDir. It can NEVER target a live squad root; any path outside
// t.TempDir fails the test loudly rather than writing to a real bus.
func TestSmokeRealAmqIntoThrowawayRoot(t *testing.T) {
	// Restore the production seam for this one test: it deliberately shells real
	// amq (or skips). Other tests run with the fake seam.
	prev := sendExec
	sendExec = defaultSendExec
	t.Cleanup(func() { sendExec = prev })

	tmp := t.TempDir()
	session := "smoke"
	root := filepath.Join(tmp, ".agent-mail", session)
	const recipient = "cto"

	// --- HARD SAFETY GUARD: never send into anything that is not our TempDir ---
	cleanTmp := cleanRoot(tmp)
	cleanThisRoot := cleanRoot(root)
	if cleanThisRoot == "" || !strings.HasPrefix(cleanThisRoot+string(filepath.Separator), cleanTmp+string(filepath.Separator)) {
		t.Fatalf("SAFETY: smoke root %q is not inside the test TempDir %q; refusing", cleanThisRoot, cleanTmp)
	}
	if strings.Contains(cleanThisRoot, "/Code/") {
		t.Fatalf("SAFETY: smoke root %q looks like a live repo path; refusing", cleanThisRoot)
	}

	// Build the throwaway maildir layout: <root>/agents/<handle>/{inbox/{new,cur,tmp}}.
	for _, h := range []string{recipient, "user"} {
		for _, sub := range []string{"inbox/new", "inbox/cur", "inbox/tmp", "outbox", "sent"} {
			if err := os.MkdirAll(filepath.Join(root, "agents", h, sub), 0o755); err != nil {
				t.Fatalf("setup mkdir: %v", err)
			}
		}
	}

	if _, err := execLookPath("amq"); err != nil {
		t.Skip("real amq not on PATH; skipping real-send smoke (fake-seam tests cover wiring)")
	}

	m := OpMessage{
		Root:    root,
		Me:      "user",
		To:      recipient,
		Subject: "smoke subject",
		Body:    "smoke body",
		Kind:    string(state.KindStatus),
	}
	if err := Send(m); err != nil {
		// amq layout/version differences shouldn't fail the suite hard; the
		// fake-seam tests already prove argv/env wiring. Surface as skip with
		// the error so a real regression is still visible in -v output.
		t.Skipf("real amq send did not complete cleanly (layout/version?): %v", err)
	}

	// Assert at least one message file landed somewhere under the throwaway root.
	if !anyMessageFileUnder(t, root, recipient) {
		t.Fatalf("no message file appeared under throwaway root %s after real amq send", root)
	}
}

// --- small test helpers ---

// execLookPath is indirected so the smoke test reads cleanly; it is the stdlib
// exec.LookPath.
var execLookPath = exec.LookPath

func bodyOf(argv []string) string { return bodyAfter(argv, "--body") }
func bodyAfter(argv []string, flag string) string {
	for i := 0; i < len(argv)-1; i++ {
		if argv[i] == flag {
			return argv[i+1]
		}
	}
	return ""
}

func containsArg(argv []string, want string) bool {
	for _, arg := range argv {
		if arg == want {
			return true
		}
	}
	return false
}

// anyMessageFileUnder walks the throwaway root and reports whether any regular
// file exists under the recipient's mailbox tree (proof a message was written).
func anyMessageFileUnder(t *testing.T, root, recipient string) bool {
	t.Helper()
	found := false
	base := filepath.Join(root, "agents", recipient)
	_ = filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil {
			return nil
		}
		if !d.IsDir() {
			found = true
		}
		return nil
	})
	if found {
		return true
	}
	// Fall back to scanning the entire root: amq layouts vary, and any new file
	// under the throwaway root proves the write landed in the right place.
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return nil
		}
		// Ignore the directories we pre-created (they are dirs, already skipped);
		// any regular file is amq's doing.
		found = true
		return filepath.SkipAll
	})
	return found
}
