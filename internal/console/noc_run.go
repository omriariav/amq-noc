// Package console — noc_run.go: wire the NOC Bubble Tea program and its data
// feeds, and the --once static render.
package console

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/omriariav/amq-noc/internal/noc"
	"github.com/omriariav/amq-noc/internal/state"
)

// NOCConfig is the NOC entrypoint's configuration.
type NOCConfig struct {
	Version       string
	Roots         []string
	Depth         int
	Thresholds    state.Thresholds
	Refresh       time.Duration
	Once          bool
	Out           io.Writer
	InitialFilter string
	// Tree forces the full root→project→session→agent expansion in the --once
	// static render (the old full board). Default --once leads with project
	// rollups + a needs-attention section. Ignored by the interactive TUI, which
	// always drills via the tree.
	Tree bool
	// HideStale starts the surface with stopped/archived (stale) squads hidden.
	HideStale bool
	// NoBell starts the surface with needs-you alerts MUTED (no terminal bell, no
	// banner) — the --no-bell flag. Default false: alerts are ON. The interactive
	// 'A' key toggles the same mute at runtime.
	NoBell bool
	// Notify opts into a DESKTOP notification on the same needs-you 0->N
	// transition the bell fires on (#28) - the --notify flag. Independent of
	// NoBell: a muted bell does not mute the notifier. Default false.
	Notify bool
	// Lifecycle is the cli-injected stop/resume/restart seam. cli owns the
	// lifecycle verbs; passing a closure here lets the live NOC
	// drive them WITHOUT a console import cycle. nil means lifecycle actions
	// degrade to a "no lifecycle backend" note rather than a panic. It is reached
	// ONLY after the operator confirms the preview overlay, and only on the live
	// interactive path because --once is non-interactive.
	Lifecycle func(LifecycleRequest) error
	// AgentResume is the cli-injected single-agent resume seam for a confirmed
	// palette action.
	AgentResume func(AgentResumeRequest) error
	// NewSession is the cli-injected launch seam for a confirmed NOC new-session
	// action. It starts a detached tmux workstream for a selected team-home.
	NewSession func(NewSessionRequest) error
	// NewTeam is the cli-injected team-creation seam for a confirmed NOC
	// new-team action.
	NewTeam func(NewTeamRequest) error
	// TeamDelete is the cli-injected team-profile removal seam for a confirmed
	// NOC delete-team action.
	TeamDelete func(TeamDeleteRequest) error
	// PointerSync is the cli-injected pointer-stub repair seam for a confirmed
	// NOC sync-pointers action.
	PointerSync func(PointerSyncRequest) error
	// ReadNeedsYou is the cli-injected AMQ read seam for a confirmed needs-you
	// read action. It moves the operator's unread message to cur and returns the
	// message body for an in-NOC result overlay.
	ReadNeedsYou func(ReadNeedsYouRequest) (ReadNeedsYouResult, error)
	// DrainAgent is the cli-injected AMQ drain seam for a confirmed agent inbox
	// drain action. It moves unread mail to cur and returns the drain output for
	// an in-NOC result overlay.
	DrainAgent func(DrainAgentRequest) (DrainAgentResult, error)
	// InboxAgent is the cli-injected AMQ list seam for a read-only agent inbox
	// inspection. It lists unread mail and returns stdout for an in-NOC overlay.
	InboxAgent func(InboxAgentRequest) (InboxAgentResult, error)
	// DLQAgent is the cli-injected AMQ DLQ list seam for a read-only agent
	// failure-queue inspection. It returns stdout for an in-NOC overlay.
	DLQAgent func(DLQAgentRequest) (DLQAgentResult, error)
	// DLQRead is the cli-injected AMQ DLQ read seam for a confirmed agent DLQ
	// item inspection. AMQ moves the item to cur, so it is confirm-gated.
	DLQRead func(DLQReadRequest) (DLQReadResult, error)
	// DLQRetry is the cli-injected AMQ DLQ retry seam for a confirmed agent DLQ
	// item remediation action.
	DLQRetry func(DLQRetryRequest) (DLQRetryResult, error)
	// DLQPurge is the cli-injected AMQ DLQ purge seam for a confirmed old-DLQ
	// cleanup action.
	DLQPurge func(DLQPurgeRequest) (DLQPurgeResult, error)
	// DLQRetryAll is the cli-injected AMQ DLQ retry-all seam for a confirmed
	// agent DLQ remediation action.
	DLQRetryAll func(DLQRetryAllRequest) (DLQRetryAllResult, error)
	// ReceiptsAgent is the cli-injected AMQ receipts list seam for a read-only
	// agent delivery-receipt inspection. It returns stdout for an in-NOC overlay.
	ReceiptsAgent func(ReceiptsAgentRequest) (ReceiptsAgentResult, error)
	// ReceiptsWait is the cli-injected AMQ receipts wait seam for a confirmed
	// message delivery follow-up.
	ReceiptsWait func(ReceiptsWaitRequest) (ReceiptsWaitResult, error)
	// MessageWait is the cli-injected AMQ send --wait-for seam for a confirmed
	// direct operator message with delivery follow-up.
	MessageWait func(MessageWaitRequest) (MessageWaitResult, error)
	// AMQCleanup is the cli-injected AMQ cleanup seam for confirmed session
	// tmp-file maintenance.
	AMQCleanup func(AMQCleanupRequest) (AMQCleanupResult, error)
	// ThreadContext is the cli-injected AMQ thread seam for a read-only needs-you
	// thread transcript. It lets the operator inspect context before replying.
	ThreadContext func(ThreadContextRequest) (ThreadContextResult, error)
	// AMQOps is the cli-injected AMQ doctor --ops seam for a read-only session
	// bus health inspection. It returns stdout for an in-NOC overlay.
	AMQOps func(AMQOpsRequest) (AMQOpsResult, error)
	// AMQWho is the cli-injected AMQ who seam for a read-only project base-root
	// inventory. It returns stdout for an in-NOC overlay.
	AMQWho func(AMQWhoRequest) (AMQWhoResult, error)
	// AMQEnv is the cli-injected AMQ env seam for a read-only project base-root
	// environment snapshot. It returns stdout for an in-NOC overlay.
	AMQEnv func(AMQEnvRequest) (AMQEnvResult, error)
	// Presence is the cli-injected AMQ presence list seam for a read-only
	// session presence inspection. It returns stdout for an in-NOC overlay.
	Presence func(PresenceRequest) (PresenceResult, error)
	// ProjectDoctor is the cli-injected amq-squad doctor seam for a read-only
	// project health inspection. It returns the human report for an in-NOC
	// overlay, including failing checks as data rather than treating them as a
	// failed overlay open.
	ProjectDoctor func(ProjectDoctorRequest) (ProjectDoctorResult, error)
	// TeamRules is the cli-injected team-rules.md seam for a read-only
	// team-home norms inspection.
	TeamRules func(TeamRulesRequest) (TeamRulesResult, error)
	// ProjectResumePlan is the cli-injected amq-squad resume seam for a
	// read-only recovery-plan inspection.
	ProjectResumePlan func(ProjectResumePlanRequest) (ProjectResumePlanResult, error)
	// ForkPlan is the cli-injected local (amq-squad's `fork` verb was removed
	// in 2.28) fork-plan seam for a read-only plan that branches one existing
	// workstream into a new target workstream.
	ForkPlan func(ForkPlanRequest) (ForkPlanResult, error)
	// Brief is the cli-injected local (amq-squad's `brief` verb was removed in
	// 2.28) brief seam for a read-only workstream brief inspection.
	Brief func(BriefRequest) (BriefResult, error)
	// BriefSeed is the cli-injected local (amq-squad's `brief` verb was
	// removed in 2.28) brief-seed seam for a confirmed workstream brief write.
	BriefSeed func(BriefSeedRequest) error
	// SendPrompt is the cli-injected amq-squad send seam for a confirmed
	// send-prompt action. It delivers a typed prompt to one agent's tmux pane
	// (body via STDIN). nil degrades to a "no send prompt backend" note. It is
	// reached ONLY after the operator confirms the preview overlay.
	SendPrompt func(SendPromptRequest) error
	// Status is the cli-injected amq-squad status seam for a read-only project or
	// session status inspection.
	Status func(StatusRequest) (StatusResult, error)
	// Threads is the cli-injected amq-squad threads seam for a read-only session
	// thread summary inspection.
	Threads func(ThreadsRequest) (ThreadsResult, error)
}

// LifecycleRequest is the public, cli-facing shape of a confirmed lifecycle action.
// It carries the exact scope the NOC previewed (verb + project dir + session +
// affected agents) so the cli closure can call the right verb on the right
// squad. Verb is "stop", "resume", or "restart". Keeping this type free of console
// internals is what lets cli inject the seam without importing unexported types.
type LifecycleRequest struct {
	Verb       string
	ProjectDir string
	Profile    string
	Session    string
	Agents     []string
}

// AgentResumeRequest is the public, cli-facing shape of a confirmed
// single-agent resume action.
type AgentResumeRequest struct {
	ProjectDir string
	Role       string
	Session    string
}

// NewSessionRequest is the public, cli-facing shape of a confirmed NOC
// new-session action.
type NewSessionRequest struct {
	ProjectDir string
	Profile    string
	Session    string
	SeedFrom   string
}

// NewTeamRequest is the public, cli-facing shape of a confirmed NOC new-team
// action.
type NewTeamRequest struct {
	ProjectDir string
	Profile    string
	Roles      string
	Binary     string
	Session    string
}

// TeamDeleteRequest is the public, cli-facing shape of a confirmed NOC
// delete-team action.
type TeamDeleteRequest struct {
	ProjectDir  string
	Profile     string
	AllProfiles bool
}

// PointerSyncRequest is the public, cli-facing shape of a confirmed NOC
// pointer-sync action.
type PointerSyncRequest struct {
	ProjectDir   string
	Profile      string
	AllowOutside bool
}

// ReadNeedsYouRequest is the public, cli-facing shape of a confirmed NOC
// needs-you read action.
type ReadNeedsYouRequest struct {
	Root           string
	OperatorHandle string
	MessageID      string
	Thread         string
	Subject        string
}

// ReadNeedsYouResult is the public body returned by the cli read seam.
type ReadNeedsYouResult struct {
	MessageID string
	Thread    string
	Subject   string
	Body      string
}

// DrainAgentRequest is the public, cli-facing shape of a confirmed NOC agent
// drain action.
type DrainAgentRequest struct {
	Root   string
	Handle string
}

// DrainAgentResult is the public output returned by the cli drain seam.
type DrainAgentResult struct {
	Handle string
	Output string
}

// InboxAgentRequest is the public, cli-facing shape of a read-only NOC agent
// inbox listing.
type InboxAgentRequest struct {
	Root   string
	Handle string
}

// InboxAgentResult is the public output returned by the cli inbox seam.
type InboxAgentResult struct {
	Handle string
	Output string
}

// DLQAgentRequest is the public, cli-facing shape of a read-only NOC agent DLQ
// listing.
type DLQAgentRequest struct {
	Root   string
	Handle string
}

// DLQAgentResult is the public output returned by the cli DLQ seam.
type DLQAgentResult struct {
	Handle string
	Output string
}

// DLQReadRequest is the public, cli-facing shape of a confirmed NOC agent DLQ
// item read.
type DLQReadRequest struct {
	Root   string
	Handle string
	ID     string
}

// DLQReadResult is the public output returned by the cli DLQ read seam.
type DLQReadResult struct {
	Handle string
	ID     string
	Output string
}

// DLQRetryRequest is the public, cli-facing shape of a confirmed NOC agent DLQ
// item retry.
type DLQRetryRequest struct {
	Root   string
	Handle string
	ID     string
}

// DLQRetryResult is the public output returned by the cli DLQ retry seam.
type DLQRetryResult struct {
	Handle string
	ID     string
	Output string
}

// DLQPurgeRequest is the public, cli-facing shape of a confirmed NOC agent DLQ
// purge.
type DLQPurgeRequest struct {
	Root      string
	Handle    string
	OlderThan string
}

// DLQPurgeResult is the public output returned by the cli DLQ purge seam.
type DLQPurgeResult struct {
	Handle    string
	OlderThan string
	Output    string
}

// DLQRetryAllRequest is the public, cli-facing shape of a confirmed NOC agent
// DLQ retry-all action.
type DLQRetryAllRequest struct {
	Root   string
	Handle string
}

// DLQRetryAllResult is the public output returned by the cli DLQ retry-all
// seam.
type DLQRetryAllResult struct {
	Handle string
	Output string
}

// ReceiptsAgentRequest is the public, cli-facing shape of a read-only NOC
// agent receipts listing.
type ReceiptsAgentRequest struct {
	Root   string
	Handle string
}

// ReceiptsAgentResult is the public output returned by the cli receipts seam.
type ReceiptsAgentResult struct {
	Handle string
	Output string
}

// ReceiptsWaitRequest is the public, cli-facing shape of a confirmed NOC
// receipts wait.
type ReceiptsWaitRequest struct {
	Root    string
	Handle  string
	MsgID   string
	Stage   string
	Timeout string
}

// ReceiptsWaitResult is the public output returned by the cli receipts wait
// seam.
type ReceiptsWaitResult struct {
	Handle  string
	MsgID   string
	Stage   string
	Timeout string
	Output  string
}

// MessageWaitRequest is the public, cli-facing shape of a confirmed NOC direct
// message with send --wait-for drained.
type MessageWaitRequest struct {
	Root           string
	OperatorHandle string
	Handle         string
	Body           string
	Timeout        string
}

// MessageWaitResult is the public output returned by the cli message wait seam.
type MessageWaitResult struct {
	Handle  string
	Timeout string
	Output  string
}

// AMQCleanupRequest is the public, cli-facing shape of a confirmed NOC AMQ
// cleanup action.
type AMQCleanupRequest struct {
	Root         string
	TmpOlderThan string
}

// AMQCleanupResult is the public output returned by the cli AMQ cleanup seam.
type AMQCleanupResult struct {
	Root         string
	TmpOlderThan string
	Output       string
}

// ThreadContextRequest is the public, cli-facing shape of a read-only NOC
// thread transcript request.
type ThreadContextRequest struct {
	Root    string
	Thread  string
	Subject string
}

// ThreadContextResult is the public output returned by the cli thread seam.
type ThreadContextResult struct {
	Thread  string
	Subject string
	Output  string
}

// AMQOpsRequest is the public, cli-facing shape of a read-only NOC AMQ ops
// inspection.
type AMQOpsRequest struct {
	Root string
}

// AMQOpsResult is the public output returned by the cli AMQ ops seam.
type AMQOpsResult struct {
	Root   string
	Output string
}

// AMQWhoRequest is the public, cli-facing shape of a read-only NOC AMQ who
// inspection.
type AMQWhoRequest struct {
	Root string
}

// AMQWhoResult is the public output returned by the cli AMQ who seam.
type AMQWhoResult struct {
	Root   string
	Output string
}

// AMQEnvRequest is the public, cli-facing shape of a read-only NOC AMQ env
// inspection.
type AMQEnvRequest struct {
	Root string
}

// AMQEnvResult is the public output returned by the cli AMQ env seam.
type AMQEnvResult struct {
	Root   string
	Output string
}

// PresenceRequest is the public, cli-facing shape of a read-only NOC AMQ
// presence inspection.
type PresenceRequest struct {
	Root string
}

// PresenceResult is the public output returned by the cli presence seam.
type PresenceResult struct {
	Root   string
	Output string
}

// ProjectDoctorRequest is the public, cli-facing shape of a read-only NOC
// project doctor inspection.
type ProjectDoctorRequest struct {
	ProjectDir string
}

// ProjectDoctorResult is the public output returned by the cli project-doctor
// seam.
type ProjectDoctorResult struct {
	ProjectDir string
	Output     string
}

// TeamRulesRequest is the public, cli-facing shape of a read-only NOC
// team-rules.md inspection.
type TeamRulesRequest struct {
	ProjectDir string
}

// TeamRulesResult is the public output returned by the cli team-rules seam.
type TeamRulesResult struct {
	ProjectDir string
	Path       string
	Content    string
}

// ProjectResumePlanRequest is the public, cli-facing shape of a read-only NOC
// project recovery-plan inspection.
type ProjectResumePlanRequest struct {
	ProjectDir string
	Profile    string
}

// ProjectResumePlanResult is the public output returned by the cli
// project-resume-plan seam.
type ProjectResumePlanResult struct {
	ProjectDir string
	Profile    string
	Output     string
}

// ForkPlanRequest is the public, cli-facing shape of a read-only NOC fork plan.
type ForkPlanRequest struct {
	ProjectDir  string
	Profile     string
	FromSession string
	ToSession   string
}

// ForkPlanResult is the public output returned by the cli fork-plan seam.
type ForkPlanResult struct {
	ProjectDir  string
	Profile     string
	FromSession string
	ToSession   string
	Output      string
}

// BriefRequest is the public, cli-facing shape of a read-only NOC workstream
// brief inspection. Profile resolves the profile-nested brief path for named
// profiles (empty/"default" keeps the un-nested path).
type BriefRequest struct {
	ProjectDir string
	Profile    string
	Session    string
}

// BriefResult is the public output returned by the cli brief seam.
type BriefResult struct {
	ProjectDir string
	Session    string
	Path       string
	Kind       string
	Exists     bool
	Content    string
}

// BriefSeedRequest is the public, cli-facing shape of a confirmed NOC
// workstream brief seed action. Profile resolves the profile-nested brief
// path for named profiles (empty/"default" keeps the un-nested path).
type BriefSeedRequest struct {
	ProjectDir string
	Profile    string
	Session    string
	SeedFrom   string
	Force      bool
}

// SendPromptRequest is the public, cli-facing shape of a confirmed send-prompt
// action. It carries the exact scope the NOC previewed (project dir + profile +
// session + role) plus the prompt Body the cli closure pipes to `amq-squad send`
// over STDIN. Keeping this free of console internals lets cli inject the seam
// without importing unexported types.
type SendPromptRequest struct {
	ProjectDir string
	Profile    string
	Session    string
	Role       string
	Body       string
}

// StatusRequest is the public, cli-facing shape of a read-only NOC status
// inspection.
type StatusRequest struct {
	ProjectDir string
	Session    string
	Profile    string
}

// StatusResult is the public output returned by the cli status seam.
type StatusResult struct {
	ProjectDir string
	Session    string
	Profile    string
	Output     string
}

// ThreadsRequest is the public, cli-facing shape of a read-only NOC session
// thread summary inspection.
type ThreadsRequest struct {
	ProjectDir string
	Session    string
}

// ThreadsResult is the public output returned by the cli threads seam.
type ThreadsResult struct {
	ProjectDir string
	Session    string
	Output     string
}

// adaptLifecycle bridges the public, cli-facing LifecycleRequest seam to the
// model's internal lifecycleOp seam. nil in means nil out, so lifecycle actions
// degrade to a note.
func adaptLifecycle(fn func(LifecycleRequest) error) func(lifecycleOp) error {
	if fn == nil {
		return nil
	}
	return func(op lifecycleOp) error {
		return fn(LifecycleRequest{
			Verb:       string(op.Verb),
			ProjectDir: op.ProjectDir,
			Profile:    op.Profile,
			Session:    op.Session,
			Agents:     op.Agents,
		})
	}
}

func adaptNewSession(fn func(NewSessionRequest) error) func(newSessionOp) error {
	if fn == nil {
		return nil
	}
	return func(op newSessionOp) error {
		return fn(NewSessionRequest{
			ProjectDir: op.ProjectDir,
			Profile:    op.Profile,
			Session:    op.Session,
			SeedFrom:   op.SeedFrom,
		})
	}
}

func adaptAgentResume(fn func(AgentResumeRequest) error) func(agentResumeOp) error {
	if fn == nil {
		return nil
	}
	return func(op agentResumeOp) error {
		return fn(AgentResumeRequest{
			ProjectDir: op.ProjectDir,
			Role:       op.Role,
			Session:    op.Session,
		})
	}
}

func adaptNewTeam(fn func(NewTeamRequest) error) func(newTeamOp) error {
	if fn == nil {
		return nil
	}
	return func(op newTeamOp) error {
		return fn(NewTeamRequest{
			ProjectDir: op.ProjectDir,
			Profile:    op.Profile,
			Roles:      op.Roles,
			Binary:     op.Binary,
			Session:    op.Session,
		})
	}
}

func adaptTeamDelete(fn func(TeamDeleteRequest) error) func(teamDeleteOp) error {
	if fn == nil {
		return nil
	}
	return func(op teamDeleteOp) error {
		return fn(TeamDeleteRequest{
			ProjectDir:  op.ProjectDir,
			Profile:     op.Profile,
			AllProfiles: op.AllProfiles,
		})
	}
}

func adaptPointerSync(fn func(PointerSyncRequest) error) func(pointerSyncOp) error {
	if fn == nil {
		return nil
	}
	return func(op pointerSyncOp) error {
		return fn(PointerSyncRequest{
			ProjectDir:   op.ProjectDir,
			Profile:      op.Profile,
			AllowOutside: op.AllowOutside,
		})
	}
}

func adaptReadNeedsYou(fn func(ReadNeedsYouRequest) (ReadNeedsYouResult, error)) func(readNeedsYouOp) (readNeedsYouResult, error) {
	if fn == nil {
		return nil
	}
	return func(op readNeedsYouOp) (readNeedsYouResult, error) {
		res, err := fn(ReadNeedsYouRequest{
			Root:           op.Root,
			OperatorHandle: op.OperatorHandle,
			MessageID:      op.MessageID,
			Thread:         op.Thread,
			Subject:        op.Subject,
		})
		if err != nil {
			return readNeedsYouResult{}, err
		}
		return readNeedsYouResult{
			MessageID: res.MessageID,
			Thread:    res.Thread,
			Subject:   res.Subject,
			Body:      res.Body,
		}, nil
	}
}

func adaptDrainAgent(fn func(DrainAgentRequest) (DrainAgentResult, error)) func(drainAgentOp) (drainAgentResult, error) {
	if fn == nil {
		return nil
	}
	return func(op drainAgentOp) (drainAgentResult, error) {
		res, err := fn(DrainAgentRequest{
			Root:   op.Root,
			Handle: op.Handle,
		})
		if err != nil {
			return drainAgentResult{}, err
		}
		return drainAgentResult{
			Handle: res.Handle,
			Output: res.Output,
		}, nil
	}
}

func adaptInboxAgent(fn func(InboxAgentRequest) (InboxAgentResult, error)) func(inboxAgentOp) (inboxAgentResult, error) {
	if fn == nil {
		return nil
	}
	return func(op inboxAgentOp) (inboxAgentResult, error) {
		res, err := fn(InboxAgentRequest{
			Root:   op.Root,
			Handle: op.Handle,
		})
		if err != nil {
			return inboxAgentResult{}, err
		}
		return inboxAgentResult{
			Handle: res.Handle,
			Output: res.Output,
		}, nil
	}
}

func adaptDLQAgent(fn func(DLQAgentRequest) (DLQAgentResult, error)) func(dlqAgentOp) (dlqAgentResult, error) {
	if fn == nil {
		return nil
	}
	return func(op dlqAgentOp) (dlqAgentResult, error) {
		res, err := fn(DLQAgentRequest{
			Root:   op.Root,
			Handle: op.Handle,
		})
		if err != nil {
			return dlqAgentResult{}, err
		}
		return dlqAgentResult{
			Handle: res.Handle,
			Output: res.Output,
		}, nil
	}
}

func adaptDLQRead(fn func(DLQReadRequest) (DLQReadResult, error)) func(dlqReadOp) (dlqReadResult, error) {
	if fn == nil {
		return nil
	}
	return func(op dlqReadOp) (dlqReadResult, error) {
		res, err := fn(DLQReadRequest{
			Root:   op.Root,
			Handle: op.Handle,
			ID:     op.ID,
		})
		if err != nil {
			return dlqReadResult{}, err
		}
		return dlqReadResult{
			Handle: res.Handle,
			ID:     res.ID,
			Output: res.Output,
		}, nil
	}
}

func adaptDLQRetry(fn func(DLQRetryRequest) (DLQRetryResult, error)) func(dlqRetryOp) (dlqRetryResult, error) {
	if fn == nil {
		return nil
	}
	return func(op dlqRetryOp) (dlqRetryResult, error) {
		res, err := fn(DLQRetryRequest{
			Root:   op.Root,
			Handle: op.Handle,
			ID:     op.ID,
		})
		if err != nil {
			return dlqRetryResult{}, err
		}
		return dlqRetryResult{
			Handle: res.Handle,
			ID:     res.ID,
			Output: res.Output,
		}, nil
	}
}

func adaptDLQPurge(fn func(DLQPurgeRequest) (DLQPurgeResult, error)) func(dlqPurgeOp) (dlqPurgeResult, error) {
	if fn == nil {
		return nil
	}
	return func(op dlqPurgeOp) (dlqPurgeResult, error) {
		res, err := fn(DLQPurgeRequest{
			Root:      op.Root,
			Handle:    op.Handle,
			OlderThan: op.OlderThan,
		})
		if err != nil {
			return dlqPurgeResult{}, err
		}
		return dlqPurgeResult{
			Handle:    res.Handle,
			OlderThan: res.OlderThan,
			Output:    res.Output,
		}, nil
	}
}

func adaptDLQRetryAll(fn func(DLQRetryAllRequest) (DLQRetryAllResult, error)) func(dlqRetryAllOp) (dlqRetryAllResult, error) {
	if fn == nil {
		return nil
	}
	return func(op dlqRetryAllOp) (dlqRetryAllResult, error) {
		res, err := fn(DLQRetryAllRequest{
			Root:   op.Root,
			Handle: op.Handle,
		})
		if err != nil {
			return dlqRetryAllResult{}, err
		}
		return dlqRetryAllResult{
			Handle: res.Handle,
			Output: res.Output,
		}, nil
	}
}

func adaptReceiptsAgent(fn func(ReceiptsAgentRequest) (ReceiptsAgentResult, error)) func(receiptsAgentOp) (receiptsAgentResult, error) {
	if fn == nil {
		return nil
	}
	return func(op receiptsAgentOp) (receiptsAgentResult, error) {
		res, err := fn(ReceiptsAgentRequest{
			Root:   op.Root,
			Handle: op.Handle,
		})
		if err != nil {
			return receiptsAgentResult{}, err
		}
		return receiptsAgentResult{
			Handle: res.Handle,
			Output: res.Output,
		}, nil
	}
}

func adaptReceiptsWait(fn func(ReceiptsWaitRequest) (ReceiptsWaitResult, error)) func(receiptsWaitOp) (receiptsWaitResult, error) {
	if fn == nil {
		return nil
	}
	return func(op receiptsWaitOp) (receiptsWaitResult, error) {
		res, err := fn(ReceiptsWaitRequest{
			Root:    op.Root,
			Handle:  op.Handle,
			MsgID:   op.MsgID,
			Stage:   op.Stage,
			Timeout: op.Timeout,
		})
		if err != nil {
			return receiptsWaitResult{}, err
		}
		return receiptsWaitResult{
			Handle:  res.Handle,
			MsgID:   res.MsgID,
			Stage:   res.Stage,
			Timeout: res.Timeout,
			Output:  res.Output,
		}, nil
	}
}

func adaptMessageWait(fn func(MessageWaitRequest) (MessageWaitResult, error)) func(messageWaitOp) (messageWaitResult, error) {
	if fn == nil {
		return nil
	}
	return func(op messageWaitOp) (messageWaitResult, error) {
		res, err := fn(MessageWaitRequest{
			Root:           op.Root,
			OperatorHandle: op.OperatorHandle,
			Handle:         op.Handle,
			Body:           op.Body,
			Timeout:        op.Timeout,
		})
		if err != nil {
			return messageWaitResult{}, err
		}
		return messageWaitResult{
			Handle:  res.Handle,
			Timeout: res.Timeout,
			Output:  res.Output,
		}, nil
	}
}

func adaptAMQCleanup(fn func(AMQCleanupRequest) (AMQCleanupResult, error)) func(amqCleanupOp) (amqCleanupResult, error) {
	if fn == nil {
		return nil
	}
	return func(op amqCleanupOp) (amqCleanupResult, error) {
		res, err := fn(AMQCleanupRequest{Root: op.Root, TmpOlderThan: op.TmpOlderThan})
		if err != nil {
			return amqCleanupResult{}, err
		}
		return amqCleanupResult{Root: res.Root, TmpOlderThan: res.TmpOlderThan, Output: res.Output}, nil
	}
}

func adaptThreadContext(fn func(ThreadContextRequest) (ThreadContextResult, error)) func(threadContextOp) (threadContextResult, error) {
	if fn == nil {
		return nil
	}
	return func(op threadContextOp) (threadContextResult, error) {
		res, err := fn(ThreadContextRequest{
			Root:    op.Root,
			Thread:  op.Thread,
			Subject: op.Subject,
		})
		if err != nil {
			return threadContextResult{}, err
		}
		return threadContextResult{
			Thread:  res.Thread,
			Subject: res.Subject,
			Output:  res.Output,
		}, nil
	}
}

func adaptAMQOps(fn func(AMQOpsRequest) (AMQOpsResult, error)) func(amqOpsOp) (amqOpsResult, error) {
	if fn == nil {
		return nil
	}
	return func(op amqOpsOp) (amqOpsResult, error) {
		res, err := fn(AMQOpsRequest{Root: op.Root})
		if err != nil {
			return amqOpsResult{}, err
		}
		return amqOpsResult{Root: res.Root, Output: res.Output}, nil
	}
}

func adaptAMQWho(fn func(AMQWhoRequest) (AMQWhoResult, error)) func(amqWhoOp) (amqWhoResult, error) {
	if fn == nil {
		return nil
	}
	return func(op amqWhoOp) (amqWhoResult, error) {
		res, err := fn(AMQWhoRequest{Root: op.Root})
		if err != nil {
			return amqWhoResult{}, err
		}
		return amqWhoResult{Root: res.Root, Output: res.Output}, nil
	}
}

func adaptAMQEnv(fn func(AMQEnvRequest) (AMQEnvResult, error)) func(amqEnvOp) (amqEnvResult, error) {
	if fn == nil {
		return nil
	}
	return func(op amqEnvOp) (amqEnvResult, error) {
		res, err := fn(AMQEnvRequest{Root: op.Root})
		if err != nil {
			return amqEnvResult{}, err
		}
		return amqEnvResult{Root: res.Root, Output: res.Output}, nil
	}
}

func adaptPresence(fn func(PresenceRequest) (PresenceResult, error)) func(presenceOp) (presenceResult, error) {
	if fn == nil {
		return nil
	}
	return func(op presenceOp) (presenceResult, error) {
		res, err := fn(PresenceRequest{Root: op.Root})
		if err != nil {
			return presenceResult{}, err
		}
		return presenceResult{Root: res.Root, Output: res.Output}, nil
	}
}

func adaptProjectDoctor(fn func(ProjectDoctorRequest) (ProjectDoctorResult, error)) func(projectDoctorOp) (projectDoctorResult, error) {
	if fn == nil {
		return nil
	}
	return func(op projectDoctorOp) (projectDoctorResult, error) {
		res, err := fn(ProjectDoctorRequest{ProjectDir: op.ProjectDir})
		if err != nil {
			return projectDoctorResult{}, err
		}
		return projectDoctorResult{ProjectDir: res.ProjectDir, Output: res.Output}, nil
	}
}

func adaptTeamRules(fn func(TeamRulesRequest) (TeamRulesResult, error)) func(teamRulesOp) (teamRulesResult, error) {
	if fn == nil {
		return nil
	}
	return func(op teamRulesOp) (teamRulesResult, error) {
		res, err := fn(TeamRulesRequest{ProjectDir: op.ProjectDir})
		if err != nil {
			return teamRulesResult{}, err
		}
		return teamRulesResult{ProjectDir: res.ProjectDir, Path: res.Path, Content: res.Content}, nil
	}
}

func adaptProjectResumePlan(fn func(ProjectResumePlanRequest) (ProjectResumePlanResult, error)) func(projectResumePlanOp) (projectResumePlanResult, error) {
	if fn == nil {
		return nil
	}
	return func(op projectResumePlanOp) (projectResumePlanResult, error) {
		res, err := fn(ProjectResumePlanRequest{ProjectDir: op.ProjectDir, Profile: op.Profile})
		if err != nil {
			return projectResumePlanResult{}, err
		}
		return projectResumePlanResult{ProjectDir: res.ProjectDir, Profile: res.Profile, Output: res.Output}, nil
	}
}

func adaptForkPlan(fn func(ForkPlanRequest) (ForkPlanResult, error)) func(forkPlanOp) (forkPlanResult, error) {
	if fn == nil {
		return nil
	}
	return func(op forkPlanOp) (forkPlanResult, error) {
		res, err := fn(ForkPlanRequest{
			ProjectDir:  op.ProjectDir,
			Profile:     op.Profile,
			FromSession: op.FromSession,
			ToSession:   op.ToSession,
		})
		if err != nil {
			return forkPlanResult{}, err
		}
		return forkPlanResult{
			ProjectDir:  res.ProjectDir,
			Profile:     res.Profile,
			FromSession: res.FromSession,
			ToSession:   res.ToSession,
			Output:      res.Output,
		}, nil
	}
}

func adaptBrief(fn func(BriefRequest) (BriefResult, error)) func(briefOp) (briefResult, error) {
	if fn == nil {
		return nil
	}
	return func(op briefOp) (briefResult, error) {
		res, err := fn(BriefRequest{ProjectDir: op.ProjectDir, Profile: op.Profile, Session: op.Session})
		if err != nil {
			return briefResult{}, err
		}
		return briefResult{
			ProjectDir: res.ProjectDir,
			Session:    res.Session,
			Path:       res.Path,
			Kind:       res.Kind,
			Exists:     res.Exists,
			Content:    res.Content,
		}, nil
	}
}

func adaptBriefSeed(fn func(BriefSeedRequest) error) func(briefSeedOp) error {
	if fn == nil {
		return nil
	}
	return func(op briefSeedOp) error {
		return fn(BriefSeedRequest{
			ProjectDir: op.ProjectDir,
			Profile:    op.Profile,
			Session:    op.Session,
			SeedFrom:   op.SeedFrom,
			Force:      op.Force,
		})
	}
}

// adaptSendPrompt bridges the public, cli-facing SendPromptRequest seam to the
// model's internal sendPromptOp seam. nil in means nil out, so send-prompt
// degrades to a note rather than a panic.
func adaptSendPrompt(fn func(SendPromptRequest) error) func(sendPromptOp) error {
	if fn == nil {
		return nil
	}
	return func(op sendPromptOp) error {
		return fn(SendPromptRequest{
			ProjectDir: op.ProjectDir,
			Profile:    op.Profile,
			Session:    op.Session,
			Role:       op.Role,
			Body:       op.Body,
		})
	}
}

func adaptStatus(fn func(StatusRequest) (StatusResult, error)) func(statusOp) (statusResult, error) {
	if fn == nil {
		return nil
	}
	return func(op statusOp) (statusResult, error) {
		res, err := fn(StatusRequest{ProjectDir: op.ProjectDir, Session: op.Session, Profile: op.Profile})
		if err != nil {
			return statusResult{}, err
		}
		return statusResult{ProjectDir: res.ProjectDir, Session: res.Session, Profile: res.Profile, Output: res.Output}, nil
	}
}

func adaptThreads(fn func(ThreadsRequest) (ThreadsResult, error)) func(threadsOp) (threadsResult, error) {
	if fn == nil {
		return nil
	}
	return func(op threadsOp) (threadsResult, error) {
		res, err := fn(ThreadsRequest{ProjectDir: op.ProjectDir, Session: op.Session})
		if err != nil {
			return threadsResult{}, err
		}
		return threadsResult{ProjectDir: res.ProjectDir, Session: res.Session, Output: res.Output}, nil
	}
}

// RunNOC is the NOC entrypoint. With cfg.Once it renders a single static board
// to cfg.Out; otherwise it starts the live program on /dev/tty.
func RunNOC(cfg NOCConfig) error {
	if cfg.Once {
		return runNOCOnce(cfg)
	}
	return runNOCLive(cfg)
}

// runNOCOnce renders a single static multi-root board for non-TTY / CI use. The
// color mode is resolved against the Out writer's TTY-ness so output is plain
// (no escape codes) when piped or under NO_COLOR.
func runNOCOnce(cfg NOCConfig) error {
	rebuild := nocRebuildFromConfig(cfg)
	ms := noc.Collect(rebuild.Roots, rebuild.Depth, rebuild.Probe, rebuild.Thresholds)

	m := newNOCModel(rebuild)
	m.version = cfg.Version
	// --once renders to a (possibly non-TTY) writer: resolve color from the
	// writer, not from an assumed interactive terminal.
	mode := resolveColorMode(writerIsTTY(cfg.Out))
	m.colorMode = mode
	m.th = newNOCTheme(mode)
	if cfg.InitialFilter != "" {
		m.filter = cfg.InitialFilter
	}
	m.fullTree = cfg.Tree
	m.hideStale = cfg.HideStale
	m.ms = ms
	m.ready = true
	m.refreshGuidance()
	if cfg.Out != nil {
		fmt.Fprintln(cfg.Out, m.staticView())
	}
	return nil
}

// runNOCLive starts the Bubble Tea program against /dev/tty. Falls back to a
// single static board on stdout if no tty is available.
func runNOCLive(cfg NOCConfig) error {
	rebuild := nocRebuildFromConfig(cfg)
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return runNOCOnce(cfg)
	}
	defer tty.Close()

	m := newNOCModel(rebuild)
	m.version = cfg.Version
	if cfg.InitialFilter != "" {
		m.filter = cfg.InitialFilter
	}
	m.hideStale = cfg.HideStale
	// Wire the confirmed lifecycle seam. The AMQ-write seam (sendOp)
	// already defaults to act.Send in newNOCModel; here we bridge the cli-owned
	// lifecycle verbs onto the model's internal seam. These are reached only
	// after the operator confirms the preview overlay.
	m.lifecycle = adaptLifecycle(cfg.Lifecycle)
	m.agentResume = adaptAgentResume(cfg.AgentResume)
	m.newSession = adaptNewSession(cfg.NewSession)
	m.newTeam = adaptNewTeam(cfg.NewTeam)
	m.teamDelete = adaptTeamDelete(cfg.TeamDelete)
	m.pointerSync = adaptPointerSync(cfg.PointerSync)
	m.readNeedsYou = adaptReadNeedsYou(cfg.ReadNeedsYou)
	m.drainAgent = adaptDrainAgent(cfg.DrainAgent)
	m.inboxAgent = adaptInboxAgent(cfg.InboxAgent)
	m.dlqAgent = adaptDLQAgent(cfg.DLQAgent)
	m.dlqRead = adaptDLQRead(cfg.DLQRead)
	m.dlqRetry = adaptDLQRetry(cfg.DLQRetry)
	m.dlqPurge = adaptDLQPurge(cfg.DLQPurge)
	m.dlqRetryAll = adaptDLQRetryAll(cfg.DLQRetryAll)
	m.receiptsAgent = adaptReceiptsAgent(cfg.ReceiptsAgent)
	m.receiptsWait = adaptReceiptsWait(cfg.ReceiptsWait)
	m.messageWait = adaptMessageWait(cfg.MessageWait)
	m.amqCleanup = adaptAMQCleanup(cfg.AMQCleanup)
	m.threadContext = adaptThreadContext(cfg.ThreadContext)
	m.amqOps = adaptAMQOps(cfg.AMQOps)
	m.amqWho = adaptAMQWho(cfg.AMQWho)
	m.amqEnv = adaptAMQEnv(cfg.AMQEnv)
	m.presence = adaptPresence(cfg.Presence)
	m.projectDoctor = adaptProjectDoctor(cfg.ProjectDoctor)
	m.teamRules = adaptTeamRules(cfg.TeamRules)
	m.projectResumePlan = adaptProjectResumePlan(cfg.ProjectResumePlan)
	m.forkPlan = adaptForkPlan(cfg.ForkPlan)
	m.brief = adaptBrief(cfg.Brief)
	m.briefSeed = adaptBriefSeed(cfg.BriefSeed)
	m.sendPrompt = adaptSendPrompt(cfg.SendPrompt)
	m.status = adaptStatus(cfg.Status)
	m.threads = adaptThreads(cfg.Threads)
	// Wire the needs-you ALERT bell (PR18). It writes a bare BEL ("\a") to the
	// SAME tty the program renders on, so the operator hears it without corrupting
	// the AltScreen frame. --no-bell starts muted; the interactive 'A' key toggles
	// the same mute. Read-only: the bell never touches squad state.
	m.alertsMuted = cfg.NoBell
	m.bell = func() { _, _ = tty.WriteString("\a") }
	// Desktop notifier (#28): opt-in, fires on the same 0->N transition as the
	// bell but independently of the mute (the NOC's terminal is usually buried
	// exactly when the notification matters). Best effort via osascript.
	if cfg.Notify {
		m.notify = noc.DesktopNotify
	}
	// Seed an initial snapshot synchronously so the first frame is populated.
	m.ms = noc.Collect(rebuild.Roots, rebuild.Depth, rebuild.Probe, rebuild.Thresholds)
	m.ready = true
	m.refreshGuidance()

	opts := []tea.ProgramOption{
		tea.WithInput(tty),
		tea.WithOutput(tty),
		tea.WithAltScreen(),
	}
	// Drive the program as a POINTER so each key handler's cursor / collapse /
	// filter mutation lands on the SAME model the event loop re-binds and renders
	// on the next frame. With a value model + pointer-receiver movement helpers,
	// a keypress would mutate a throwaway copy and the live surface would look
	// frozen (arrows / j / k dead).
	p := tea.NewProgram(&m, opts...)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startNOCFeeds(ctx, p, rebuild)

	_, err = p.Run()
	return err
}

// nocRebuildFromConfig assembles the immutable rebuild config.
func nocRebuildFromConfig(cfg NOCConfig) NOCRebuildConfig {
	depth := cfg.Depth
	if depth <= 0 {
		depth = noc.DefaultDepth
	}
	return NOCRebuildConfig{
		Roots:      cfg.Roots,
		Depth:      depth,
		Probe:      state.DefaultProbe,
		Thresholds: cfg.Thresholds,
		Refresh:    cfg.Refresh,
	}
}

// nocRebuildCmd collects a fresh MultiSnapshot off the immutable rebuild config
// and delivers it as a nocSnapshotMsg. Pure: it does not mutate the model.
func nocRebuildCmd(cfg NOCRebuildConfig) tea.Cmd {
	return func() tea.Msg {
		ms := noc.Collect(cfg.Roots, cfg.Depth, cfg.Probe, cfg.Thresholds)
		return nocSnapshotMsg{ms: ms}
	}
}

// nocTickCmd schedules the next periodic refresh at the given cadence.
func nocTickCmd(d time.Duration) tea.Cmd {
	if d <= 0 {
		d = NOCDefaultRefresh
	}
	return tea.Tick(d, func(_ time.Time) tea.Msg {
		return nocTickMsg{}
	})
}

// nocRediscoverTickCmd schedules the next periodic re-discovery.
func nocRediscoverTickCmd() tea.Cmd {
	return tea.Tick(NOCDefaultRediscover, func(_ time.Time) tea.Msg {
		return nocRediscoverMsg{}
	})
}

// writerIsTTY reports whether w is an *os.File backed by a terminal. Anything
// else (a bytes.Buffer in tests, a pipe in CI) is treated as non-TTY so the
// --once render stays plain text.
func writerIsTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
