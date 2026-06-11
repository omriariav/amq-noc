// Package console: noc_control.go implements the operator CONTROL layer.
//
// This is the controlled-action counterpart to the NOC visibility surface. It implements
// the GOAL.md safety model verbatim:
//
//   - Read-only is the default. The control keys (i/v/d/a/r/x/m/b/S/R/X/N/T/o) are ADDITIVE;
//     none of the existing nav/peek/filter/jump keys gain a side effect.
//   - Mutation is DELIBERATE + PREVIEW-FIRST + CONFIRM-GATED. Every mutating
//     action (read/drain/approve/reply/deny/message/broadcast/stop/resume/restart/new-session/new-team/amq-cleanup)
//     is TWO-STEP: the key opens a confirm overlay that shows the EXACT effect.
//     AMQ writes show the literal `amq send ...` from act.Preview, lifecycle
//     shows the exact stop/resume/restart command plus affected agents, and creation
//     shows the exact amq-squad new command. NOTHING runs until the operator
//     presses y/enter. Any other key (or esc) CANCELS with zero effect.
//   - Inject-the-seam. Side effects reach the outside world ONLY
//     through model seams: m.sendOp, lifecycle, creation, read, drain, and list.
//     Tests swap these seams for fakes. A declined overlay never touches a seam;
//     a confirmed overlay calls it exactly once with the exact payload.
//   - Focus-if-present-never-spawn. 'o' is read-only view movement, and it is
//     still confirm-gated before it focuses an EXISTING tmux window for the squad
//     (via the same switchTo seam jump uses). When nothing is running, it sets a
//     suggest-up note. It NEVER spawns and NEVER mutates squad state.
package console

import (
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/omriariav/amq-noc/internal/act"
	"github.com/omriariav/amq-noc/internal/catalog"
	"github.com/omriariav/amq-noc/internal/noc"
	"github.com/omriariav/amq-noc/internal/state"
	"github.com/omriariav/amq-noc/internal/team"
)

// controlKind names a controlled action for overlay headers and tests.
type controlKind int

const (
	ctlRead controlKind = iota
	ctlDrain
	ctlApprove
	ctlReply
	ctlDeny
	ctlDLQRead
	ctlDLQRetry
	ctlDLQPurge
	ctlDLQRetryAll
	ctlReceiptsWait
	ctlMessage
	ctlMessageWait
	ctlAMQCleanup
	ctlBroadcast
	ctlStop
	ctlResume
	ctlRestart
	ctlUp
	ctlAgentResume
	ctlArchive
	ctlRemove
	ctlNewSession
	ctlNewTeam
	ctlDeleteTeam
	ctlSyncPointers
	ctlForkPlan
	ctlBriefSeed
	ctlThreadContextAny
	ctlSendPrompt
	ctlDirective
)

func (k controlKind) label() string {
	switch k {
	case ctlRead:
		return "READ"
	case ctlDrain:
		return "DRAIN"
	case ctlApprove:
		return "APPROVE"
	case ctlReply:
		return "REPLY"
	case ctlDeny:
		return "DENY"
	case ctlDLQRead:
		return "DLQ READ"
	case ctlDLQRetry:
		return "DLQ RETRY"
	case ctlDLQPurge:
		return "DLQ PURGE"
	case ctlDLQRetryAll:
		return "DLQ RETRY ALL"
	case ctlReceiptsWait:
		return "RECEIPTS WAIT"
	case ctlMessage:
		return "MESSAGE"
	case ctlMessageWait:
		return "MESSAGE WAIT"
	case ctlAMQCleanup:
		return "AMQ CLEANUP"
	case ctlThreadContextAny:
		return "THREAD CONTEXT"
	case ctlBroadcast:
		return "BROADCAST"
	case ctlStop:
		return "STOP"
	case ctlResume:
		return "RESUME"
	case ctlRestart:
		return "RESTART"
	case ctlUp:
		return "UP"
	case ctlAgentResume:
		return "AGENT RESUME"
	case ctlArchive:
		return "ARCHIVE"
	case ctlRemove:
		return "REMOVE"
	case ctlNewSession:
		return "NEW SESSION"
	case ctlNewTeam:
		return "NEW TEAM"
	case ctlDeleteTeam:
		return "DELETE TEAM"
	case ctlSyncPointers:
		return "SYNC POINTERS"
	case ctlForkPlan:
		return "FORK PLAN"
	case ctlBriefSeed:
		return "BRIEF SEED"
	case ctlSendPrompt:
		return "SEND PROMPT"
	case ctlDirective:
		return "DIRECT LEAD"
	default:
		return "ACTION"
	}
}

// lifecycleVerb is the stop/resume/restart verb a lifecycleOp carries. It is a small
// string enum so the cli-injected seam can switch without importing console's
// controlKind.
type lifecycleVerb string

const (
	lifecycleStop    lifecycleVerb = "stop"
	lifecycleResume  lifecycleVerb = "resume"
	lifecycleRestart lifecycleVerb = "restart"
	lifecycleUp      lifecycleVerb = "up"
)

// lifecycleOp is the exact lifecycle effect a confirmed stop/resume/restart runs. It is
// inert data (like act.OpMessage): building one performs no I/O. The cli-injected
// m.lifecycle seam turns it into an executeDown/executeResume call. ProjectDir +
// Session pin WHICH squad; Agents is the affected-roster preview the overlay
// shows so the operator confirms scope, not just verb.
type lifecycleOp struct {
	Verb       lifecycleVerb
	ProjectDir string
	Profile    string
	Session    string
	Agents     []string
}

// agentResumeOp is the exact single-agent launch effect a confirmed palette
// agent-resume action runs. It resumes one saved launch record by role.
type agentResumeOp struct {
	ProjectDir string
	Role       string
	Session    string
}

func (op agentResumeOp) command() string {
	parts := []string{squadCommandToken(), "agent", "resume", shellToken(op.Role)}
	if strings.TrimSpace(op.ProjectDir) != "" {
		parts = append(parts, "--project", shellToken(op.ProjectDir))
	}
	if strings.TrimSpace(op.Session) != "" {
		parts = append(parts, "--session", shellToken(op.Session))
	}
	return strings.Join(parts, " ")
}

// sendPromptOp is the exact prompt-delivery effect a confirmed NOC send-prompt
// action runs. It delivers a typed prompt to one agent's tmux pane via
// `amq-squad send`, reading the body from STDIN. This is NOT an AMQ message: it
// shells amq-squad, never act.Send, so there is no thread/kind. amq-squad owns
// the delivery (and the mid-turn "busy" refusal); amq-noc only previews the argv
// and pipes Body to the child. The seam (m.sendPrompt) is reached ONLY after the
// operator confirms the overlay; tests swap it for a fake so no test shells a
// real amq-squad.
type sendPromptOp struct {
	ProjectDir string
	Profile    string
	Session    string
	Role       string
	Body       string
}

// command renders the EXACT argv the seam runs, for the confirm preview. The
// body is piped via stdin (--body-file -), so it is NOT shown in the command;
// the overlay's "affects" line names the recipient. --profile is included only
// when non-default/non-empty, mirroring the lifecycle/new-session previews.
func (op sendPromptOp) command() string {
	parts := []string{squadCommandToken(), "send"}
	if strings.TrimSpace(op.ProjectDir) != "" {
		parts = append(parts, "--project", shellToken(op.ProjectDir))
	}
	if profile := strings.TrimSpace(op.Profile); profile != "" && profile != team.DefaultProfile {
		parts = append(parts, "--profile", shellToken(profile))
	}
	if strings.TrimSpace(op.Session) != "" {
		parts = append(parts, "--session", shellToken(op.Session))
	}
	if strings.TrimSpace(op.Role) != "" {
		parts = append(parts, "--role", shellToken(op.Role))
	}
	parts = append(parts, "--body-file", "-")
	return strings.Join(parts, " ")
}

// sessionCleanupOp is the exact archive/remove effect a confirmed NOC action
// runs for one workstream session.
type sessionCleanupOp struct {
	ProjectDir string
	Session    string
	Archive    bool
}

func (op sessionCleanupOp) command() string {
	verb := "rm"
	if op.Archive {
		verb = "archive"
	}
	parts := []string{squadCommandToken(), verb}
	if strings.TrimSpace(op.ProjectDir) != "" {
		parts = append(parts, "--project", shellToken(op.ProjectDir))
	}
	parts = append(parts, "--yes", shellToken(op.Session))
	return strings.Join(parts, " ")
}

// newSessionOp is the exact launch effect a confirmed NOC new-session action
// runs. It creates a detached tmux session for a selected team-home.
type newSessionOp struct {
	ProjectDir string
	Profile    string
	Session    string
	SeedFrom   string
}

func (op newSessionOp) command() string {
	parts := []string{squadCommandToken(), "new", "session"}
	if strings.TrimSpace(op.ProjectDir) != "" {
		parts = append(parts, "--project", shellToken(op.ProjectDir))
	}
	if profile := strings.TrimSpace(op.Profile); profile != "" && profile != "default" {
		parts = append(parts, "--profile", shellToken(profile))
	}
	if seedFrom := strings.TrimSpace(op.SeedFrom); seedFrom != "" {
		parts = append(parts, "--seed-from", shellToken(seedFrom))
	}
	terminalSession := nocTerminalSessionName(op.ProjectDir, op.Session)
	parts = append(parts, "--target", "new-session", "--terminal-session", shellToken(terminalSession), shellToken(op.Session))
	return strings.Join(parts, " ")
}

// newTeamOp is the exact profile-creation effect a confirmed NOC new-team
// action runs. Empty Profile means the selected team-home's default profile.
type newTeamOp struct {
	ProjectDir string
	Profile    string
	Roles      string
	Binary     string
	Session    string
}

// teamDeleteOp is the exact team-profile removal effect a confirmed NOC action
// runs. It deletes one team profile config, not sessions or briefs.
type teamDeleteOp struct {
	ProjectDir  string
	Profile     string
	AllProfiles bool
}

func (op teamDeleteOp) command() string {
	parts := []string{squadCommandToken(), "team", "rm"}
	if strings.TrimSpace(op.ProjectDir) != "" {
		parts = append(parts, "--project", shellToken(op.ProjectDir))
	}
	if op.AllProfiles {
		parts = append(parts, "--all-profiles")
	} else if profile := strings.TrimSpace(op.Profile); profile != "" {
		parts = append(parts, "--profile", shellToken(profile))
	}
	parts = append(parts, "--yes")
	return strings.Join(parts, " ")
}

// pointerSyncOp is the exact team-sync effect a confirmed NOC pointer repair
// action runs. Empty Profile means the selected team-home's default profile.
type pointerSyncOp struct {
	ProjectDir   string
	Profile      string
	AllowOutside bool
}

func (op pointerSyncOp) command() string {
	parts := []string{squadCommandToken(), "team", "sync"}
	if strings.TrimSpace(op.ProjectDir) != "" {
		parts = append(parts, "--project", shellToken(op.ProjectDir))
	}
	if profile := strings.TrimSpace(op.Profile); profile != "" && profile != "default" {
		parts = append(parts, "--profile", shellToken(profile))
	}
	if op.AllowOutside {
		parts = append(parts, "--allow-outside")
	}
	parts = append(parts, "--apply")
	return strings.Join(parts, " ")
}

// readNeedsYouOp is the exact AMQ read effect a confirmed NOC read action runs.
// It marks the operator's top needs-you message read and returns its body.
type readNeedsYouOp struct {
	Root           string
	OperatorHandle string
	MessageID      string
	Thread         string
	Subject        string
}

func (op readNeedsYouOp) command() string {
	return strings.Join([]string{
		"amq", "read",
		"--root", shellToken(op.Root),
		"--me", shellToken(operatorHandleOrDefault(op.OperatorHandle)),
		"--id", shellToken(op.MessageID),
		"--json",
	}, " ")
}

type readNeedsYouResult struct {
	MessageID string
	Thread    string
	Subject   string
	Body      string
}

type readResultOverlay struct {
	preview string
	result  readNeedsYouResult
}

// inboxAgentOp is the exact AMQ list effect a NOC inbox action runs. It is
// read-only: AMQ list does not move messages out of inbox/new.
type inboxAgentOp struct {
	Root   string
	Handle string
}

func (op inboxAgentOp) command() string {
	return strings.Join([]string{
		"amq", "list",
		"--root", shellToken(op.Root),
		"--me", shellToken(op.Handle),
		"--new",
	}, " ")
}

type inboxAgentResult struct {
	Handle string
	Output string
}

type inboxResultOverlay struct {
	preview string
	result  inboxAgentResult
}

// dlqAgentOp is the exact AMQ DLQ list effect a NOC DLQ action runs. It is
// read-only: AMQ DLQ list does not retry, purge, or move failed messages.
type dlqAgentOp struct {
	Root   string
	Handle string
}

func (op dlqAgentOp) command() string {
	return strings.Join([]string{
		"amq", "dlq", "list",
		"--root", shellToken(op.Root),
		"--me", shellToken(op.Handle),
	}, " ")
}

type dlqAgentResult struct {
	Handle string
	Output string
}

type dlqResultOverlay struct {
	preview string
	result  dlqAgentResult
}

// dlqReadOp is the exact AMQ DLQ read effect a NOC action runs. AMQ moves a new
// DLQ item to cur, so this is confirm-gated.
type dlqReadOp struct {
	Root   string
	Handle string
	ID     string
}

func (op dlqReadOp) command() string {
	return strings.Join([]string{
		"amq", "dlq", "read",
		"--root", shellToken(op.Root),
		"--me", shellToken(op.Handle),
		"--id", shellToken(op.ID),
	}, " ")
}

type dlqReadResult struct {
	Handle string
	ID     string
	Output string
}

type dlqReadResultOverlay struct {
	preview string
	result  dlqReadResult
}

// dlqRetryOp is the exact AMQ DLQ retry effect a NOC action runs. It moves one
// failed message back to the agent inbox after confirm.
type dlqRetryOp struct {
	Root   string
	Handle string
	ID     string
}

func (op dlqRetryOp) command() string {
	return strings.Join([]string{
		"amq", "dlq", "retry",
		"--root", shellToken(op.Root),
		"--me", shellToken(op.Handle),
		"--id", shellToken(op.ID),
	}, " ")
}

type dlqRetryResult struct {
	Handle string
	ID     string
	Output string
}

type dlqRetryResultOverlay struct {
	preview string
	result  dlqRetryResult
}

// dlqPurgeOp is the exact AMQ DLQ purge effect a NOC action runs. It
// permanently removes messages older than the requested age after confirm.
type dlqPurgeOp struct {
	Root      string
	Handle    string
	OlderThan string
}

func (op dlqPurgeOp) command() string {
	return strings.Join([]string{
		"amq", "dlq", "purge",
		"--root", shellToken(op.Root),
		"--me", shellToken(op.Handle),
		"--older-than", shellToken(op.OlderThan),
		"--yes",
	}, " ")
}

type dlqPurgeResult struct {
	Handle    string
	OlderThan string
	Output    string
}

type dlqPurgeResultOverlay struct {
	preview string
	result  dlqPurgeResult
}

// dlqRetryAllOp is the exact AMQ DLQ retry effect a NOC action runs. It moves
// every new DLQ item for an agent back to that agent's inbox after confirm.
type dlqRetryAllOp struct {
	Root   string
	Handle string
}

func (op dlqRetryAllOp) command() string {
	return strings.Join([]string{
		"amq", "dlq", "retry",
		"--root", shellToken(op.Root),
		"--me", shellToken(op.Handle),
		"--all",
	}, " ")
}

type dlqRetryAllResult struct {
	Handle string
	Output string
}

type dlqRetryAllResultOverlay struct {
	preview string
	result  dlqRetryAllResult
}

// receiptsAgentOp is the exact AMQ receipts list effect a NOC receipts action
// runs. It is read-only: AMQ receipts list only inspects delivery receipts.
type receiptsAgentOp struct {
	Root   string
	Handle string
}

func (op receiptsAgentOp) command() string {
	return strings.Join([]string{
		"amq", "receipts", "list",
		"--root", shellToken(op.Root),
		"--me", shellToken(op.Handle),
	}, " ")
}

type receiptsAgentResult struct {
	Handle string
	Output string
}

type receiptsResultOverlay struct {
	preview string
	result  receiptsAgentResult
}

// receiptsWaitOp is the exact AMQ receipts wait effect a NOC action runs. It
// waits for one delivery stage for a message without changing mailbox state.
type receiptsWaitOp struct {
	Root    string
	Handle  string
	MsgID   string
	Stage   string
	Timeout string
}

func (op receiptsWaitOp) command() string {
	return strings.Join([]string{
		"amq", "receipts", "wait",
		"--root", shellToken(op.Root),
		"--me", shellToken(op.Handle),
		"--msg-id", shellToken(op.MsgID),
		"--stage", shellToken(op.Stage),
		"--timeout", shellToken(op.Timeout),
	}, " ")
}

type receiptsWaitResult struct {
	Handle  string
	MsgID   string
	Stage   string
	Timeout string
	Output  string
}

type receiptsWaitResultOverlay struct {
	preview string
	result  receiptsWaitResult
}

// messageWaitOp is the exact AMQ send --wait-for effect a NOC action runs. It
// sends a direct operator message to one agent and waits for a drained receipt.
type messageWaitOp struct {
	Root           string
	OperatorHandle string
	Handle         string
	Body           string
	Timeout        string
}

func (op messageWaitOp) command() string {
	return strings.Join([]string{
		"amq", "send",
		"--root", shellToken(op.Root),
		"--me", shellToken(operatorHandleOrDefault(op.OperatorHandle)),
		"--to", shellToken(op.Handle),
		"--subject", shellToken("Message from operator"),
		"--body", shellToken(op.Body),
		"--kind", string(state.KindStatus),
		"--wait-for", "drained",
		"--wait-timeout", shellToken(op.Timeout),
	}, " ")
}

type messageWaitResult struct {
	Handle  string
	Timeout string
	Output  string
}

type messageWaitResultOverlay struct {
	preview string
	result  messageWaitResult
}

// threadContextOp is the exact AMQ thread effect a NOC context action runs.
// It is read-only: AMQ thread scans messages without moving inbox state.
type threadContextOp struct {
	Root    string
	Thread  string
	Subject string
}

func (op threadContextOp) command() string {
	return strings.Join([]string{
		"amq", "thread",
		"--root", shellToken(op.Root),
		"--id", shellToken(op.Thread),
		"--include-body",
		"--limit", "20",
	}, " ")
}

type threadContextResult struct {
	Thread  string
	Subject string
	Output  string
}

type threadContextResultOverlay struct {
	preview string
	result  threadContextResult
}

// amqOpsOp is the exact AMQ doctor --ops effect a NOC bus-health action runs.
// It is read-only and scoped by AM_ROOT to the selected session root.
type amqOpsOp struct {
	Root string
}

func (op amqOpsOp) command() string {
	return paletteAMQOpsCommand(op.Root)
}

type amqOpsResult struct {
	Root   string
	Output string
}

type amqOpsResultOverlay struct {
	preview string
	result  amqOpsResult
}

// amqWhoOp is the exact AMQ who effect a NOC project inventory action runs.
// It is read-only and scoped to a project base root.
type amqWhoOp struct {
	Root string
}

func (op amqWhoOp) command() string {
	return strings.Join([]string{
		"amq", "who",
		"--root", shellToken(op.Root),
	}, " ")
}

type amqWhoResult struct {
	Root   string
	Output string
}

type amqWhoResultOverlay struct {
	preview string
	result  amqWhoResult
}

// amqEnvOp is the exact AMQ env effect a NOC project environment action runs.
// It is read-only and scoped to a project base root.
type amqEnvOp struct {
	Root string
}

func (op amqEnvOp) command() string {
	return strings.Join([]string{
		"amq", "env",
		"--root", shellToken(op.Root),
		"--json",
	}, " ")
}

type amqEnvResult struct {
	Root   string
	Output string
}

type amqEnvResultOverlay struct {
	preview string
	result  amqEnvResult
}

// presenceOp is the exact AMQ presence list effect a NOC action runs. It is
// read-only and scoped to the selected session root.
type presenceOp struct {
	Root string
}

func (op presenceOp) command() string {
	return strings.Join([]string{
		"amq", "presence", "list",
		"--root", shellToken(op.Root),
	}, " ")
}

type presenceResult struct {
	Root   string
	Output string
}

type presenceResultOverlay struct {
	preview string
	result  presenceResult
}

// amqCleanupOp is the exact AMQ cleanup effect a confirmed NOC action runs.
// It is scoped to one selected session root and removes stale AMQ tmp files.
type amqCleanupOp struct {
	Root         string
	TmpOlderThan string
}

func (op amqCleanupOp) command() string {
	return strings.Join([]string{
		"amq", "cleanup",
		"--root", shellToken(op.Root),
		"--tmp-older-than", shellToken(op.TmpOlderThan),
		"--yes",
	}, " ")
}

type amqCleanupResult struct {
	Root         string
	TmpOlderThan string
	Output       string
}

type amqCleanupResultOverlay struct {
	preview string
	result  amqCleanupResult
}

// statusOp is the exact amq-squad status effect a NOC status action runs. It is
// read-only and can target either the project board or one session detail.
type statusOp struct {
	ProjectDir string
	Session    string
	Profile    string
}

func (op statusOp) command() string {
	parts := []string{squadCommandToken(), "status"}
	if strings.TrimSpace(op.ProjectDir) != "" {
		parts = append(parts, "--project", shellToken(op.ProjectDir))
	}
	if profile := strings.TrimSpace(op.Profile); profile != "" && profile != team.DefaultProfile {
		parts = append(parts, "--profile", shellToken(profile))
	}
	if session := strings.TrimSpace(op.Session); session != "" {
		parts = append(parts, "--session", shellToken(session))
	}
	return strings.Join(parts, " ")
}

type statusResult struct {
	ProjectDir string
	Session    string
	Profile    string
	Output     string
}

type statusResultOverlay struct {
	preview string
	result  statusResult
}

// threadsOp is the exact amq-squad threads effect a NOC session-threads action
// runs. It is read-only and renders collapsed thread summaries.
type threadsOp struct {
	ProjectDir string
	Session    string
}

func (op threadsOp) command() string {
	parts := []string{squadCommandToken(), "threads"}
	if strings.TrimSpace(op.ProjectDir) != "" {
		parts = append(parts, "--project", shellToken(op.ProjectDir))
	}
	if session := strings.TrimSpace(op.Session); session != "" {
		parts = append(parts, "--session", shellToken(session))
	}
	parts = append(parts, "--limit", "20")
	return strings.Join(parts, " ")
}

type threadsResult struct {
	ProjectDir string
	Session    string
	Output     string
}

type threadsResultOverlay struct {
	preview string
	result  threadsResult
}

// projectDoctorOp is the exact amq-squad doctor effect a NOC project-health
// action runs. It is read-only.
type projectDoctorOp struct {
	ProjectDir string
}

func (op projectDoctorOp) command() string {
	parts := []string{squadCommandToken(), "doctor"}
	if strings.TrimSpace(op.ProjectDir) != "" {
		parts = append(parts, "--project", shellToken(op.ProjectDir))
	}
	parts = append(parts, "--all-profiles")
	return strings.Join(parts, " ")
}

type projectDoctorResult struct {
	ProjectDir string
	Output     string
}

type projectDoctorResultOverlay struct {
	preview string
	result  projectDoctorResult
}

// projectHistoryOp is the exact amq-squad history effect a NOC project-history
// action runs. It is read-only.
type projectHistoryOp struct {
	ProjectDir string
}

func (op projectHistoryOp) command() string {
	parts := []string{squadCommandToken(), "history"}
	if strings.TrimSpace(op.ProjectDir) != "" {
		parts = append(parts, "--project", shellToken(op.ProjectDir))
	}
	return strings.Join(parts, " ")
}

type projectHistoryResult struct {
	ProjectDir string
	Output     string
}

type projectHistoryResultOverlay struct {
	preview string
	result  projectHistoryResult
}

// teamRulesOp is the exact amq-squad team-rules read effect a NOC project
// action runs. It is read-only.
type teamRulesOp struct {
	ProjectDir string
}

func (op teamRulesOp) command() string {
	parts := []string{squadCommandToken(), "team", "rules", "show"}
	if strings.TrimSpace(op.ProjectDir) != "" {
		parts = append(parts, "--project", shellToken(op.ProjectDir))
	}
	return strings.Join(parts, " ")
}

type teamRulesResult struct {
	ProjectDir string
	Path       string
	Content    string
}

type teamRulesResultOverlay struct {
	preview string
	result  teamRulesResult
}

// projectResumePlanOp is the exact amq-squad resume effect a NOC project
// recovery-plan action runs. It is read-only because resume without --exec only
// prints the per-member plan.
type projectResumePlanOp struct {
	ProjectDir string
	Profile    string
}

func (op projectResumePlanOp) command() string {
	parts := []string{squadCommandToken(), "resume"}
	if strings.TrimSpace(op.ProjectDir) != "" {
		parts = append(parts, "--project", shellToken(op.ProjectDir))
	}
	if profile := strings.TrimSpace(op.Profile); profile != "" && profile != team.DefaultProfile {
		parts = append(parts, "--profile", shellToken(profile))
	}
	return strings.Join(parts, " ")
}

type projectResumePlanResult struct {
	ProjectDir string
	Profile    string
	Output     string
}

type projectResumePlanResultOverlay struct {
	preview string
	result  projectResumePlanResult
}

// forkPlanOp is the exact amq-squad fork effect a NOC session fork-plan action
// runs. It is read-only because fork prints a plan and does not launch.
type forkPlanOp struct {
	ProjectDir  string
	Profile     string
	FromSession string
	ToSession   string
}

func (op forkPlanOp) command() string {
	parts := []string{squadCommandToken(), "fork"}
	if strings.TrimSpace(op.ProjectDir) != "" {
		parts = append(parts, "--project", shellToken(op.ProjectDir))
	}
	if profile := strings.TrimSpace(op.Profile); profile != "" && profile != team.DefaultProfile {
		parts = append(parts, "--profile", shellToken(profile))
	}
	parts = append(parts, "--from", shellToken(op.FromSession), "--as", shellToken(op.ToSession))
	return strings.Join(parts, " ")
}

type forkPlanResult struct {
	ProjectDir  string
	Profile     string
	FromSession string
	ToSession   string
	Output      string
}

type forkPlanResultOverlay struct {
	preview string
	result  forkPlanResult
}

// briefOp is the exact amq-squad brief effect a NOC session brief action runs.
// It is read-only and reports the full workstream brief for one session.
type briefOp struct {
	ProjectDir string
	Session    string
}

func (op briefOp) command() string {
	parts := []string{squadCommandToken(), "brief"}
	if strings.TrimSpace(op.ProjectDir) != "" {
		parts = append(parts, "--project", shellToken(op.ProjectDir))
	}
	parts = append(parts, "--session", shellToken(op.Session))
	return strings.Join(parts, " ")
}

type briefResult struct {
	ProjectDir string
	Session    string
	Path       string
	Kind       string
	Exists     bool
	Content    string
}

type briefResultOverlay struct {
	preview string
	result  briefResult
}

// briefSeedOp is the exact amq-squad brief seed effect a confirmed NOC action
// runs. It writes or overwrites one workstream brief after preview.
type briefSeedOp struct {
	ProjectDir string
	Session    string
	SeedFrom   string
	Force      bool
}

func (op briefSeedOp) command() string {
	parts := []string{squadCommandToken(), "brief", "seed"}
	if strings.TrimSpace(op.ProjectDir) != "" {
		parts = append(parts, "--project", shellToken(op.ProjectDir))
	}
	parts = append(parts, "--session", shellToken(op.Session), "--seed-from", shellToken(op.SeedFrom))
	if op.Force {
		parts = append(parts, "--force")
	}
	return strings.Join(parts, " ")
}

type roleMarketOverlay struct {
	project    string
	projectDir string
}

type teamProfilesOverlay struct {
	project    string
	projectDir string
	profiles   []string
}

// drainAgentOp is the exact AMQ drain effect a confirmed NOC drain action runs.
// It marks the selected agent's new mail read and returns the drain output.
type drainAgentOp struct {
	Root   string
	Handle string
}

func (op drainAgentOp) command() string {
	return strings.Join([]string{
		"amq", "drain",
		"--root", shellToken(op.Root),
		"--me", shellToken(op.Handle),
		"--include-body",
	}, " ")
}

type drainAgentResult struct {
	Handle string
	Output string
}

type drainResultOverlay struct {
	preview string
	result  drainAgentResult
}

func (op newTeamOp) command() string {
	profile := strings.TrimSpace(op.Profile)
	parts := []string{squadCommandToken(), "new", "team"}
	if profile != "" && profile != team.DefaultProfile {
		parts = []string{squadCommandToken(), "new", "profile", shellToken(profile)}
	}
	if strings.TrimSpace(op.ProjectDir) != "" {
		parts = append(parts, "--project", shellToken(op.ProjectDir))
	}
	parts = append(parts, "--roles", op.Roles)
	if binary := strings.TrimSpace(op.Binary); binary != "" {
		parts = append(parts, "--binary", shellToken(binary))
	}
	if session := strings.TrimSpace(op.Session); session != "" {
		parts = append(parts, "--session", shellToken(session))
	}
	return strings.Join(parts, " ")
}

// command renders the lifecycle command the overlay previews for confirm. It is
// the stop/resume analogue of act.Preview and mirrors the project-scoped CLI
// verbs: `amq-squad stop --project ... --all` / `amq-squad resume --project ...`.
func (op lifecycleOp) command() string {
	projectArgs := lifecycleProjectArgs(op.ProjectDir)
	profileArgs := lifecycleProfileArgs(op.Profile)
	squad := squadCommandToken()
	stop := squad + " stop" + projectArgs + " --all" + profileArgs + " --session " + shellToken(op.Session)
	terminalSession := nocTerminalSessionName(op.ProjectDir, op.Session)
	resume := squad + " resume" + projectArgs + profileArgs + " --exec --target new-session --terminal-session " +
		shellToken(terminalSession) + " --session " + shellToken(op.Session)

	switch op.Verb {
	case lifecycleStop:
		return stop
	case lifecycleResume:
		return resume
	case lifecycleRestart:
		return stop + " && " + resume
	case lifecycleUp:
		// up pins NO --session: amq-squad derives the workstream from the team
		// config (#22's lesson). Detached new-session target mirrors resume so
		// the NOC's own pane is never hijacked.
		return squad + " up" + projectArgs + profileArgs +
			" --target new-session --terminal-session " + shellToken(nocTerminalSessionName(op.ProjectDir, ""))
	default:
		return squad + " " + string(op.Verb) + projectArgs
	}
}

func lifecycleProjectArgs(projectDir string) string {
	projectDir = strings.TrimSpace(projectDir)
	if projectDir == "" {
		return ""
	}
	return " --project " + shellToken(projectDir)
}

func lifecycleProfileArgs(profile string) string {
	profile = strings.TrimSpace(profile)
	if profile == "" || profile == team.DefaultProfile {
		return ""
	}
	return " --profile " + shellToken(profile)
}

func nocTerminalSessionName(projectDir, session string) string {
	project := sanitizeNOCTmuxSessionName(filepath.Base(strings.TrimSpace(projectDir)))
	workstream := sanitizeNOCTmuxSessionName(session)
	base := "amq-squad-" + project
	if workstream == "" || workstream == "project" || workstream == project {
		return base
	}
	return base + "-" + workstream
}

func sanitizeNOCTmuxSessionName(s string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-'
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "project"
	}
	return out
}

// shellToken is a tiny display-only quoter for the lifecycle preview. The act
// package owns the real shellQuote for AMQ writes; the lifecycle command is
// always a sanitized session name, so a minimal quote keeps the preview honest
// without pulling act's unexported helper across the package boundary.
func shellToken(s string) string {
	if s == "" {
		return "''"
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./@:,=", r)) {
			return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
		}
	}
	return s
}

var generatedSquadCommandOverride string

// squadCommandToken is the binary token the NOC renders in confirmed
// squad-control previews. amq-noc delegates squad lifecycle/config actions to
// the installed amq-squad CLI, so previews show "amq-squad", never the running
// amq-noc executable. Tests redirect it through generatedSquadCommandOverride.
func squadCommandToken() string {
	if generatedSquadCommandOverride != "" {
		return shellToken(generatedSquadCommandOverride)
	}
	return "amq-squad"
}

func newSessionCommand(projectDir string) string {
	if strings.TrimSpace(projectDir) == "" {
		return squadCommandToken() + " new session <name>"
	}
	return squadCommandToken() + " new session --project " + shellToken(projectDir) + " <name>"
}

func projectAMQRoot(ps noc.ProjectSnapshot) string {
	if baseRoot := strings.TrimSpace(ps.Snap.BaseRoot); baseRoot != "" {
		return baseRoot
	}
	projectDir := strings.TrimSpace(ps.Dir)
	if projectDir == "" {
		return ""
	}
	if ps.SessionStore || len(ps.SessionNames) > 0 || len(ps.Snap.Sessions) > 0 {
		return filepath.Join(projectDir, noc.AgentMailDirName)
	}
	return ""
}

// pendingAction is the confirm overlay's state: a previewed, NOT-yet-executed
// mutating action. Exactly one of op (AMQ write) / life (lifecycle) is set. The
// preview is the literal command string the overlay renders and the seam runs.
type pendingAction struct {
	kind       controlKind
	preview    string
	read       *readNeedsYouOp // set for read-needs-you
	drain      *drainAgentOp   // set for agent drain
	dlqRead    *dlqReadOp      // set for DLQ read
	dlqRetry   *dlqRetryOp     // set for DLQ retry
	dlqPurge   *dlqPurgeOp     // set for DLQ purge
	dlqAll     *dlqRetryAllOp  // set for DLQ retry-all
	receipt    *receiptsWaitOp // set for receipts wait
	msgWait    *messageWaitOp  // set for message wait
	amqClean   *amqCleanupOp   // set for AMQ cleanup
	op         act.OpMessage   // set for approve/reply/deny/message/broadcast
	life       *lifecycleOp    // set for stop/resume/restart
	agent      *agentResumeOp  // set for single-agent resume
	cleanup    *sessionCleanupOp
	session    *newSessionOp  // set for new-session
	team       *newTeamOp     // set for new-team
	delTeam    *teamDeleteOp  // set for delete-team
	sync       *pointerSyncOp // set for pointer sync
	brief      *briefSeedOp
	sendPrompt *sendPromptOp // set for send-prompt (amq-squad send, body via stdin)
	// affected lists the agents the action touches, shown under the preview so
	// scope is explicit (recipients for an AMQ write, the roster for lifecycle).
	affected []string
	// warning is an optional consequence note rendered in the confirm overlay
	// (e.g. a new-session name diverging from the team's configured workstream).
	// It never blocks confirmation; it makes the consequence explicit first.
	warning string
}

// inputAction is the text editor before a read-only lookup or confirm overlay.
// The operator types here; on enter the action either runs a read-only seam or
// builds a pendingAction so the exact command is previewed before confirmation.
// esc cancels with zero effect.
type inputAction struct {
	kind controlKind
	// stage 0 = subject/profile for two-step editors, 1 = body/roles/session.
	// message/reply/deny/new-session/default-new-team skip straight to stage 1.
	stage   int
	subject string
	body    string
	extra   string
	hint    string
	// build turns the captured (subject, body) into the pendingAction. It is a
	// closure so the node context (root/session/thread/handles) is captured at
	// key-press time, not re-resolved after the snapshot may have moved.
	build           func(subject, body, extra string) pendingAction
	read            func(subject, body, extra string) tea.Cmd
	validateSubject func(string) error
	validateBody    func(string) error
	// stagePrefill optionally recomputes the stage-1 body prefill + hint once
	// the stage-0 choice is captured (e.g. new-session resolving the chosen
	// profile's configured workstream). Nil keeps the editor's static hint.
	stagePrefill func(choice string) (body, hint string)
}

// controlEnabled reports whether the control layer is active. It is always true
// in production; it exists so a future read-only-lock flag can disable the
// controlled command keys wholesale while leaving nav intact.
func (m *NOCModel) controlEnabled() bool { return true }

// handleControlKey routes a control key when no overlay/editor is open. It
// returns handled=false for any key it does not own so handleKey falls through
// to the existing read-only keymap (the control keys are strictly additive).
//
// Mutating branches only PREPARE state (a pending overlay or an input editor);
// none calls a mutating seam. The seam is reached exclusively from
// handleConfirmKey after an explicit confirm. The 'i' inbox branch is read-only,
// and the 'o' focus branch opens a confirm overlay before switching views.
func (m *NOCModel) handleControlKey(key string) (tea.Cmd, bool) {
	switch key {
	case "delete", "backspace":
		return m.beginDelete(), true
	case "d":
		return m.beginDrainAgent(), true
	case "a":
		return m.beginApproveOrDeny(ctlApprove), true
	case "r":
		return m.beginReply(), true
	case "x":
		return m.beginApproveOrDeny(ctlDeny), true
	case "m":
		return m.beginMessage(), true
	case "p":
		return m.beginSendPrompt(), true
	case "L":
		return m.beginDirective(), true
	case "v":
		return m.beginViewOutput(), true
	case "o":
		// Read-only focus on the selected agent's pane (#25): re-wired onto the
		// existing focus-confirm machinery (runtime-contract pane id first,
		// scrape fallback, not-in-tmux degrade with the exact command to run).
		_, cmd := m.jump()
		return cmd, true
	case "b":
		return m.beginBroadcast(), true
	case "S":
		return m.beginLifecycle(ctlStop), true
	case "R":
		return m.beginLifecycle(ctlResume), true
	case "X":
		return m.beginLifecycle(ctlRestart), true
	case "U":
		return m.beginUp(), true
	case "N":
		return m.beginNewSession(), true
	case "T":
		return m.beginNewTeam(), true
	}
	return nil, false
}

func (m *NOCModel) beginDelete() tea.Cmd {
	n, ok := m.selectedNode()
	if !ok || (n.kind != nodeProject && n.kind != nodeSession) {
		m.actNote = "delete applies to a project or session row"
		return nil
	}
	if n.kind == nodeSession {
		if isBaseRootSession(n.session) {
			m.actNote = "delete: (root) is the AMQ base mailbox, not a removable session"
			return nil
		}
		return m.beginSessionCleanupFor(n.project.Dir, n.session.Name, false)
	}
	return m.beginDeleteTeamForProject(n.project)
}

func (m *NOCModel) beginDeleteTeam() tea.Cmd {
	n, ok := m.selectedNode()
	if !ok || (n.kind != nodeProject && n.kind != nodeSession) {
		m.actNote = "delete team applies to a project or session row - select a team first"
		return nil
	}
	return m.beginDeleteTeamForProject(n.project)
}

// --- inbox / read / approve / reply / deny --------------------------------

// beginInboxAgent lists the selected agent's unread inbox. AMQ list is
// read-only, so this does not open a confirm overlay, but it still uses an
// injected seam so tests never shell out to a real bus.
func (m *NOCModel) beginInboxAgent() tea.Cmd {
	n, ok := m.selectedNode()
	if !ok || n.kind != nodeAgent {
		m.actNote = "inbox applies to an agent row - select an agent first"
		return nil
	}
	return m.beginInboxAgentFor(n.session.Root, n.agent.Handle)
}

func (m *NOCModel) beginInboxAgentFor(root, handle string) tea.Cmd {
	root = strings.TrimSpace(root)
	handle = strings.TrimSpace(handle)
	if root == "" {
		m.actNote = "inbox: selected agent has no session root"
		return nil
	}
	if handle == "" {
		m.actNote = "inbox: selected agent has no handle"
		return nil
	}
	op := inboxAgentOp{Root: root, Handle: handle}
	if m.inboxAgent == nil {
		m.actNote = "inbox unavailable in this context (no AMQ list backend)"
		return nil
	}
	res, err := m.inboxAgent(op)
	if err != nil {
		m.actNote = "inbox failed: " + err.Error()
		return nil
	}
	if strings.TrimSpace(res.Handle) == "" {
		res.Handle = handle
	}
	m.inboxResult = &inboxResultOverlay{preview: op.command(), result: res}
	m.actNote = "INBOX read: " + op.command()
	return nil
}

func (m *NOCModel) beginDLQAgent() tea.Cmd {
	n, ok := m.selectedNode()
	if !ok || n.kind != nodeAgent {
		m.actNote = "DLQ applies to an agent row - select an agent first"
		return nil
	}
	return m.beginDLQAgentFor(n.session.Root, n.agent.Handle)
}

func (m *NOCModel) beginDLQAgentFor(root, handle string) tea.Cmd {
	root = strings.TrimSpace(root)
	handle = strings.TrimSpace(handle)
	if root == "" {
		m.actNote = "DLQ: selected agent has no session root"
		return nil
	}
	if handle == "" {
		m.actNote = "DLQ: selected agent has no handle"
		return nil
	}
	op := dlqAgentOp{Root: root, Handle: handle}
	if m.dlqAgent == nil {
		m.actNote = "DLQ unavailable in this context (no AMQ DLQ backend)"
		return nil
	}
	res, err := m.dlqAgent(op)
	if err != nil {
		m.actNote = "DLQ failed: " + err.Error()
		return nil
	}
	if strings.TrimSpace(res.Handle) == "" {
		res.Handle = handle
	}
	m.dlqResult = &dlqResultOverlay{preview: op.command(), result: res}
	m.actNote = "DLQ read: " + op.command()
	return nil
}

func (m *NOCModel) beginDLQReadFor(root, handle string) tea.Cmd {
	root = strings.TrimSpace(root)
	handle = strings.TrimSpace(handle)
	if root == "" {
		m.actNote = "DLQ read: selected agent has no session root"
		return nil
	}
	if handle == "" {
		m.actNote = "DLQ read: selected agent has no handle"
		return nil
	}
	m.input = &inputAction{
		kind:         ctlDLQRead,
		stage:        1,
		hint:         "DLQ id from amq dlq list",
		validateBody: validateNOCDLQID,
		build: func(_, id, _ string) pendingAction {
			op := dlqReadOp{Root: root, Handle: handle, ID: strings.TrimSpace(id)}
			return pendingAction{kind: ctlDLQRead, preview: op.command(), dlqRead: &op, affected: []string{handle}}
		},
	}
	return nil
}

func (m *NOCModel) beginDLQRetryFor(root, handle string) tea.Cmd {
	root = strings.TrimSpace(root)
	handle = strings.TrimSpace(handle)
	if root == "" {
		m.actNote = "DLQ retry: selected agent has no session root"
		return nil
	}
	if handle == "" {
		m.actNote = "DLQ retry: selected agent has no handle"
		return nil
	}
	m.input = &inputAction{
		kind:         ctlDLQRetry,
		stage:        1,
		hint:         "DLQ id from amq dlq list",
		validateBody: validateNOCDLQID,
		build: func(_, id, _ string) pendingAction {
			op := dlqRetryOp{Root: root, Handle: handle, ID: strings.TrimSpace(id)}
			return pendingAction{kind: ctlDLQRetry, preview: op.command(), dlqRetry: &op, affected: []string{handle}}
		},
	}
	return nil
}

func (m *NOCModel) beginDLQPurgeFor(root, handle string) tea.Cmd {
	root = strings.TrimSpace(root)
	handle = strings.TrimSpace(handle)
	if root == "" {
		m.actNote = "DLQ purge: selected agent has no session root"
		return nil
	}
	if handle == "" {
		m.actNote = "DLQ purge: selected agent has no handle"
		return nil
	}
	m.input = &inputAction{
		kind:         ctlDLQPurge,
		stage:        1,
		hint:         "positive age threshold, for example 24h or 168h",
		validateBody: validateNOCDLQPurgeOlderThan,
		build: func(_, olderThan, _ string) pendingAction {
			op := dlqPurgeOp{Root: root, Handle: handle, OlderThan: strings.TrimSpace(olderThan)}
			return pendingAction{kind: ctlDLQPurge, preview: op.command(), dlqPurge: &op, affected: []string{handle}}
		},
	}
	return nil
}

func (m *NOCModel) beginDLQRetryAllFor(root, handle string) tea.Cmd {
	root = strings.TrimSpace(root)
	handle = strings.TrimSpace(handle)
	if root == "" {
		m.actNote = "DLQ retry-all: selected agent has no session root"
		return nil
	}
	if handle == "" {
		m.actNote = "DLQ retry-all: selected agent has no handle"
		return nil
	}
	op := dlqRetryAllOp{Root: root, Handle: handle}
	m.pending = &pendingAction{
		kind:     ctlDLQRetryAll,
		preview:  op.command(),
		dlqAll:   &op,
		affected: []string{handle},
	}
	return nil
}

func (m *NOCModel) beginReceiptsAgentFor(root, handle string) tea.Cmd {
	root = strings.TrimSpace(root)
	handle = strings.TrimSpace(handle)
	if root == "" {
		m.actNote = "receipts: selected agent has no session root"
		return nil
	}
	if handle == "" {
		m.actNote = "receipts: selected agent has no handle"
		return nil
	}
	op := receiptsAgentOp{Root: root, Handle: handle}
	if m.receiptsAgent == nil {
		m.actNote = "receipts unavailable in this context (no AMQ receipts backend)"
		return nil
	}
	res, err := m.receiptsAgent(op)
	if err != nil {
		m.actNote = "receipts failed: " + err.Error()
		return nil
	}
	if strings.TrimSpace(res.Handle) == "" {
		res.Handle = handle
	}
	m.receiptsResult = &receiptsResultOverlay{preview: op.command(), result: res}
	m.actNote = "RECEIPTS read: " + op.command()
	return nil
}

func (m *NOCModel) beginReceiptsWaitFor(root, handle string) tea.Cmd {
	root = strings.TrimSpace(root)
	handle = strings.TrimSpace(handle)
	if root == "" {
		m.actNote = "receipts wait: selected agent has no session root"
		return nil
	}
	if handle == "" {
		m.actNote = "receipts wait: selected agent has no handle"
		return nil
	}
	m.input = &inputAction{
		kind:         ctlReceiptsWait,
		stage:        1,
		hint:         "message ID, or: msg_123 stage=drained timeout=60s",
		validateBody: validateNOCReceiptsWaitInput,
		read: func(_, raw, _ string) tea.Cmd {
			spec, _ := parseNOCReceiptsWaitInput(raw)
			op := receiptsWaitOp{Root: root, Handle: handle, MsgID: spec.MsgID, Stage: spec.Stage, Timeout: spec.Timeout}
			return m.runReceiptsWaitReadOnly(op)
		},
	}
	return nil
}

func (m *NOCModel) runReceiptsWaitReadOnly(op receiptsWaitOp) tea.Cmd {
	if m.receiptsWait == nil {
		m.actNote = "receipts wait unavailable in this context (no AMQ receipts wait backend)"
		return nil
	}
	res, err := m.receiptsWait(op)
	if err != nil {
		m.actNote = "receipts wait failed: " + err.Error()
		return nil
	}
	preview := op.command()
	m.receiptsWaitResult = &receiptsWaitResultOverlay{preview: preview, result: res}
	m.actNote = "RECEIPTS WAIT done: " + preview
	return nil
}

func (m *NOCModel) beginThreadContext() tea.Cmd {
	th, sess, ok := m.selectedNeedsYouThread()
	if !ok {
		m.actNote = "context applies to a needs-you thread - nothing here needs you"
		return nil
	}
	return m.beginThreadContextFor(sess.Root, th)
}

func (m *NOCModel) beginThreadContextFor(root string, th state.ThreadSummary) tea.Cmd {
	root = strings.TrimSpace(root)
	threadID := strings.TrimSpace(th.ID)
	if root == "" {
		m.actNote = "context: selected session has no session root"
		return nil
	}
	if threadID == "" {
		m.actNote = "context: selected thread has no id"
		return nil
	}
	op := threadContextOp{Root: root, Thread: threadID, Subject: th.Subject}
	if m.threadContext == nil {
		m.actNote = "context unavailable in this context (no AMQ thread backend)"
		return nil
	}
	res, err := m.threadContext(op)
	if err != nil {
		m.actNote = "context failed: " + err.Error()
		return nil
	}
	if strings.TrimSpace(res.Thread) == "" {
		res.Thread = threadID
	}
	if strings.TrimSpace(res.Subject) == "" {
		res.Subject = th.Subject
	}
	m.threadContextResult = &threadContextResultOverlay{preview: op.command(), result: res}
	m.actNote = "CONTEXT read: " + op.command()
	return nil
}

func (m *NOCModel) beginThreadContextAnyInput(root string) tea.Cmd {
	root = strings.TrimSpace(root)
	if root == "" {
		m.actNote = "context: selected session has no session root"
		return nil
	}
	m.input = &inputAction{
		kind:         ctlThreadContextAny,
		stage:        1,
		hint:         "thread id, e.g. p2p/cto__fullstack or decision/ship",
		validateBody: validateNOCThreadID,
		read: func(_, body, _ string) tea.Cmd {
			return m.beginThreadContextFor(root, state.ThreadSummary{ID: strings.TrimSpace(body)})
		},
	}
	return nil
}

func (m *NOCModel) beginAMQOpsFor(root string) tea.Cmd {
	root = strings.TrimSpace(root)
	if root == "" {
		m.actNote = "AMQ ops: selected session has no session root"
		return nil
	}
	op := amqOpsOp{Root: root}
	if m.amqOps == nil {
		m.actNote = "AMQ ops unavailable in this context (no AMQ doctor backend)"
		return nil
	}
	res, err := m.amqOps(op)
	if err != nil {
		m.actNote = "AMQ ops failed: " + err.Error()
		return nil
	}
	if strings.TrimSpace(res.Root) == "" {
		res.Root = root
	}
	m.amqOpsResult = &amqOpsResultOverlay{preview: op.command(), result: res}
	m.actNote = "AMQ OPS read: " + op.command()
	return nil
}

func (m *NOCModel) beginAMQWhoFor(root string) tea.Cmd {
	root = strings.TrimSpace(root)
	if root == "" {
		m.actNote = "AMQ who: selected project has no AMQ base root"
		return nil
	}
	op := amqWhoOp{Root: root}
	if m.amqWho == nil {
		m.actNote = "AMQ who unavailable in this context (no AMQ who backend)"
		return nil
	}
	res, err := m.amqWho(op)
	if err != nil {
		m.actNote = "AMQ who failed: " + err.Error()
		return nil
	}
	if strings.TrimSpace(res.Root) == "" {
		res.Root = root
	}
	m.amqWhoResult = &amqWhoResultOverlay{preview: op.command(), result: res}
	m.actNote = "AMQ WHO read: " + op.command()
	return nil
}

func (m *NOCModel) beginAMQEnvFor(root string) tea.Cmd {
	root = strings.TrimSpace(root)
	if root == "" {
		m.actNote = "AMQ env: selected project has no AMQ base root"
		return nil
	}
	op := amqEnvOp{Root: root}
	if m.amqEnv == nil {
		m.actNote = "AMQ env unavailable in this context (no AMQ env backend)"
		return nil
	}
	res, err := m.amqEnv(op)
	if err != nil {
		m.actNote = "AMQ env failed: " + err.Error()
		return nil
	}
	if strings.TrimSpace(res.Root) == "" {
		res.Root = root
	}
	m.amqEnvResult = &amqEnvResultOverlay{preview: op.command(), result: res}
	m.actNote = "AMQ ENV read: " + op.command()
	return nil
}

func (m *NOCModel) beginPresenceFor(root string) tea.Cmd {
	root = strings.TrimSpace(root)
	if root == "" {
		m.actNote = "presence: selected session has no session root"
		return nil
	}
	op := presenceOp{Root: root}
	if m.presence == nil {
		m.actNote = "presence unavailable in this context (no AMQ presence backend)"
		return nil
	}
	res, err := m.presence(op)
	if err != nil {
		m.actNote = "presence failed: " + err.Error()
		return nil
	}
	if strings.TrimSpace(res.Root) == "" {
		res.Root = root
	}
	m.presenceResult = &presenceResultOverlay{preview: op.command(), result: res}
	m.actNote = "PRESENCE read: " + op.command()
	return nil
}

func (m *NOCModel) beginAMQCleanupFor(root string) tea.Cmd {
	root = strings.TrimSpace(root)
	if root == "" {
		m.actNote = "AMQ cleanup: selected session has no session root"
		return nil
	}
	m.input = &inputAction{
		kind:         ctlAMQCleanup,
		stage:        1,
		hint:         "positive tmp-file age threshold, for example 36h or 168h",
		validateBody: validateNOCAMQCleanupTmpOlderThan,
		build: func(_, olderThan, _ string) pendingAction {
			op := amqCleanupOp{Root: root, TmpOlderThan: strings.TrimSpace(olderThan)}
			return pendingAction{kind: ctlAMQCleanup, preview: op.command(), amqClean: &op, affected: []string{root}}
		},
	}
	return nil
}

func (m *NOCModel) beginProjectDoctorFor(projectDir string) tea.Cmd {
	projectDir = strings.TrimSpace(projectDir)
	if projectDir == "" {
		m.actNote = "doctor: selected project has no directory"
		return nil
	}
	op := projectDoctorOp{ProjectDir: projectDir}
	if m.projectDoctor == nil {
		m.actNote = "doctor unavailable in this context (no project doctor backend)"
		return nil
	}
	res, err := m.projectDoctor(op)
	if err != nil {
		m.actNote = "doctor failed: " + err.Error()
		return nil
	}
	if strings.TrimSpace(res.ProjectDir) == "" {
		res.ProjectDir = projectDir
	}
	m.projectDoctorResult = &projectDoctorResultOverlay{preview: op.command(), result: res}
	m.actNote = "DOCTOR read: " + op.command()
	return nil
}

func (m *NOCModel) beginProjectHistoryFor(projectDir string) tea.Cmd {
	projectDir = strings.TrimSpace(projectDir)
	if projectDir == "" {
		m.actNote = "history: selected project has no directory"
		return nil
	}
	op := projectHistoryOp{ProjectDir: projectDir}
	if m.projectHistory == nil {
		m.actNote = "history unavailable in this context (no project history backend)"
		return nil
	}
	res, err := m.projectHistory(op)
	if err != nil {
		m.actNote = "history failed: " + err.Error()
		return nil
	}
	if strings.TrimSpace(res.ProjectDir) == "" {
		res.ProjectDir = projectDir
	}
	m.projectHistoryResult = &projectHistoryResultOverlay{preview: op.command(), result: res}
	m.actNote = "HISTORY read: " + op.command()
	return nil
}

func (m *NOCModel) beginTeamRulesFor(projectDir string) tea.Cmd {
	projectDir = strings.TrimSpace(projectDir)
	if projectDir == "" {
		m.actNote = "team rules: selected project has no directory"
		return nil
	}
	op := teamRulesOp{ProjectDir: projectDir}
	if m.teamRules == nil {
		m.actNote = "team rules unavailable in this context (no team rules backend)"
		return nil
	}
	res, err := m.teamRules(op)
	if err != nil {
		m.actNote = "team rules failed: " + err.Error()
		return nil
	}
	if strings.TrimSpace(res.ProjectDir) == "" {
		res.ProjectDir = projectDir
	}
	m.teamRulesResult = &teamRulesResultOverlay{preview: op.command(), result: res}
	m.actNote = "TEAM RULES read: " + op.command()
	return nil
}

func (m *NOCModel) beginProjectResumePlanFor(projectDir, profile string) tea.Cmd {
	projectDir = strings.TrimSpace(projectDir)
	profile = strings.TrimSpace(profile)
	if projectDir == "" {
		m.actNote = "resume plan: selected project has no directory"
		return nil
	}
	op := projectResumePlanOp{ProjectDir: projectDir, Profile: profileForOp(profile)}
	if m.projectResumePlan == nil {
		m.actNote = "resume plan unavailable in this context (no project resume backend)"
		return nil
	}
	res, err := m.projectResumePlan(op)
	if err != nil {
		m.actNote = "resume plan failed: " + err.Error()
		return nil
	}
	if strings.TrimSpace(res.ProjectDir) == "" {
		res.ProjectDir = projectDir
	}
	if strings.TrimSpace(res.Profile) == "" {
		res.Profile = profileForOp(profile)
	}
	m.projectResumePlanResult = &projectResumePlanResultOverlay{preview: op.command(), result: res}
	m.actNote = "RESUME PLAN read: " + op.command()
	return nil
}

func (m *NOCModel) beginForkPlanInput(project noc.ProjectSnapshot, session, profile string) tea.Cmd {
	projectDir := strings.TrimSpace(project.Dir)
	session = strings.TrimSpace(session)
	if projectDir == "" {
		m.actNote = "fork plan: selected project has no directory"
		return nil
	}
	if session == "" {
		m.actNote = "fork plan: selected session has no name"
		return nil
	}
	profile = profileForOp(profile)
	m.input = &inputAction{
		kind: ctlForkPlan,
		hint: "target session name",
		validateBody: func(target string) error {
			return validateNOCNewSessionName(target, project)
		},
		read: func(_, target, _ string) tea.Cmd {
			return m.beginForkPlanFor(projectDir, profile, session, target)
		},
	}
	return nil
}

func (m *NOCModel) beginForkPlanFor(projectDir, profile, fromSession, toSession string) tea.Cmd {
	projectDir = strings.TrimSpace(projectDir)
	profile = strings.TrimSpace(profile)
	fromSession = strings.TrimSpace(fromSession)
	toSession = strings.TrimSpace(toSession)
	if projectDir == "" {
		m.actNote = "fork plan: selected project has no directory"
		return nil
	}
	if fromSession == "" {
		m.actNote = "fork plan: source session cannot be empty"
		return nil
	}
	if toSession == "" {
		m.actNote = "fork plan: target session cannot be empty"
		return nil
	}
	op := forkPlanOp{ProjectDir: projectDir, Profile: profileForOp(profile), FromSession: fromSession, ToSession: toSession}
	if m.forkPlan == nil {
		m.actNote = "fork plan unavailable in this context (no fork backend)"
		return nil
	}
	res, err := m.forkPlan(op)
	if err != nil {
		m.actNote = "fork plan failed: " + err.Error()
		return nil
	}
	if strings.TrimSpace(res.ProjectDir) == "" {
		res.ProjectDir = projectDir
	}
	if strings.TrimSpace(res.FromSession) == "" {
		res.FromSession = fromSession
	}
	if strings.TrimSpace(res.ToSession) == "" {
		res.ToSession = toSession
	}
	if strings.TrimSpace(res.Profile) == "" {
		res.Profile = profileForOp(profile)
	}
	m.forkPlanResult = &forkPlanResultOverlay{preview: op.command(), result: res}
	m.actNote = "FORK PLAN read: " + op.command()
	return nil
}

func (m *NOCModel) beginBriefFor(projectDir, session string) tea.Cmd {
	projectDir = strings.TrimSpace(projectDir)
	session = strings.TrimSpace(session)
	if projectDir == "" {
		m.actNote = "brief: selected project has no directory"
		return nil
	}
	if session == "" {
		m.actNote = "brief: selected session has no name"
		return nil
	}
	op := briefOp{ProjectDir: projectDir, Session: session}
	if m.brief == nil {
		m.actNote = "brief unavailable in this context (no brief backend)"
		return nil
	}
	res, err := m.brief(op)
	if err != nil {
		m.actNote = "brief failed: " + err.Error()
		return nil
	}
	if strings.TrimSpace(res.ProjectDir) == "" {
		res.ProjectDir = projectDir
	}
	if strings.TrimSpace(res.Session) == "" {
		res.Session = session
	}
	m.briefResult = &briefResultOverlay{preview: op.command(), result: res}
	m.actNote = "BRIEF read: " + op.command()
	return nil
}

func (m *NOCModel) beginBriefSeedFor(projectDir, session string) tea.Cmd {
	projectDir = strings.TrimSpace(projectDir)
	session = strings.TrimSpace(session)
	if projectDir == "" {
		m.actNote = "brief seed: selected project has no directory"
		return nil
	}
	if session == "" {
		m.actNote = "brief seed: selected session has no name"
		return nil
	}
	m.input = &inputAction{
		kind: ctlBriefSeed,
		hint: "seed source: file:./brief.md, issue:31, gh:owner/repo#31, plus optional force=true",
		build: func(_, body, _ string) pendingAction {
			seedFrom, force, _ := parseNOCBriefSeedInput(body)
			op := briefSeedOp{ProjectDir: projectDir, Session: session, SeedFrom: seedFrom, Force: force}
			return pendingAction{kind: ctlBriefSeed, preview: op.command(), brief: &op, affected: []string{session}}
		},
	}
	return nil
}

func (m *NOCModel) beginStatusFor(projectDir, session, profile string) tea.Cmd {
	projectDir = strings.TrimSpace(projectDir)
	session = strings.TrimSpace(session)
	profile = strings.TrimSpace(profile)
	if projectDir == "" {
		m.actNote = "status: selected project has no directory"
		return nil
	}
	op := statusOp{ProjectDir: projectDir, Session: session, Profile: profileForOp(profile)}
	if m.status == nil {
		m.actNote = "status unavailable in this context (no status backend)"
		return nil
	}
	res, err := m.status(op)
	if err != nil {
		m.actNote = "status failed: " + err.Error()
		return nil
	}
	if strings.TrimSpace(res.ProjectDir) == "" {
		res.ProjectDir = projectDir
	}
	if strings.TrimSpace(res.Session) == "" {
		res.Session = session
	}
	if strings.TrimSpace(res.Profile) == "" {
		res.Profile = op.Profile
	}
	m.statusResult = &statusResultOverlay{preview: op.command(), result: res}
	m.actNote = "STATUS read: " + op.command()
	return nil
}

func (m *NOCModel) beginThreadsFor(projectDir, session string) tea.Cmd {
	projectDir = strings.TrimSpace(projectDir)
	session = strings.TrimSpace(session)
	if projectDir == "" {
		m.actNote = "threads: selected project has no directory"
		return nil
	}
	if session == "" {
		m.actNote = "threads: selected session has no name"
		return nil
	}
	op := threadsOp{ProjectDir: projectDir, Session: session}
	if m.threads == nil {
		m.actNote = "threads unavailable in this context (no threads backend)"
		return nil
	}
	res, err := m.threads(op)
	if err != nil {
		m.actNote = "threads failed: " + err.Error()
		return nil
	}
	if strings.TrimSpace(res.ProjectDir) == "" {
		res.ProjectDir = projectDir
	}
	if strings.TrimSpace(res.Session) == "" {
		res.Session = session
	}
	m.threadsResult = &threadsResultOverlay{preview: op.command(), result: res}
	m.actNote = "THREADS read: " + op.command()
	return nil
}

// beginReadNeedsYou opens the confirm flow for reading the selected needs-you
// message body. AMQ read moves inbox/new to cur, so this is a confirmed mutation.
func (m *NOCModel) beginReadNeedsYou() tea.Cmd {
	th, sess, ok := m.selectedNeedsYouThread()
	if !ok {
		m.actNote = "read applies to a needs-you thread (a paused agent / open ask) - nothing here needs you"
		return nil
	}
	return m.beginReadNeedsYouFor(sess.Root, th, m.selectedOperatorHandle())
}

func (m *NOCModel) beginReadNeedsYouFor(root string, th state.ThreadSummary, operatorHandle string) tea.Cmd {
	root = strings.TrimSpace(root)
	operatorHandle = operatorHandleOrDefault(operatorHandle)
	messageID := strings.TrimSpace(th.LatestID)
	if root == "" {
		m.actNote = "read: selected session has no session root"
		return nil
	}
	if messageID == "" {
		m.actNote = "read needs a message id, but this thread has no latest message id"
		return nil
	}
	op := readNeedsYouOp{
		Root:           root,
		OperatorHandle: operatorHandle,
		MessageID:      messageID,
		Thread:         th.ID,
		Subject:        th.Subject,
	}
	m.pending = &pendingAction{
		kind:     ctlRead,
		preview:  op.command(),
		read:     &op,
		affected: []string{operatorHandle},
	}
	return nil
}

// beginDrainAgent opens the confirm flow for draining the selected agent's
// inbox. AMQ drain moves inbox/new to cur, so this is a confirmed mutation.
func (m *NOCModel) beginDrainAgent() tea.Cmd {
	n, ok := m.selectedNode()
	if !ok || n.kind != nodeAgent {
		m.actNote = "drain applies to an agent row - select an agent first"
		return nil
	}
	return m.beginDrainAgentFor(n.session.Root, n.agent.Handle)
}

func (m *NOCModel) beginDrainAgentFor(root, handle string) tea.Cmd {
	root = strings.TrimSpace(root)
	handle = strings.TrimSpace(handle)
	if root == "" {
		m.actNote = "drain: selected agent has no session root"
		return nil
	}
	if handle == "" {
		m.actNote = "drain: selected agent has no handle"
		return nil
	}
	op := drainAgentOp{Root: root, Handle: handle}
	m.pending = &pendingAction{
		kind:     ctlDrain,
		preview:  op.command(),
		drain:    &op,
		affected: []string{handle},
	}
	return nil
}

// beginApproveOrDeny opens the confirm flow for an approve/deny on the selected
// needs-you thread. Valid on a needs-you SESSION (acts on its top needs-you
// thread) or a needs-you AGENT (acts on the agent's needs-you thread). On any
// other node it is a no-op note. Approve previews immediately; deny opens the
// reason editor first (so the operator can type a reason), then previews.
func (m *NOCModel) beginApproveOrDeny(kind controlKind) tea.Cmd {
	th, sess, ok := m.selectedNeedsYouThread()
	if !ok {
		m.actNote = strings.ToLower(kind.label()) + " applies to a needs-you thread (a paused agent / open ask) - nothing here needs you"
		return nil
	}
	return m.beginApproveOrDenyFor(sess.Root, sess.Name, th, kind, m.selectedOperatorHandle())
}

func (m *NOCModel) beginApproveOrDenyFor(root, session string, th state.ThreadSummary, kind controlKind, operatorHandle string) tea.Cmd {
	root = strings.TrimSpace(root)
	session = strings.TrimSpace(session)
	operatorHandle = operatorHandleOrDefault(operatorHandle)
	if root == "" {
		m.actNote = strings.ToLower(kind.label()) + ": selected session has no session root"
		return nil
	}
	recipients := nonOperatorFor(th.Participants, operatorHandle)
	if kind == ctlApprove {
		op := act.ApproveAs(root, operatorHandle, th)
		m.pending = &pendingAction{
			kind:     ctlApprove,
			preview:  act.Preview(op),
			op:       op,
			affected: recipients,
		}
		return nil
	}
	// Deny: capture a reason first, then preview act.Deny.
	m.input = &inputAction{
		kind:  ctlDeny,
		stage: 1, // body == reason
		build: func(_, reason, _ string) pendingAction {
			op := act.DenyAs(root, operatorHandle, th, reason)
			return pendingAction{kind: ctlDeny, preview: act.Preview(op), op: op, affected: recipients}
		},
	}
	return nil
}

// beginReply opens the body editor for a custom answer on the selected
// needs-you thread. It has the same target selection as approve/deny, but lets
// the operator provide the answer body before previewing the exact AMQ send.
func (m *NOCModel) beginReply() tea.Cmd {
	th, sess, ok := m.selectedNeedsYouThread()
	if !ok {
		m.actNote = "reply applies to a needs-you thread (a paused agent / open ask) - nothing here needs you"
		return nil
	}
	return m.beginReplyFor(sess.Root, sess.Name, th, m.selectedOperatorHandle())
}

func (m *NOCModel) beginReplyFor(root, session string, th state.ThreadSummary, operatorHandle string) tea.Cmd {
	root = strings.TrimSpace(root)
	session = strings.TrimSpace(session)
	operatorHandle = operatorHandleOrDefault(operatorHandle)
	if root == "" {
		m.actNote = "reply: selected session has no session root"
		return nil
	}
	recipients := nonOperatorFor(th.Participants, operatorHandle)
	m.input = &inputAction{
		kind:  ctlReply,
		stage: 1,
		build: func(_, body, _ string) pendingAction {
			op := act.ReplyAs(root, operatorHandle, th, body)
			return pendingAction{kind: ctlReply, preview: act.Preview(op), op: op, affected: recipients}
		},
	}
	return nil
}

// --- message --------------------------------------------------------------

// beginMessage opens the body editor for a direct message to the selected
// AGENT. On any other node kind it is a no-op note. After the body is entered,
// the action previews a direct message addressed to that single agent.
func (m *NOCModel) beginMessage() tea.Cmd {
	n, ok := m.selectedNode()
	if !ok || (n.kind != nodeAgent && n.kind != nodeSession) {
		m.actNote = "message applies to an agent row - select an agent first"
		return nil
	}
	// Messaging the orchestrator IS the conversation (#27): m on an
	// orchestrated session or its lead row opens conversation mode. Worker
	// rows keep the one-shot kind-aware composer.
	if n.session.Orchestrated && (n.kind == nodeSession || n.agent.IsLead) {
		return m.openConversation(n.project, n.session)
	}
	if n.kind != nodeAgent {
		m.actNote = "message applies to an agent row; on an orchestrated session m opens the lead conversation"
		return nil
	}
	return m.beginMessageFor(n.session.Root, n.agent.Handle, m.selectedOperatorHandle())
}

func (m *NOCModel) beginMessageFor(root, handle, operatorHandle string) tea.Cmd {
	root = strings.TrimSpace(root)
	handle = strings.TrimSpace(handle)
	operatorHandle = operatorHandleOrDefault(operatorHandle)
	if root == "" {
		m.actNote = "message: selected agent has no session root"
		return nil
	}
	if handle == "" {
		m.actNote = "message: selected agent has no handle"
		return nil
	}
	m.input = &inputAction{
		kind:            ctlMessage,
		stage:           0, // kind choice first (#21); enter keeps the status default
		hint:            "message kind (enter keeps status): " + strings.Join(knownKindNames(), ", "),
		validateSubject: validateNOCMessageKind,
		build: func(kind, body, _ string) pendingAction {
			// A direct message is addressed to exactly the selected agent, not
			// pinned to a thread (it opens its own). Build the OpMessage
			// explicitly so the recipient is precisely that one handle, then
			// preview the exact `amq send` via act.Preview.
			op := act.OpMessage{
				Root:    root,
				Me:      operatorHandle,
				To:      handle,
				Subject: "Message from operator",
				Body:    body,
				Kind:    normalizedMessageKind(kind),
			}
			return pendingAction{kind: ctlMessage, preview: act.Preview(op), op: op, affected: []string{handle}}
		},
	}
	return nil
}

// knownKindNames renders state.KnownKinds for hints and validation errors.
func knownKindNames() []string {
	kinds := state.KnownKinds()
	out := make([]string, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, string(k))
	}
	return out
}

// validateNOCMessageKind accepts an empty choice (the status default) or any
// recognized AMQ kind, sourced from state.KnownKinds so the selector cannot
// drift from what the scanner recognizes. An unknown kind would be rejected
// by amq itself, so it is caught here before the confirm overlay.
func validateNOCMessageKind(raw string) error {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return nil
	}
	for _, k := range state.KnownKinds() {
		if raw == string(k) {
			return nil
		}
	}
	return errString("unknown kind " + raw + "; valid: " + strings.Join(knownKindNames(), ", "))
}

// normalizedMessageKind maps the captured kind choice to the wire value,
// defaulting an empty choice to status (the pre-#21 behavior).
func normalizedMessageKind(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return string(state.KindStatus)
	}
	return raw
}

// beginSendPrompt opens the body editor for a SEND PROMPT to the selected AGENT.
// Unlike beginMessage (an AMQ direct message via act.Send), this delivers the
// typed prompt straight to the agent's tmux pane via `amq-squad send`, piping the
// body to the child's STDIN. It mirrors beginMessage's node-kind guard (agent
// rows only) and resolves the squad-shell scope from the node: ProjectDir from
// the project, Session from the session, Role from the agent, Profile via the
// shared sessionCommandProfile helper (the "PROFILE" mixed-profile placeholder
// maps to "" so the preview omits --profile and lets amq-squad resolve it).
func (m *NOCModel) beginSendPrompt() tea.Cmd {
	n, ok := m.selectedNode()
	if !ok || n.kind != nodeAgent {
		m.actNote = "send prompt applies to an agent row - select an agent first"
		return nil
	}
	role := strings.TrimSpace(n.agent.Role)
	handle := strings.TrimSpace(n.agent.Handle)
	if role == "" {
		m.actNote = "send prompt: selected agent has no role"
		return nil
	}
	if handle == "" {
		handle = role
	}
	projectDir := strings.TrimSpace(n.project.Dir)
	session := strings.TrimSpace(n.session.Name)
	profile := sessionCommandProfile(n.project, n.session.Name)
	if profile == "PROFILE" {
		// Mixed profiles in one session: let amq-squad resolve the agent's own
		// profile rather than pinning a wrong one. Empty omits --profile.
		profile = ""
	}
	m.input = &inputAction{
		kind:  ctlSendPrompt,
		stage: 1, // body only
		build: func(_, body, _ string) pendingAction {
			op := sendPromptOp{
				ProjectDir: projectDir,
				Profile:    profile,
				Session:    session,
				Role:       role,
				Body:       body,
			}
			return pendingAction{kind: ctlSendPrompt, preview: op.command(), sendPrompt: &op, affected: []string{handle}}
		},
		validateBody: func(body string) error {
			if strings.TrimSpace(body) == "" {
				return errString("body cannot be empty")
			}
			return nil
		},
	}
	return nil
}

// --- direct the lead -------------------------------------------------------

// beginDirective opens the directive compose for the selected orchestrated
// squad's lead (#24): the human's core write action as the orchestrator's
// client. Reachable from the session row or the lead's own agent row. The
// delivery channel is deterministic: an OPERATIONAL lead gets the directive
// in its pane (amq-squad send, busy-guarded, never forced); a down lead gets
// a durable AMQ message to its inbox instead, read on its next drain/wake.
func (m *NOCModel) beginDirective() tea.Cmd {
	n, ok := m.selectedNode()
	if !ok || (n.kind != nodeSession && n.kind != nodeAgent) {
		m.actNote = "direct-lead applies to an orchestrated session or its lead agent row"
		return nil
	}
	if !n.session.Orchestrated {
		m.actNote = "direct-lead applies to an orchestrated squad (team.json orchestrated/lead); this session has no lead"
		return nil
	}
	if n.kind == nodeAgent && !n.agent.IsLead {
		m.actNote = "direct-lead targets the lead (" + n.session.LeadHandle + "); select the lead row or the session"
		return nil
	}
	return m.beginDirectiveForSession(n.project, n.session)
}

func (m *NOCModel) beginDirectiveForSession(project noc.ProjectSnapshot, sess state.Session) tea.Cmd {
	leadHandle := strings.TrimSpace(sess.LeadHandle)
	if leadHandle == "" {
		m.actNote = "direct-lead: session has no lead handle"
		return nil
	}
	leadRole := strings.TrimSpace(sess.LeadRole)
	if leadRole == "" {
		leadRole = leadHandle
	}
	var leadAg state.Agent
	leadFound := false
	for _, ag := range sess.Agents {
		if ag.IsLead || strings.EqualFold(ag.Handle, leadHandle) {
			leadAg = ag
			leadFound = true
			break
		}
	}
	nonEmptyBody := func(body string) error {
		if strings.TrimSpace(body) == "" {
			return errString("body cannot be empty")
		}
		return nil
	}

	if leadFound && agentOperational(leadAg) {
		projectDir := strings.TrimSpace(project.Dir)
		session := strings.TrimSpace(sess.Name)
		profile := sessionCommandProfile(project, sess.Name)
		if profile == "PROFILE" {
			profile = ""
		}
		m.input = &inputAction{
			kind:  ctlDirective,
			stage: 1,
			hint:  "directive to lead " + leadHandle + " via its pane (a busy pane refuses; never forced)",
			build: func(_, body, _ string) pendingAction {
				op := sendPromptOp{ProjectDir: projectDir, Profile: profile, Session: session, Role: leadRole, Body: body}
				return pendingAction{kind: ctlDirective, preview: op.command(), sendPrompt: &op, affected: []string{leadHandle}}
			},
			validateBody: nonEmptyBody,
		}
		return nil
	}

	root := strings.TrimSpace(sess.Root)
	if root == "" {
		m.actNote = "direct-lead: session has no AMQ root"
		return nil
	}
	operatorHandle := operatorHandleOrDefault(project.OperatorGateHandle())
	m.input = &inputAction{
		kind:  ctlDirective,
		stage: 1,
		hint:  "lead " + leadHandle + " pane is down; the directive goes to its AMQ inbox (read on next drain/wake)",
		build: func(_, body, _ string) pendingAction {
			op := act.OpMessage{
				Root:    root,
				Me:      operatorHandle,
				To:      leadHandle,
				Subject: directiveSubject(body),
				Body:    body,
				Thread:  directiveThread(leadHandle, operatorHandle),
				Kind:    "todo",
			}
			return pendingAction{kind: ctlDirective, preview: act.Preview(op), op: op, affected: []string{leadHandle}}
		},
		validateBody: nonEmptyBody,
	}
	return nil
}

// directiveThread is the deterministic thread for operator-to-lead directives:
// p2p/<a>__<b> with sorted handles, matching the squad p2p convention so the
// directive lands in the same conversation surface the lead already drains.
func directiveThread(leadHandle, operatorHandle string) string {
	a, b := leadHandle, operatorHandle
	if strings.Compare(strings.ToLower(a), strings.ToLower(b)) > 0 {
		a, b = b, a
	}
	return "p2p/" + a + "__" + b
}

// directiveSubject derives the AMQ subject from the directive's first line,
// capped for list rendering. The DIRECTIVE prefix keeps operator directives
// structurally recognizable in drains and thread lists.
func directiveSubject(body string) string {
	first := body
	if i := strings.IndexByte(first, '\n'); i >= 0 {
		first = first[:i]
	}
	first = strings.TrimSpace(first)
	const subjectCap = 72
	if len(first) > subjectCap {
		first = first[:subjectCap-3] + "..."
	}
	return "DIRECTIVE: " + first
}

func (m *NOCModel) beginMessageWaitFor(root, handle string) tea.Cmd {
	return m.beginMessageWaitForOperator(root, handle, m.selectedOperatorHandle())
}

func (m *NOCModel) beginMessageWaitForOperator(root, handle, operatorHandle string) tea.Cmd {
	root = strings.TrimSpace(root)
	handle = strings.TrimSpace(handle)
	operatorHandle = operatorHandleOrDefault(operatorHandle)
	if root == "" {
		m.actNote = "message wait: selected agent has no session root"
		return nil
	}
	if handle == "" {
		m.actNote = "message wait: selected agent has no handle"
		return nil
	}
	m.input = &inputAction{
		kind:            ctlMessageWait,
		stage:           0,
		hint:            "timeout, for example 60s or 5m",
		validateSubject: validateNOCMessageWaitTimeout,
		build: func(timeout, body, _ string) pendingAction {
			op := messageWaitOp{Root: root, OperatorHandle: operatorHandle, Handle: handle, Body: strings.TrimSpace(body), Timeout: strings.TrimSpace(timeout)}
			return pendingAction{kind: ctlMessageWait, preview: op.command(), msgWait: &op, affected: []string{handle}}
		},
	}
	return nil
}

// --- broadcast ------------------------------------------------------------

// beginBroadcast opens the subject-to-body editor for a broadcast to the selected
// SQUAD (a session or project node). On any other node it is a no-op note. After
// subject + body are entered, the action previews act.Broadcast to the squad's
// non-operator handles.
func (m *NOCModel) beginBroadcast() tea.Cmd {
	handles, root, session, _, note, ok := m.selectedSquad()
	if !ok {
		m.actNote = squadSelectionNote(note, "broadcast applies to a session or project (a squad); select one first")
		return nil
	}
	return m.beginBroadcastFor(root, session, handles, m.selectedOperatorHandle())
}

func (m *NOCModel) beginBroadcastFor(root, session string, handles []string, operatorHandle string) tea.Cmd {
	root = strings.TrimSpace(root)
	session = strings.TrimSpace(session)
	operatorHandle = operatorHandleOrDefault(operatorHandle)
	if root == "" {
		m.actNote = "broadcast: selected session has no session root"
		return nil
	}
	recipients := nonOperatorFor(handles, operatorHandle)
	if len(recipients) == 0 {
		m.actNote = "broadcast: no agents in this squad to address"
		return nil
	}
	m.input = &inputAction{
		kind:  ctlBroadcast,
		stage: 0, // subject first, then body
		build: func(subject, body, _ string) pendingAction {
			op := act.BroadcastAs(root, operatorHandle, handles, subject, body)
			return pendingAction{kind: ctlBroadcast, preview: act.Preview(op), op: op, affected: recipients}
		},
	}
	return nil
}

// --- stop / resume / restart ---------------------------------------------

// beginLifecycle opens the confirm overlay for stop/resume/restart on the selected
// SQUAD (session or project node). On any other node it is a no-op note. The
// overlay shows the exact lifecycle command + the affected agents; the seam is
// reached only on confirm.
func (m *NOCModel) beginLifecycle(kind controlKind) tea.Cmd {
	handles, _, session, sess, note, ok := m.selectedSquad()
	if !ok {
		m.actNote = squadSelectionNote(note, strings.ToLower(kind.label())+" applies to a session or project (a squad); select one first")
		return nil
	}
	dir := m.selectedProjectDir()
	return m.beginLifecycleFor(dir, session, handles, sess, kind)
}

func (m *NOCModel) beginLifecycleFor(projectDir, session string, handles []string, sess state.Session, kind controlKind) tea.Cmd {
	projectDir = strings.TrimSpace(projectDir)
	session = strings.TrimSpace(session)
	if projectDir == "" {
		m.actNote = strings.ToLower(kind.label()) + ": selected project has no directory"
		return nil
	}
	if session == "" {
		m.actNote = strings.ToLower(kind.label()) + ": selected session has no name"
		return nil
	}
	var verb lifecycleVerb
	switch kind {
	case ctlStop:
		verb = lifecycleStop
	case ctlResume:
		verb = lifecycleResume
	case ctlRestart:
		verb = lifecycleRestart
	default:
		m.actNote = "unknown lifecycle action"
		return nil
	}
	profiles := lifecycleProfilesForSession(sess)
	if len(profiles) > 1 {
		hint := "profiles: " + strings.Join(profiles, ", ")
		m.input = &inputAction{
			kind:  kind,
			stage: 0,
			hint:  hint,
			validateSubject: func(raw string) error {
				return validateNOCProfileChoice(raw, profiles)
			},
			build: func(profileChoice, _, _ string) pendingAction {
				profile := profileForOp(profileChoice)
				affected := handlesForProfile(sess.Agents, profileChoice)
				op := lifecycleOp{Verb: verb, ProjectDir: projectDir, Profile: profile, Session: session, Agents: affected}
				return pendingAction{kind: kind, preview: op.command(), life: &op, affected: affected}
			},
		}
		return nil
	}
	profile := ""
	if len(profiles) == 1 {
		profile = profileForOp(profiles[0])
	}
	op := lifecycleOp{Verb: verb, ProjectDir: projectDir, Profile: profile, Session: session, Agents: handles}
	m.pending = &pendingAction{
		kind:     kind,
		preview:  op.command(),
		life:     &op,
		affected: handles,
	}
	return nil
}

// --- up (launch the configured team) --------------------------------------

// beginUp opens the confirm overlay for launching the selected project's
// configured team profile via `amq-squad up` (#19). It deliberately pins NO
// --session: amq-squad derives the workstream from the team config (#22's
// lesson), and the right-pane launch actions already display the resolved
// workstream. The exec target is the same detached new-session convention as
// the resume exec path, so the NOC's own pane is never hijacked; the exact
// command (target included) is previewed before confirm.
func (m *NOCModel) beginUp() tea.Cmd {
	project, ok := m.selectedProjectSnapshot()
	if !ok {
		m.actNote = "up applies to a project, session, or agent row; select a squad first"
		return nil
	}
	return m.beginUpForProject(project)
}

func (m *NOCModel) beginUpForProject(project noc.ProjectSnapshot) tea.Cmd {
	projectDir := strings.TrimSpace(project.Dir)
	if projectDir == "" {
		m.actNote = "up: selected project has no directory"
		return nil
	}
	profile, choices, note, ok := launchProfileForNewSession(project)
	if !ok {
		m.actNote = note
		return nil
	}
	buildPending := func(rawProfile, opProfile string) pendingAction {
		op := lifecycleOp{Verb: lifecycleUp, ProjectDir: projectDir, Profile: opProfile}
		warning := ""
		configured, conflicts, okWS := configuredWorkstreamForProfile(project, rawProfile)
		switch {
		case !okWS && len(conflicts) > 1:
			warning = "members configure different workstreams (" + strings.Join(conflicts, ", ") + "); amq-squad may require an explicit session"
		case okWS && sessionNameExists(project, configured):
			// up boots fresh agents (bootstrap re-reads the brief + AMQ
			// history); resume reattaches the saved conversations. Make the
			// difference visible where the choice is made, not in docs.
			warning = "workstream " + configured + " already exists; up boots FRESH agents into it (R resume on the session restores saved conversations instead)"
		}
		return pendingAction{kind: ctlUp, preview: op.command(), life: &op, affected: []string{projectDir}, warning: warning}
	}
	if len(choices) > 0 {
		m.input = &inputAction{
			kind:  ctlUp,
			stage: 0,
			hint:  "profiles: " + strings.Join(choices, ", "),
			validateSubject: func(raw string) error {
				return validateNOCProfileChoice(raw, choices)
			},
			build: func(profileChoice, _, _ string) pendingAction {
				return buildPending(profileChoice, profileForOp(profileChoice))
			},
		}
		return nil
	}
	raw := team.DefaultProfile
	if profiles := projectLaunchProfiles(project); len(profiles) == 1 {
		raw = profiles[0]
	}
	m.pending = ptrPending(buildPending(raw, profile))
	return nil
}

// --- new session ---------------------------------------------------------

func (m *NOCModel) beginNewSession() tea.Cmd {
	project, ok := m.selectedProjectSnapshot()
	if !ok {
		m.actNote = "new session applies to a project, session, or agent row; select a squad first"
		return nil
	}
	return m.beginNewSessionForProject(project)
}

func (m *NOCModel) beginNewSessionForProject(project noc.ProjectSnapshot) tea.Cmd {
	projectDir := strings.TrimSpace(project.Dir)
	if projectDir == "" {
		m.actNote = "new session applies to a project, session, or agent row; select a squad first"
		return nil
	}
	profile, choices, note, ok := launchProfileForNewSession(project)
	if !ok {
		m.actNote = note
		return nil
	}
	stage := 1
	var validateSubject func(string) error
	var stagePrefill func(choice string) (string, string)
	hint := ""
	body := ""
	if len(choices) > 0 {
		stage = 0
		hint = "profiles: " + strings.Join(choices, ", ")
		validateSubject = func(raw string) error {
			return validateNOCProfileChoice(raw, choices)
		}
		stagePrefill = func(choice string) (string, string) {
			return newSessionPrefill(project, choice)
		}
	} else {
		body, hint = newSessionPrefill(project, profile)
	}
	m.input = &inputAction{
		kind:            ctlNewSession,
		stage:           stage,
		body:            body,
		hint:            hint,
		validateSubject: validateSubject,
		stagePrefill:    stagePrefill,
		validateBody: func(name string) error {
			return validateNOCNewSessionName(name, project)
		},
		build: func(profileChoice, session, _ string) pendingAction {
			launchProfile := profile
			effectiveProfile := profile
			if len(choices) > 0 {
				launchProfile = profileForOp(profileChoice)
				effectiveProfile = profileChoice
			}
			parsedSession, seedFrom, _ := parseNOCNewSessionInput(session)
			op := newSessionOp{ProjectDir: projectDir, Profile: launchProfile, Session: parsedSession, SeedFrom: seedFrom}
			warning := ""
			if configured, _, okWS := configuredWorkstreamForProfile(project, effectiveProfile); okWS && parsedSession != "" && parsedSession != configured {
				warning = "team is configured for workstream " + configured + "; launching under " + parsedSession + " creates a NEW workstream with a stub brief"
			}
			return pendingAction{kind: ctlNewSession, preview: op.command(), session: &op, affected: []string{projectDir}, warning: warning}
		},
	}
	return nil
}

// configuredWorkstreamForProfile resolves a launch profile's configured AMQ
// workstream from the collected team metadata. ok is true only when exactly
// one distinct member workstream hint is configured. conflicts carries the
// distinct hints when members disagree (ok=false); both are empty when the
// profile configures no workstream at all. An empty/op-normalized profile
// maps to the default profile.
func configuredWorkstreamForProfile(project noc.ProjectSnapshot, profile string) (name string, conflicts []string, ok bool) {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		profile = team.DefaultProfile
	}
	names := project.ConfiguredWorkstreams[profile]
	switch len(names) {
	case 0:
		return "", nil, false
	case 1:
		return names[0], nil, true
	default:
		return "", names, false
	}
}

// sessionNameExists reports whether name is an existing AMQ workstream of the
// project (session store directories or collected sessions).
func sessionNameExists(project noc.ProjectSnapshot, name string) bool {
	for _, existing := range project.SessionNames {
		if existing == name {
			return true
		}
	}
	return false
}

// newSessionPrefill resolves the new-session editor's body prefill + hint for
// a launch profile. The session name IS the AMQ workstream (mailboxes + brief
// + history), so when the team pins one configured workstream that has not
// been launched yet, the editor leads with it instead of free-asking (#22).
// An already-existing configured workstream is never prefilled: the name
// validator rejects it, and the right verb for it is resume, not new-session.
func newSessionPrefill(project noc.ProjectSnapshot, profile string) (body, hint string) {
	configured, conflicts, ok := configuredWorkstreamForProfile(project, profile)
	if !ok {
		if len(conflicts) > 1 {
			return "", "members configure different workstreams (" + strings.Join(conflicts, ", ") + "); the name you type is the AMQ workstream (mailboxes + brief)"
		}
		return "", "AMQ workstream name (mailboxes + brief), or: issue-97 seed-from=issue:31"
	}
	for _, existing := range project.SessionNames {
		if existing == configured {
			return "", "configured workstream " + configured + " already exists (select it and press R to resume); a different name creates a NEW workstream with a stub brief"
		}
	}
	return configured, "enter launches the configured workstream " + configured + "; a different name creates a NEW workstream (mailboxes + brief)"
}

func launchProfileForNewSession(project noc.ProjectSnapshot) (profile string, choices []string, note string, ok bool) {
	profiles := projectLaunchProfiles(project)
	if len(profiles) == 0 {
		if project.TeamConfigured {
			return "", nil, "new session cannot pick a valid team profile; press T to create a default team profile", false
		}
		return "", nil, "new session needs a team profile; press T to create one first", false
	}
	if len(profiles) == 1 {
		return profileForOp(profiles[0]), nil, "", true
	}
	return "", profiles, "", true
}

func projectLaunchProfiles(project noc.ProjectSnapshot) []string {
	out := []string{}
	seen := map[string]bool{}
	if project.DefaultTeam {
		out = append(out, team.DefaultProfile)
		seen[team.DefaultProfile] = true
	}
	for _, profile := range project.Profiles {
		profile = strings.TrimSpace(profile)
		if profile == "" {
			continue
		}
		if profile == team.DefaultProfile {
			if project.DefaultTeam && !seen[profile] {
				out = append(out, profile)
				seen[profile] = true
			}
			continue
		}
		if seen[profile] {
			continue
		}
		out = append(out, profile)
		seen[profile] = true
	}
	return out
}

func profileForOp(profile string) string {
	profile = strings.TrimSpace(profile)
	if profile == team.DefaultProfile {
		return ""
	}
	return profile
}

func (m *NOCModel) beginAgentResumeFor(projectDir, role, session string) tea.Cmd {
	projectDir = strings.TrimSpace(projectDir)
	role = strings.TrimSpace(role)
	session = strings.TrimSpace(session)
	if projectDir == "" {
		m.actNote = "agent resume: selected project has no directory"
		return nil
	}
	if role == "" {
		m.actNote = "agent resume needs a role from launch history"
		return nil
	}
	op := agentResumeOp{ProjectDir: projectDir, Role: role, Session: session}
	m.pending = &pendingAction{
		kind:     ctlAgentResume,
		preview:  op.command(),
		agent:    &op,
		affected: []string{role},
	}
	return nil
}

func (m *NOCModel) beginSessionCleanupFor(projectDir, session string, archive bool) tea.Cmd {
	projectDir = strings.TrimSpace(projectDir)
	session = strings.TrimSpace(session)
	label := "remove"
	kind := ctlRemove
	if archive {
		label = "archive"
		kind = ctlArchive
	}
	if projectDir == "" {
		m.actNote = label + ": selected project has no directory"
		return nil
	}
	if session == "" {
		m.actNote = label + ": (root) is the AMQ base mailbox, not a removable session"
		return nil
	}
	op := sessionCleanupOp{ProjectDir: projectDir, Session: session, Archive: archive}
	m.pending = &pendingAction{
		kind:     kind,
		preview:  op.command(),
		cleanup:  &op,
		affected: []string{session},
	}
	return nil
}

func namedTeamProfiles(project noc.ProjectSnapshot) []string {
	out := make([]string, 0, len(project.Profiles))
	for _, profile := range project.Profiles {
		profile = strings.TrimSpace(profile)
		if profile == "" || profile == "default" {
			continue
		}
		out = append(out, profile)
	}
	return out
}

func validateNOCProfileChoice(raw string, choices []string) error {
	profile := strings.TrimSpace(raw)
	if profile == "" {
		return errString("profile name cannot be empty")
	}
	for _, choice := range choices {
		if profile == choice {
			return nil
		}
	}
	return errString("unknown profile " + profile + "; choices: " + strings.Join(choices, ", "))
}

// --- new team ------------------------------------------------------------

func (m *NOCModel) beginNewTeam() tea.Cmd {
	project, ok := m.selectedProjectSnapshot()
	if !ok {
		m.actNote = "new team applies to a project, session, or agent row; select a team-home first"
		return nil
	}
	return m.beginNewTeamForProject(project)
}

func (m *NOCModel) beginNewTeamForProject(project noc.ProjectSnapshot) tea.Cmd {
	projectDir := strings.TrimSpace(project.Dir)
	if projectDir == "" {
		m.actNote = "new team applies to a project, session, or agent row; select a team-home first"
		return nil
	}
	stage := 1
	hint := ""
	var validateSubject func(string) error
	if project.TeamConfigured {
		stage = 0
		if profiles := projectLaunchProfiles(project); len(profiles) > 0 {
			hint = nocNewTeamProfileHint(profiles)
		}
		validateSubject = func(profile string) error {
			return validateNOCNewTeamProfile(profile, project)
		}
	} else {
		hint = nocTeamSpecHint()
	}
	m.input = &inputAction{
		kind:            ctlNewTeam,
		stage:           stage,
		hint:            hint,
		validateSubject: validateSubject,
		build: func(profile, specText, _ string) pendingAction {
			spec, _ := parseNOCTeamSpec(specText)
			op := newTeamOp{ProjectDir: projectDir, Profile: profileForOp(profile), Roles: spec.Roles, Binary: spec.Binary, Session: spec.Session}
			return pendingAction{kind: ctlNewTeam, preview: op.command(), team: &op, affected: []string{projectDir}}
		},
	}
	return nil
}

// --- delete team ---------------------------------------------------------

func (m *NOCModel) beginDeleteTeamForProject(project noc.ProjectSnapshot) tea.Cmd {
	projectDir := strings.TrimSpace(project.Dir)
	if projectDir == "" {
		m.actNote = "delete team applies to a configured team-home; select a project first"
		return nil
	}
	if !project.TeamConfigured {
		m.actNote = "delete team needs a configured team"
		return nil
	}
	profiles := projectLaunchProfiles(project)
	if len(profiles) == 0 {
		m.actNote = "delete team needs a team profile"
		return nil
	}
	if len(profiles) == 1 {
		profile := profiles[0]
		op := teamDeleteOp{ProjectDir: projectDir, Profile: profileForOp(profile)}
		m.pending = &pendingAction{
			kind:     ctlDeleteTeam,
			preview:  op.command(),
			delTeam:  &op,
			affected: []string{team.ProfilePath(projectDir, profile)},
		}
		return nil
	}
	m.input = &inputAction{
		kind:  ctlDeleteTeam,
		hint:  "delete what? 1 one profile, 2 all team config",
		extra: projectDir,
		validateSubject: func(mode string) error {
			return validateNOCDeleteMode(mode)
		},
		build: func(mode, value, projectDir string) pendingAction {
			mode, _ = normalizeNOCDeleteMode(mode)
			if mode == "all-team-config" {
				op := teamDeleteOp{ProjectDir: projectDir, AllProfiles: true}
				return pendingAction{kind: ctlDeleteTeam, preview: op.command(), delTeam: &op, affected: teamProfilePaths(projectDir, profiles)}
			}
			op := teamDeleteOp{ProjectDir: projectDir, Profile: profileForOp(value)}
			return pendingAction{kind: ctlDeleteTeam, preview: op.command(), delTeam: &op, affected: []string{team.ProfilePath(projectDir, value)}}
		},
	}
	return nil
}

func validateNOCDeleteMode(raw string) error {
	if _, ok := normalizeNOCDeleteMode(raw); ok {
		return nil
	}
	return errString("choose 1 for one profile or 2 for all team config")
}

func normalizeNOCDeleteMode(raw string) (string, bool) {
	mode := strings.ToLower(strings.TrimSpace(raw))
	mode = strings.Join(strings.Fields(strings.ReplaceAll(mode, "_", " ")), " ")
	switch mode {
	case "1", "one", "one profile", "profile", "profile only", "profile-only", "selected", "selected profile":
		return "profile-only", true
	case "2", "all", "all profiles", "all profile", "all team config", "all-team-config", "all config":
		return "all-team-config", true
	default:
		return "", false
	}
}

func deleteModeLabel(mode string) string {
	mode, _ = normalizeNOCDeleteMode(mode)
	if mode == "all-team-config" {
		return "all team config"
	}
	return "one profile"
}

func teamProfilePaths(projectDir string, profiles []string) []string {
	out := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		out = append(out, team.ProfilePath(projectDir, profile))
	}
	return out
}

func deleteProfilesForProject(ms noc.MultiSnapshot, projectDir string) []string {
	projectDir = strings.TrimSpace(projectDir)
	for _, project := range ms.Projects {
		if strings.TrimSpace(project.Dir) == projectDir {
			return projectLaunchProfiles(project)
		}
	}
	return nil
}

// --- sync pointers -------------------------------------------------------

func (m *NOCModel) beginPointerSyncForProject(project noc.ProjectSnapshot) tea.Cmd {
	projectDir := strings.TrimSpace(project.Dir)
	if projectDir == "" {
		m.actNote = "sync pointers applies to a configured team-home; select a project first"
		return nil
	}
	if !project.TeamConfigured {
		m.actNote = "sync pointers needs a configured team; press T to create one first"
		return nil
	}
	profiles := projectLaunchProfiles(project)
	if len(profiles) == 0 {
		m.actNote = "sync pointers needs a team profile; press T to create one first"
		return nil
	}
	build := func(profile string) pendingAction {
		op := pointerSyncOp{ProjectDir: projectDir, Profile: profileForOp(profile)}
		return pendingAction{kind: ctlSyncPointers, preview: op.command(), sync: &op, affected: []string{projectDir}}
	}
	if len(profiles) == 1 {
		m.pending = ptrPending(build(profiles[0]))
		return nil
	}
	m.input = &inputAction{
		kind: ctlSyncPointers,
		hint: "profiles: " + strings.Join(profiles, ", "),
		validateSubject: func(profile string) error {
			return validateNOCProfileChoice(profile, profiles)
		},
		build: func(profile, _, _ string) pendingAction {
			return build(profile)
		},
	}
	return nil
}

// --- focus / open ('o') ---------------------------------------------------

// focusTeam opens the READ-ONLY focus CONFIRM overlay for the selected squad
// (QA-2 / QA-4b): it does NOT focus immediately. It previews "Open/focus squad
// <session>?" and only a confirmed y/Y/enter runs the focus (performFocusTeam);
// any other key / esc cancels with zero effect. It is read-only view movement -
// never a mutation and never a spawn. (Validity / not-running notes are still
// surfaced eagerly so the operator is not asked to confirm a no-op.)
func (m *NOCModel) focusTeam() tea.Cmd {
	_, _, session, _, note, ok := m.selectedSquad()
	if !ok {
		m.actNote = squadSelectionNote(note, "open applies to a session or project (a squad); select one first")
		return nil
	}
	projectDir := m.selectedProjectDir()
	m.jumpPending = &pendingFocus{
		prompt: "Open/focus squad " + session + "? (focus its iTerm2 window)",
		run:    func(m *NOCModel) { m.performFocusTeam(session, projectDir) },
	}
	return nil
}

// performFocusTeam focuses an EXISTING tmux window for the squad: resolveSquadWindow
// (read-only) then the switchTo seam, or a suggest-up note when nothing is
// running. It NEVER spawns. It is reached ONLY from the focus-confirm gate, so a
// switchTo call here always corresponds to an operator confirm.
func (m *NOCModel) performFocusTeam(session, projectDir string) {
	panes, err := m.panes()
	if err != nil {
		m.actNote = "tmux not available: " + err.Error()
		return
	}
	target, found := resolveSquadWindow(session, projectDir, panes)
	if !found {
		m.actNote = "team not running; press R to resume it, or run " + newSessionCommand(projectDir)
		return
	}
	if err := m.switchTo(target); err != nil {
		if nit, isNIT := err.(*noc.NotInTmuxError); isNIT {
			m.actNote = "not inside tmux - run: " + nit.Command
			return
		}
		m.actNote = "open: " + err.Error() + " (try: " + noc.SuggestJump(target) + ")"
		return
	}
	m.actNote = "focused " + noc.SuggestJump(target)
}

// resolveSquadWindow finds an existing tmux window for the squad: a pane whose
// tmux-session name equals the amq session, or (fallback) a pane whose cwd is
// the project dir. Read-only: it only reads the pane list. found=false when no
// window exists (the squad is not running): the caller then suggests up/resume,
// never spawns.
func resolveSquadWindow(session, projectDir string, panes []noc.TmuxPane) (noc.TmuxTarget, bool) {
	// Prefer an exact tmux-session==amq-session match.
	for _, p := range panes {
		if session != "" && p.Session == session {
			return squadTargetFromPane(p), true
		}
	}
	// Fallback: any pane rooted in the project dir (a current-window launch puts
	// the squad in whatever tmux session the operator ran `up` from).
	want := cleanDirForFocus(projectDir)
	if want != "" {
		for _, p := range panes {
			if cleanDirForFocus(p.CWD) == want {
				return squadTargetFromPane(p), true
			}
		}
	}
	return noc.TmuxTarget{}, false
}

// squadTargetFromPane builds a focus target from a squad pane, carrying its
// title token + window name so the cross-session iTerm2 -CC focus (SwitchTo) can
// raise the right native window without switch-client exploding the layout.
func squadTargetFromPane(p noc.TmuxPane) noc.TmuxTarget {
	return noc.TmuxTarget{
		Session:    p.Session,
		Window:     p.Window,
		Pane:       p.Pane,
		Title:      p.Title,
		WindowName: p.WindowName,
	}
}

// --- confirm / input key routing -----------------------------------------

// handleConfirmKey routes a key while the confirm overlay is open. y/enter
// EXECUTES the previewed action through the seam exactly once; ANY other key
// (esc included) CANCELS with zero effect. This is the single gate between a
// keypress and a mutation.
func (m *NOCModel) handleConfirmKey(key string) (tea.Model, tea.Cmd) {
	p := m.pending
	switch key {
	case "y", "Y", "enter":
		m.pending = nil
		if m.runPending(p) {
			return m, nocRebuildCmd(m.rebuild)
		}
		return m, nil
	default:
		// Decline: clear the overlay, call NOTHING.
		m.pending = nil
		m.actNote = strings.ToLower(p.kind.label()) + " cancelled - nothing sent"
		return m, nil
	}
}

// handleResultKey closes the post-action result overlay. q keeps its normal quit
// meaning; every other key just returns to the live board.
func (m *NOCModel) handleResultKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "q", "ctrl+c":
		return m, tea.Quit
	default:
		m.readResult = nil
		m.drainResult = nil
		m.inboxResult = nil
		m.dlqResult = nil
		m.dlqReadResult = nil
		m.dlqRetryResult = nil
		m.dlqPurgeResult = nil
		m.dlqRetryAllResult = nil
		m.receiptsResult = nil
		m.receiptsWaitResult = nil
		m.messageWaitResult = nil
		m.amqCleanupResult = nil
		m.threadContextResult = nil
		m.amqOpsResult = nil
		m.amqWhoResult = nil
		m.amqEnvResult = nil
		m.presenceResult = nil
		m.roleMarket = nil
		m.teamProfiles = nil
		m.projectDoctorResult = nil
		m.projectHistoryResult = nil
		m.teamRulesResult = nil
		m.projectResumePlanResult = nil
		m.forkPlanResult = nil
		m.briefResult = nil
		m.statusResult = nil
		m.threadsResult = nil
		return m, nil
	}
}

// runPending executes a confirmed action through the appropriate seam. It
// returns true when the mutation succeeded and the caller should refresh the
// snapshot immediately.
//
// It is reached ONLY from handleConfirmKey on an explicit confirm, so a seam
// call here always corresponds to an operator confirm.
func (m *NOCModel) runPending(p *pendingAction) bool {
	if p == nil {
		return false
	}
	switch {
	case p.drain != nil:
		if m.drainAgent == nil {
			m.actNote = "drain unavailable in this context (no AMQ drain backend)"
			return false
		}
		res, err := m.drainAgent(*p.drain)
		if err != nil {
			m.actNote = "drain failed: " + err.Error()
			return false
		}
		m.drainResult = &drainResultOverlay{preview: p.preview, result: res}
		m.actNote = "DRAIN done: " + p.preview
		return true
	case p.dlqRead != nil:
		if m.dlqRead == nil {
			m.actNote = "DLQ read unavailable in this context (no AMQ DLQ read backend)"
			return false
		}
		res, err := m.dlqRead(*p.dlqRead)
		if err != nil {
			m.actNote = "DLQ read failed: " + err.Error()
			return false
		}
		m.dlqReadResult = &dlqReadResultOverlay{preview: p.preview, result: res}
		m.actNote = "DLQ READ done: " + p.preview
		return true
	case p.dlqRetry != nil:
		if m.dlqRetry == nil {
			m.actNote = "DLQ retry unavailable in this context (no AMQ DLQ retry backend)"
			return false
		}
		res, err := m.dlqRetry(*p.dlqRetry)
		if err != nil {
			m.actNote = "DLQ retry failed: " + err.Error()
			return false
		}
		m.dlqRetryResult = &dlqRetryResultOverlay{preview: p.preview, result: res}
		m.actNote = "DLQ RETRY done: " + p.preview
		return true
	case p.dlqPurge != nil:
		if m.dlqPurge == nil {
			m.actNote = "DLQ purge unavailable in this context (no AMQ DLQ purge backend)"
			return false
		}
		res, err := m.dlqPurge(*p.dlqPurge)
		if err != nil {
			m.actNote = "DLQ purge failed: " + err.Error()
			return false
		}
		m.dlqPurgeResult = &dlqPurgeResultOverlay{preview: p.preview, result: res}
		m.actNote = "DLQ PURGE done: " + p.preview
		return true
	case p.dlqAll != nil:
		if m.dlqRetryAll == nil {
			m.actNote = "DLQ retry-all unavailable in this context (no AMQ DLQ retry backend)"
			return false
		}
		res, err := m.dlqRetryAll(*p.dlqAll)
		if err != nil {
			m.actNote = "DLQ retry-all failed: " + err.Error()
			return false
		}
		m.dlqRetryAllResult = &dlqRetryAllResultOverlay{preview: p.preview, result: res}
		m.actNote = "DLQ RETRY ALL done: " + p.preview
		return true
	case p.msgWait != nil:
		if m.messageWait == nil {
			m.actNote = "message wait unavailable in this context (no AMQ send wait backend)"
			return false
		}
		res, err := m.messageWait(*p.msgWait)
		if err != nil {
			m.actNote = "message wait failed: " + err.Error()
			return false
		}
		m.messageWaitResult = &messageWaitResultOverlay{preview: p.preview, result: res}
		m.actNote = "MESSAGE WAIT done: " + p.preview
		return true
	case p.amqClean != nil:
		if m.amqCleanup == nil {
			m.actNote = "AMQ cleanup unavailable in this context (no AMQ cleanup backend)"
			return false
		}
		res, err := m.amqCleanup(*p.amqClean)
		if err != nil {
			m.actNote = "AMQ cleanup failed: " + err.Error()
			return false
		}
		m.amqCleanupResult = &amqCleanupResultOverlay{preview: p.preview, result: res}
		m.actNote = "AMQ CLEANUP done: " + p.preview
		return true
	case p.receipt != nil:
		if m.receiptsWait == nil {
			m.actNote = "receipts wait unavailable in this context (no AMQ receipts wait backend)"
			return false
		}
		res, err := m.receiptsWait(*p.receipt)
		if err != nil {
			m.actNote = "receipts wait failed: " + err.Error()
			return false
		}
		m.receiptsWaitResult = &receiptsWaitResultOverlay{preview: p.preview, result: res}
		m.actNote = "RECEIPTS WAIT done: " + p.preview
		return true
	case p.read != nil:
		if m.readNeedsYou == nil {
			m.actNote = "read unavailable in this context (no AMQ read backend)"
			return false
		}
		res, err := m.readNeedsYou(*p.read)
		if err != nil {
			m.actNote = "read failed: " + err.Error()
			return false
		}
		m.readResult = &readResultOverlay{preview: p.preview, result: res}
		m.actNote = "READ done: " + p.preview
		return true
	case p.team != nil:
		if m.newTeam == nil {
			m.actNote = "new team unavailable in this context (no team backend)"
			return false
		}
		if err := m.newTeam(*p.team); err != nil {
			m.actNote = "new team failed: " + err.Error()
			return false
		}
		m.actNote = "NEW TEAM sent: " + p.preview
		return true
	case p.delTeam != nil:
		if m.teamDelete == nil {
			m.actNote = "delete team unavailable in this context (no team delete backend)"
			return false
		}
		if err := m.teamDelete(*p.delTeam); err != nil {
			m.actNote = "delete team failed: " + err.Error()
			return false
		}
		m.actNote = "DELETE TEAM sent: " + p.preview
		return true
	case p.agent != nil:
		if m.agentResume == nil {
			m.actNote = "agent resume unavailable in this context (no agent resume backend)"
			return false
		}
		if err := m.agentResume(*p.agent); err != nil {
			m.actNote = "agent resume failed: " + err.Error()
			return false
		}
		m.actNote = "AGENT RESUME sent: " + p.preview
		return true
	case p.cleanup != nil:
		if m.sessionCleanup == nil {
			m.actNote = strings.ToLower(p.kind.label()) + " unavailable in this context (no session cleanup backend)"
			return false
		}
		if err := m.sessionCleanup(*p.cleanup); err != nil {
			m.actNote = strings.ToLower(p.kind.label()) + " failed: " + err.Error()
			return false
		}
		m.actNote = p.kind.label() + " sent: " + p.preview
		return true
	case p.sync != nil:
		if m.pointerSync == nil {
			m.actNote = "pointer sync unavailable in this context (no sync backend)"
			return false
		}
		if err := m.pointerSync(*p.sync); err != nil {
			m.actNote = "pointer sync failed: " + err.Error()
			return false
		}
		m.actNote = "SYNC POINTERS sent: " + p.preview
		return true
	case p.brief != nil:
		if m.briefSeed == nil {
			m.actNote = "brief seed unavailable in this context (no brief seed backend)"
			return false
		}
		if err := m.briefSeed(*p.brief); err != nil {
			m.actNote = "brief seed failed: " + err.Error()
			return false
		}
		m.actNote = "BRIEF SEED sent: " + p.preview
		return true
	case p.sendPrompt != nil:
		if m.sendPrompt == nil {
			m.actNote = strings.ToLower(p.kind.label()) + " unavailable in this context (no send prompt backend)"
			return false
		}
		if err := m.sendPrompt(*p.sendPrompt); err != nil {
			// amq-squad refuses a prompt while the agent is mid-turn (exit
			// non-zero with a "busy" message). Surface that as an actionable note
			// rather than a raw subprocess error so the operator knows to retry.
			if isAgentBusyError(err) {
				m.actNote = strings.ToLower(p.kind.label()) + ": agent busy (mid-turn); retry when idle"
				return false
			}
			m.actNote = strings.ToLower(p.kind.label()) + " failed: " + err.Error()
			return false
		}
		m.actNote = p.kind.label() + " sent (pane-only; does not clear needs-you): " + p.preview
		return true
	case p.session != nil:
		if m.newSession == nil {
			m.actNote = "new session unavailable in this context (no launch backend)"
			return false
		}
		if err := m.newSession(*p.session); err != nil {
			m.actNote = "new session failed: " + err.Error()
			return false
		}
		m.actNote = "NEW SESSION sent: " + p.preview
		return true
	case p.life != nil:
		if m.lifecycle == nil {
			m.actNote = strings.ToLower(p.kind.label()) + " unavailable in this context (no lifecycle backend)"
			return false
		}
		if err := m.lifecycle(*p.life); err != nil {
			m.actNote = strings.ToLower(p.kind.label()) + " failed: " + err.Error()
			return false
		}
		m.actNote = p.kind.label() + " sent: " + p.preview
		if v := p.life.Verb; v == lifecycleResume || v == lifecycleRestart || v == lifecycleUp {
			// These verbs (re)create a DETACHED tmux session. The NOC cannot
			// attach it safely itself (attach takes over the terminal, and
			// switch-client under iTerm2 -CC explodes layouts - the SwitchTo
			// lesson), so it puts the exact attach command one paste away.
			attach := "tmux -CC attach -t " + nocTerminalSessionName(p.life.ProjectDir, p.life.Session)
			suffix := ""
			if m.copyText != nil {
				if err := m.copyText(attach); err == nil {
					suffix = " (copied to clipboard)"
				}
			}
			m.actNote = p.kind.label() + " sent; attach when ready: " + attach + suffix
		}
		return true
	default:
		if m.sendOp == nil {
			m.actNote = strings.ToLower(p.kind.label()) + " unavailable (no AMQ backend)"
			return false
		}
		if err := m.sendOp(p.op); err != nil {
			m.actNote = strings.ToLower(p.kind.label()) + " failed: " + err.Error()
			return false
		}
		m.actNote = sentAMQActionNote(p.kind, p.preview)
		return true
	}
}

func sentAMQActionNote(kind controlKind, preview string) string {
	switch kind {
	case ctlApprove, ctlReply, ctlDeny:
		return kind.label() + " sent (gate answer; refresh should clear needs-you): " + preview
	case ctlMessage:
		return kind.label() + " sent (direct AMQ message; use reply/approve/deny to clear needs-you): " + preview
	case ctlDirective:
		return kind.label() + " sent (AMQ inbox; the lead reads it on its next drain/wake; does not clear gates): " + preview
	default:
		return kind.label() + " sent: " + preview
	}
}

// handleInputKey edits the input editor. enter advances two-step inputs
// (broadcast subject then body, profile then session/roles), then on the final
// stage runs a read-only lookup or builds the pending action and transitions to
// the confirm overlay. esc cancels with zero effect.
func (m *NOCModel) handleInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	in := m.input
	switch msg.String() {
	case "esc":
		m.input = nil
		m.actNote = strings.ToLower(in.kind.label()) + " cancelled - nothing sent"
		return m, nil
	case "enter":
		if in.kind == ctlBroadcast && in.stage == 0 {
			subject := strings.TrimSpace(in.subject)
			if subject == "" {
				m.actNote = "broadcast: subject cannot be empty"
				return m, nil
			}
			in.subject = subject
			// Captured subject; advance to body.
			in.stage = 1
			return m, nil
		}
		if in.kind == ctlMessage && in.stage == 0 {
			kindChoice := strings.TrimSpace(in.subject)
			validate := validateNOCMessageKind
			if in.validateSubject != nil {
				validate = in.validateSubject
			}
			if err := validate(kindChoice); err != nil {
				m.actNote = "message: " + err.Error()
				return m, nil
			}
			in.subject = kindChoice
			in.stage = 1
			in.hint = "message body"
			return m, nil
		}
		if in.kind == ctlNewTeam && in.stage == 0 {
			profile := strings.TrimSpace(in.subject)
			validate := validateNOCProfileName
			if in.validateSubject != nil {
				validate = in.validateSubject
			}
			if err := validate(profile); err != nil {
				m.actNote = "new team: " + err.Error()
				return m, nil
			}
			in.subject = profile
			in.stage = 1
			in.hint = nocTeamSpecHint()
			return m, nil
		}
		if in.kind == ctlNewSession && in.stage == 0 {
			profile := strings.TrimSpace(in.subject)
			validate := validateNOCProfileName
			if in.validateSubject != nil {
				validate = in.validateSubject
			}
			if err := validate(profile); err != nil {
				m.actNote = "new session: " + err.Error()
				return m, nil
			}
			in.subject = profile
			in.stage = 1
			in.hint = "session name, or: issue-97 seed-from=issue:31"
			if in.stagePrefill != nil {
				in.body, in.hint = in.stagePrefill(profile)
			}
			return m, nil
		}
		if lifecycleControlKind(in.kind) && in.stage == 0 {
			profile := strings.TrimSpace(in.subject)
			validate := validateNOCProfileName
			if in.validateSubject != nil {
				validate = in.validateSubject
			}
			if err := validate(profile); err != nil {
				m.actNote = strings.ToLower(in.kind.label()) + ": " + err.Error()
				return m, nil
			}
			in.subject = profile
			m.input = nil
			m.pending = ptrPending(in.build(in.subject, "", in.extra))
			return m, nil
		}
		if in.kind == ctlDeleteTeam && in.stage == 0 {
			mode := strings.TrimSpace(in.subject)
			validate := validateNOCDeleteMode
			if in.validateSubject != nil {
				validate = in.validateSubject
			}
			if err := validate(mode); err != nil {
				m.actNote = "delete team: " + err.Error()
				return m, nil
			}
			mode, _ = normalizeNOCDeleteMode(mode)
			in.subject = mode
			in.stage = 1
			projectDir := strings.TrimSpace(in.extra)
			profiles := deleteProfilesForProject(m.ms, projectDir)
			if mode == "all-team-config" {
				in.hint = "type project name " + filepath.Base(projectDir) + " to confirm all team config"
				in.validateBody = func(value string) error {
					if strings.TrimSpace(value) != filepath.Base(projectDir) {
						return errString("project name did not match")
					}
					return nil
				}
			} else {
				in.hint = "profiles: " + strings.Join(profiles, ", ")
				in.validateBody = func(profile string) error {
					return validateNOCProfileChoice(profile, profiles)
				}
			}
			return m, nil
		}
		if in.kind == ctlSyncPointers && in.stage == 0 {
			profile := strings.TrimSpace(in.subject)
			validate := validateNOCProfileName
			if in.validateSubject != nil {
				validate = in.validateSubject
			}
			if err := validate(profile); err != nil {
				m.actNote = strings.ToLower(in.kind.label()) + ": " + err.Error()
				return m, nil
			}
			in.subject = profile
			m.input = nil
			m.pending = ptrPending(in.build(in.subject, "", in.extra))
			return m, nil
		}
		if in.kind == ctlMessageWait && in.stage == 0 {
			timeout := strings.TrimSpace(in.subject)
			validate := validateNOCMessageWaitTimeout
			if in.validateSubject != nil {
				validate = in.validateSubject
			}
			if err := validate(timeout); err != nil {
				m.actNote = "message wait: " + err.Error()
				return m, nil
			}
			in.subject = timeout
			in.stage = 1
			in.hint = "message body"
			return m, nil
		}
		if in.kind == ctlReply || in.kind == ctlMessage || in.kind == ctlMessageWait || in.kind == ctlBroadcast {
			body := strings.TrimSpace(in.body)
			if body == "" {
				m.actNote = strings.ToLower(in.kind.label()) + ": body cannot be empty"
				return m, nil
			}
			in.body = body
		}
		if in.kind == ctlSendPrompt {
			body := strings.TrimSpace(in.body)
			if in.validateBody != nil {
				if err := in.validateBody(body); err != nil {
					m.actNote = strings.ToLower(in.kind.label()) + ": " + err.Error()
					return m, nil
				}
			}
			in.body = body
		}
		if in.kind == ctlDLQRead || in.kind == ctlDLQRetry || in.kind == ctlDLQPurge {
			body := strings.TrimSpace(in.body)
			if in.validateBody != nil {
				if err := in.validateBody(body); err != nil {
					m.actNote = strings.ToLower(in.kind.label()) + ": " + err.Error()
					return m, nil
				}
			}
			in.body = body
		}
		if in.kind == ctlReceiptsWait {
			body := strings.TrimSpace(in.body)
			if in.validateBody != nil {
				if err := in.validateBody(body); err != nil {
					m.actNote = "receipts wait: " + err.Error()
					return m, nil
				}
			}
			in.body = body
		}
		if in.kind == ctlAMQCleanup {
			body := strings.TrimSpace(in.body)
			if in.validateBody != nil {
				if err := in.validateBody(body); err != nil {
					m.actNote = "AMQ cleanup: " + err.Error()
					return m, nil
				}
			}
			in.body = body
		}
		if in.kind == ctlThreadContextAny {
			body := strings.TrimSpace(in.body)
			if in.validateBody != nil {
				if err := in.validateBody(body); err != nil {
					m.actNote = "thread context: " + err.Error()
					return m, nil
				}
			}
			in.body = body
		}
		if in.kind == ctlNewSession {
			name, _, err := parseNOCNewSessionInput(in.body)
			if err != nil {
				m.actNote = "new session: " + err.Error()
				return m, nil
			}
			if err := validateNOCSessionName(name); err != nil {
				m.actNote = "new session: " + err.Error()
				return m, nil
			}
			if in.validateBody != nil {
				if err := in.validateBody(name); err != nil {
					m.actNote = "new session: " + err.Error()
					return m, nil
				}
			}
			in.body = strings.TrimSpace(in.body)
		}
		if in.kind == ctlForkPlan {
			name := strings.TrimSpace(in.body)
			if err := validateNOCSessionName(name); err != nil {
				m.actNote = "fork plan: " + err.Error()
				return m, nil
			}
			if in.validateBody != nil {
				if err := in.validateBody(name); err != nil {
					m.actNote = "fork plan: " + err.Error()
					return m, nil
				}
			}
			in.body = name
		}
		if in.kind == ctlBriefSeed {
			seedFrom, _, err := parseNOCBriefSeedInput(in.body)
			if err != nil {
				m.actNote = "brief seed: " + err.Error()
				return m, nil
			}
			if err := validateNOCBriefSeedSource(seedFrom); err != nil {
				m.actNote = "brief seed: " + err.Error()
				return m, nil
			}
			in.body = strings.TrimSpace(in.body)
		}
		if in.kind == ctlDeleteTeam {
			body := strings.TrimSpace(in.body)
			if in.validateBody != nil {
				if err := in.validateBody(body); err != nil {
					m.actNote = "delete team: " + err.Error()
					return m, nil
				}
			}
			in.body = body
		}
		if in.kind == ctlNewTeam {
			spec, err := parseNOCTeamSpec(in.body)
			if err != nil {
				m.actNote = "new team: " + err.Error()
				return m, nil
			}
			in.body = strings.TrimSpace(in.body)
			in.extra = spec.Binary
		}
		// Final stage: run read-only input or build the confirm preview.
		m.input = nil
		if in.read != nil {
			return m, in.read(in.subject, in.body, in.extra)
		}
		m.pending = ptrPending(in.build(in.subject, in.body, in.extra))
		return m, nil
	case "backspace":
		if inputEditingSubject(in) {
			in.subject = dropLast(in.subject)
		} else {
			in.body = dropLast(in.body)
		}
		return m, nil
	default:
		if text := keyText(msg); text != "" {
			if inputEditingSubject(in) {
				in.subject += singleLineInputText(text)
			} else {
				if inputBodyAcceptsMultiline(in.kind) {
					in.body += normalizePastedText(text)
				} else {
					in.body += singleLineInputText(text)
				}
			}
		}
		return m, nil
	}
}

func inputEditingSubject(in *inputAction) bool {
	return in != nil && in.stage == 0 && (in.kind == ctlBroadcast || in.kind == ctlMessage || in.kind == ctlMessageWait || in.kind == ctlNewSession || in.kind == ctlNewTeam || in.kind == ctlSyncPointers || in.kind == ctlDeleteTeam || lifecycleControlKind(in.kind))
}

func inputBodyAcceptsMultiline(kind controlKind) bool {
	return kind == ctlReply || kind == ctlMessage || kind == ctlMessageWait || kind == ctlBroadcast || kind == ctlDeny || kind == ctlSendPrompt || kind == ctlDirective
}

func lifecycleControlKind(kind controlKind) bool {
	return kind == ctlStop || kind == ctlResume || kind == ctlRestart || kind == ctlUp
}

// ptrPending boxes a pendingAction value (the input builder returns a value).
func ptrPending(p pendingAction) *pendingAction { return &p }

// isAgentBusyError reports whether a send-prompt error is amq-squad's mid-turn
// refusal. amq-squad exits non-zero with a message containing "busy" when the
// target agent is mid-turn (and --force was not used); the NOC turns that into
// an actionable "retry when idle" note instead of a raw subprocess error.
func isAgentBusyError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "busy")
}

// dropLast trims the last rune of an editor buffer.
func dropLast(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	return string(runes[:len(runes)-1])
}

// --- selection helpers ----------------------------------------------------

// selectedNeedsYouThread returns the needs-you thread the read/approve/reply/deny
// keys act on for the selected node, plus its owning session. For a SESSION node it is the
// session's top needs-you thread; for an AGENT node it is that agent's
// highest-urgency needs-you thread. ok=false on any other node, or when the node
// has nothing that needs the operator.
func (m *NOCModel) selectedNeedsYouThread() (state.ThreadSummary, state.Session, bool) {
	n, ok := m.selectedNode()
	if !ok {
		return state.ThreadSummary{}, state.Session{}, false
	}
	switch n.kind {
	case nodeSession:
		ny := n.session.Coordination.NeedsYouThreads()
		th, found := mostUrgent(ny, "")
		return th, n.session, found
	case nodeAgent:
		ny := n.session.Coordination.NeedsYouThreads()
		th, found := mostUrgent(ny, n.agent.Handle)
		return th, n.session, found
	default:
		return state.ThreadSummary{}, state.Session{}, false
	}
}

// mostUrgent picks the highest-urgency needs-you thread (lowest AttnReason rank),
// optionally restricted to those a given handle participates in. ok=false when
// the filtered set is empty.
func mostUrgent(threads []state.ThreadSummary, handle string) (state.ThreadSummary, bool) {
	best := -1
	var chosen state.ThreadSummary
	for _, th := range threads {
		if handle != "" && !threadHasParticipant(th, handle) {
			continue
		}
		rank := th.AttnReason.Rank()
		if best < 0 || rank < best {
			best = rank
			chosen = th
		}
	}
	return chosen, best >= 0
}

// selectedSquad returns the handles, root, and session name of the squad the
// broadcast/lifecycle/open keys act on. A SESSION node yields that session. A
// PROJECT node yields its only session; if a project has multiple sessions the
// caller must force the operator to select one explicitly so a project-level key
// never silently mutates the wrong workstream.
func (m *NOCModel) selectedSquad() (handles []string, root, session string, sess state.Session, note string, ok bool) {
	n, ok2 := m.selectedNode()
	if !ok2 {
		return nil, "", "", state.Session{}, "", false
	}
	switch n.kind {
	case nodeSession:
		return agentHandles(n.session.Agents), n.session.Root, n.session.Name, n.session, "", true
	case nodeProject:
		sessions := sortedSessions(n.project.Snap.Sessions)
		if len(sessions) == 0 {
			return nil, "", "", state.Session{}, "", false
		}
		if len(sessions) > 1 {
			return nil, "", "", state.Session{}, "project has multiple sessions; select one session first", false
		}
		s := sessions[0]
		return agentHandles(s.Agents), s.Root, s.Name, s, "", true
	default:
		return nil, "", "", state.Session{}, "", false
	}
}

func lifecycleProfilesForSession(sess state.Session) []string {
	seen := map[string]bool{}
	for _, ag := range sess.Agents {
		profile := strings.TrimSpace(ag.TeamProfile)
		if profile == "" {
			profile = team.DefaultProfile
		}
		seen[profile] = true
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	if seen[team.DefaultProfile] {
		out = append(out, team.DefaultProfile)
		delete(seen, team.DefaultProfile)
	}
	for profile := range seen {
		out = append(out, profile)
	}
	start := 0
	if len(out) > 0 && out[0] == team.DefaultProfile {
		start = 1
	}
	sort.Strings(out[start:])
	return out
}

func handlesForProfile(agents []state.Agent, profile string) []string {
	want := profileForCompare(profile)
	out := []string{}
	for _, ag := range agents {
		if profileForCompare(ag.TeamProfile) != want {
			continue
		}
		if handle := strings.TrimSpace(ag.Handle); handle != "" {
			out = append(out, handle)
		}
	}
	return out
}

func profileForCompare(profile string) string {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return team.DefaultProfile
	}
	return profile
}

func squadSelectionNote(note, fallback string) string {
	if strings.TrimSpace(note) != "" {
		return note
	}
	return fallback
}

// selectedProjectDir returns the on-disk project dir for the selected node (used
// to pin lifecycle ops and as the focus fallback). Empty when not resolvable.
func (m *NOCModel) selectedProjectDir() string {
	n, ok := m.selectedNode()
	if !ok {
		return ""
	}
	return n.project.Dir
}

func (m *NOCModel) selectedProjectSnapshot() (noc.ProjectSnapshot, bool) {
	n, ok := m.selectedNode()
	if !ok || strings.TrimSpace(n.project.Dir) == "" {
		return noc.ProjectSnapshot{}, false
	}
	return n.project, true
}

func (m *NOCModel) selectedOperatorHandle() string {
	n, ok := m.selectedNode()
	if !ok {
		return state.DefaultOperatorHandle
	}
	return operatorHandleForProject(n.project)
}

func operatorHandleForProject(project noc.ProjectSnapshot) string {
	if handle := project.OperatorGateHandle(); handle != "" {
		return handle
	}
	return state.DefaultOperatorHandle
}

func operatorHandleOrDefault(handle string) string {
	if h := strings.TrimSpace(handle); h != "" {
		return h
	}
	return state.DefaultOperatorHandle
}

// agentHandles collects the (non-empty) handles of a session's agents.
func agentHandles(agents []state.Agent) []string {
	out := make([]string, 0, len(agents))
	for _, a := range agents {
		if h := strings.TrimSpace(a.Handle); h != "" {
			out = append(out, h)
		}
	}
	return out
}

// nonOperator drops the operator handle from a participant/handle list so a
// preview never shows the operator addressing itself. Order is preserved.
func nonOperator(in []string) []string {
	return nonOperatorFor(in, state.DefaultOperatorHandle)
}

func nonOperatorFor(in []string, operatorHandle string) []string {
	operatorHandle = operatorHandleOrDefault(operatorHandle)
	out := make([]string, 0, len(in))
	for _, h := range in {
		h = strings.TrimSpace(h)
		if h == "" || h == operatorHandle {
			continue
		}
		out = append(out, h)
	}
	return out
}

// cleanDirForFocus normalizes a dir for the focus cwd-fallback comparison.
func cleanDirForFocus(dir string) string {
	return strings.TrimRight(strings.TrimSpace(dir), "/")
}

// --- overlay rendering ----------------------------------------------------

// confirmOverlayView renders the confirm overlay: the action header, the EXACT
// preview (the literal command the seam will run), the affected agents, and the
// y/esc affordance. This is what makes mutation PREVIEW-FIRST: the operator sees
// byte-for-byte what will happen before any confirm.
func (m NOCModel) confirmOverlayView() string {
	p := m.pending
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.needsYou, "CONFIRM "+p.kind.label()))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.dim, "this will run:"))
	b.WriteString("\n  ")
	b.WriteString(m.th.paint(m.th.brand, p.preview))
	b.WriteString("\n")
	if len(p.affected) > 0 {
		b.WriteString(m.th.paint(m.th.dim, "affects: "+strings.Join(p.affected, ", ")))
		b.WriteString("\n")
	}
	if warning := strings.TrimSpace(p.warning); warning != "" {
		b.WriteString(m.th.paint(m.th.needsYou, "warning: "+warning))
		b.WriteString("\n")
	}
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	hint := "y confirm · esc cancel"
	if m.colorMode == ColorAscii {
		hint = "y confirm | esc cancel"
	}
	b.WriteString(m.th.paint(m.th.needsYou, hint))
	return b.String()
}

// focusConfirmOverlayView renders the READ-ONLY focus confirm overlay (jump / J
// / o): the action header, the prompt ("Jump to ... (focus its iTerm2 window)" /
// "Open/focus squad ..."), and the y/esc affordance. Unlike the mutating confirm
// it carries no `amq send` / lifecycle command - its only effect is terminal
// focus, so the prompt states the focus, not a squad-changing command.
func (m NOCModel) focusConfirmOverlayView() string {
	p := m.jumpPending
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.running, "CONFIRM FOCUS"))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.brand, p.prompt))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.dim, "read-only - moves your terminal view only, never squad state"))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	hint := "y focus · esc cancel"
	if m.colorMode == ColorAscii {
		hint = "y focus | esc cancel"
	}
	b.WriteString(m.th.paint(m.th.running, hint))
	return b.String()
}

// readResultOverlayView renders the AMQ read result after the confirmed read
// action completes. The body is shown inside the NOC instead of disappearing
// into a subprocess stdout stream.
func (m NOCModel) readResultOverlayView() string {
	p := m.readResult
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.needsYou, "READ RESULT"))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	if subject := strings.TrimSpace(p.result.Subject); subject != "" {
		b.WriteString(m.th.paint(m.th.dim, "subject: "+subject))
		b.WriteString("\n")
	}
	if thread := strings.TrimSpace(p.result.Thread); thread != "" {
		b.WriteString(m.th.paint(m.th.dim, "thread: "+thread))
		b.WriteString("\n")
	}
	if id := strings.TrimSpace(p.result.MessageID); id != "" {
		b.WriteString(m.th.paint(m.th.dim, "message: "+id))
		b.WriteString("\n")
	}
	if preview := strings.TrimSpace(p.preview); preview != "" {
		b.WriteString(m.th.paint(m.th.dim, "ran: "+preview))
		b.WriteString("\n")
	}
	b.WriteString(m.th.paint(m.th.dim, "body:"))
	b.WriteString("\n")
	body := strings.TrimRight(p.result.Body, "\n")
	if body == "" {
		body = "(empty)"
	}
	b.WriteString(m.th.paint(m.th.brand, body))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.needsYou, "enter close | esc close"))
	return b.String()
}

// drainResultOverlayView renders the AMQ drain result after the confirmed drain
// action completes. Empty output means there was nothing new to drain.
func (m NOCModel) drainResultOverlayView() string {
	p := m.drainResult
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.needsYou, "DRAIN RESULT"))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	if handle := strings.TrimSpace(p.result.Handle); handle != "" {
		b.WriteString(m.th.paint(m.th.dim, "agent: "+handle))
		b.WriteString("\n")
	}
	if preview := strings.TrimSpace(p.preview); preview != "" {
		b.WriteString(m.th.paint(m.th.dim, "ran: "+preview))
		b.WriteString("\n")
	}
	b.WriteString(m.th.paint(m.th.dim, "output:"))
	b.WriteString("\n")
	output := strings.TrimRight(p.result.Output, "\n")
	if output == "" {
		output = "(no new messages)"
	}
	b.WriteString(m.th.paint(m.th.brand, output))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.needsYou, "enter close | esc close"))
	return b.String()
}

// inboxResultOverlayView renders the AMQ list result after a read-only inbox
// inspection completes. Empty output means there was nothing unread.
func (m NOCModel) inboxResultOverlayView() string {
	p := m.inboxResult
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.needsYou, "INBOX"))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	if handle := strings.TrimSpace(p.result.Handle); handle != "" {
		b.WriteString(m.th.paint(m.th.dim, "agent: "+handle))
		b.WriteString("\n")
	}
	if preview := strings.TrimSpace(p.preview); preview != "" {
		b.WriteString(m.th.paint(m.th.dim, "ran: "+preview))
		b.WriteString("\n")
	}
	b.WriteString(m.th.paint(m.th.dim, "output:"))
	b.WriteString("\n")
	output := strings.TrimRight(p.result.Output, "\n")
	if output == "" {
		output = "(no unread messages)"
	}
	b.WriteString(m.th.paint(m.th.brand, output))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.needsYou, "enter close | esc close"))
	return b.String()
}

// dlqResultOverlayView renders the AMQ DLQ list result after a read-only DLQ
// inspection completes.
func (m NOCModel) dlqResultOverlayView() string {
	p := m.dlqResult
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.needsYou, "DLQ"))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	if handle := strings.TrimSpace(p.result.Handle); handle != "" {
		b.WriteString(m.th.paint(m.th.dim, "agent: "+handle))
		b.WriteString("\n")
	}
	if preview := strings.TrimSpace(p.preview); preview != "" {
		b.WriteString(m.th.paint(m.th.dim, "ran: "+preview))
		b.WriteString("\n")
	}
	b.WriteString(m.th.paint(m.th.dim, "output:"))
	b.WriteString("\n")
	output := strings.TrimRight(p.result.Output, "\n")
	if output == "" {
		output = "(no DLQ messages)"
	}
	b.WriteString(m.th.paint(m.th.brand, output))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.needsYou, "enter close | esc close"))
	return b.String()
}

// dlqReadResultOverlayView renders one AMQ DLQ item after a confirmed read.
func (m NOCModel) dlqReadResultOverlayView() string {
	p := m.dlqReadResult
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.needsYou, "DLQ READ RESULT"))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	if handle := strings.TrimSpace(p.result.Handle); handle != "" {
		b.WriteString(m.th.paint(m.th.dim, "agent: "+handle))
		b.WriteString("\n")
	}
	if id := strings.TrimSpace(p.result.ID); id != "" {
		b.WriteString(m.th.paint(m.th.dim, "dlq id: "+id))
		b.WriteString("\n")
	}
	if preview := strings.TrimSpace(p.preview); preview != "" {
		b.WriteString(m.th.paint(m.th.dim, "ran: "+preview))
		b.WriteString("\n")
	}
	b.WriteString(m.th.paint(m.th.dim, "output:"))
	b.WriteString("\n")
	output := strings.TrimRight(p.result.Output, "\n")
	if output == "" {
		output = "(empty DLQ read result)"
	}
	b.WriteString(m.th.paint(m.th.brand, output))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.needsYou, "enter close | esc close"))
	return b.String()
}

// dlqRetryResultOverlayView renders one AMQ DLQ retry result after confirm.
func (m NOCModel) dlqRetryResultOverlayView() string {
	p := m.dlqRetryResult
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.needsYou, "DLQ RETRY RESULT"))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	if handle := strings.TrimSpace(p.result.Handle); handle != "" {
		b.WriteString(m.th.paint(m.th.dim, "agent: "+handle))
		b.WriteString("\n")
	}
	if id := strings.TrimSpace(p.result.ID); id != "" {
		b.WriteString(m.th.paint(m.th.dim, "dlq id: "+id))
		b.WriteString("\n")
	}
	if preview := strings.TrimSpace(p.preview); preview != "" {
		b.WriteString(m.th.paint(m.th.dim, "ran: "+preview))
		b.WriteString("\n")
	}
	b.WriteString(m.th.paint(m.th.dim, "output:"))
	b.WriteString("\n")
	output := strings.TrimRight(p.result.Output, "\n")
	if output == "" {
		output = "(empty DLQ retry result)"
	}
	b.WriteString(m.th.paint(m.th.brand, output))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.needsYou, "enter close | esc close"))
	return b.String()
}

// dlqPurgeResultOverlayView renders an AMQ DLQ purge result after confirm.
func (m NOCModel) dlqPurgeResultOverlayView() string {
	p := m.dlqPurgeResult
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.needsYou, "DLQ PURGE RESULT"))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	if handle := strings.TrimSpace(p.result.Handle); handle != "" {
		b.WriteString(m.th.paint(m.th.dim, "agent: "+handle))
		b.WriteString("\n")
	}
	if olderThan := strings.TrimSpace(p.result.OlderThan); olderThan != "" {
		b.WriteString(m.th.paint(m.th.dim, "older-than: "+olderThan))
		b.WriteString("\n")
	}
	if preview := strings.TrimSpace(p.preview); preview != "" {
		b.WriteString(m.th.paint(m.th.dim, "ran: "+preview))
		b.WriteString("\n")
	}
	b.WriteString(m.th.paint(m.th.dim, "output:"))
	b.WriteString("\n")
	output := strings.TrimRight(p.result.Output, "\n")
	if output == "" {
		output = "(empty DLQ purge result)"
	}
	b.WriteString(m.th.paint(m.th.brand, output))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.needsYou, "enter close | esc close"))
	return b.String()
}

// dlqRetryAllResultOverlayView renders the AMQ DLQ retry-all result after a
// confirmed retry-all action completes.
func (m NOCModel) dlqRetryAllResultOverlayView() string {
	p := m.dlqRetryAllResult
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.needsYou, "DLQ RETRY ALL RESULT"))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	if handle := strings.TrimSpace(p.result.Handle); handle != "" {
		b.WriteString(m.th.paint(m.th.dim, "agent: "+handle))
		b.WriteString("\n")
	}
	if preview := strings.TrimSpace(p.preview); preview != "" {
		b.WriteString(m.th.paint(m.th.dim, "ran: "+preview))
		b.WriteString("\n")
	}
	b.WriteString(m.th.paint(m.th.dim, "output:"))
	b.WriteString("\n")
	output := strings.TrimRight(p.result.Output, "\n")
	if output == "" {
		output = "(no DLQ messages retried)"
	}
	b.WriteString(m.th.paint(m.th.brand, output))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.needsYou, "enter close | esc close"))
	return b.String()
}

// receiptsResultOverlayView renders the AMQ receipts list result after a
// read-only receipt inspection completes.
func (m NOCModel) receiptsResultOverlayView() string {
	p := m.receiptsResult
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.needsYou, "RECEIPTS"))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	if handle := strings.TrimSpace(p.result.Handle); handle != "" {
		b.WriteString(m.th.paint(m.th.dim, "agent: "+handle))
		b.WriteString("\n")
	}
	if preview := strings.TrimSpace(p.preview); preview != "" {
		b.WriteString(m.th.paint(m.th.dim, "ran: "+preview))
		b.WriteString("\n")
	}
	b.WriteString(m.th.paint(m.th.dim, "output:"))
	b.WriteString("\n")
	output := strings.TrimRight(p.result.Output, "\n")
	if output == "" {
		output = "(no receipts)"
	}
	b.WriteString(m.th.paint(m.th.brand, output))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.needsYou, "enter close | esc close"))
	return b.String()
}

// receiptsWaitResultOverlayView renders one AMQ receipts wait result after
// confirm.
func (m NOCModel) receiptsWaitResultOverlayView() string {
	p := m.receiptsWaitResult
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.needsYou, "RECEIPTS WAIT RESULT"))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	if handle := strings.TrimSpace(p.result.Handle); handle != "" {
		b.WriteString(m.th.paint(m.th.dim, "agent: "+handle))
		b.WriteString("\n")
	}
	if msgID := strings.TrimSpace(p.result.MsgID); msgID != "" {
		b.WriteString(m.th.paint(m.th.dim, "message: "+msgID))
		b.WriteString("\n")
	}
	if stage := strings.TrimSpace(p.result.Stage); stage != "" {
		b.WriteString(m.th.paint(m.th.dim, "stage: "+stage))
		b.WriteString("\n")
	}
	if timeout := strings.TrimSpace(p.result.Timeout); timeout != "" {
		b.WriteString(m.th.paint(m.th.dim, "timeout: "+timeout))
		b.WriteString("\n")
	}
	if preview := strings.TrimSpace(p.preview); preview != "" {
		b.WriteString(m.th.paint(m.th.dim, "ran: "+preview))
		b.WriteString("\n")
	}
	b.WriteString(m.th.paint(m.th.dim, "output:"))
	b.WriteString("\n")
	output := strings.TrimRight(p.result.Output, "\n")
	if output == "" {
		output = "(empty receipts wait result)"
	}
	b.WriteString(m.th.paint(m.th.brand, output))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.needsYou, "enter close | esc close"))
	return b.String()
}

// messageWaitResultOverlayView renders an AMQ send --wait-for result after
// confirm.
func (m NOCModel) messageWaitResultOverlayView() string {
	p := m.messageWaitResult
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.needsYou, "MESSAGE WAIT RESULT"))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	if handle := strings.TrimSpace(p.result.Handle); handle != "" {
		b.WriteString(m.th.paint(m.th.dim, "agent: "+handle))
		b.WriteString("\n")
	}
	if timeout := strings.TrimSpace(p.result.Timeout); timeout != "" {
		b.WriteString(m.th.paint(m.th.dim, "timeout: "+timeout))
		b.WriteString("\n")
	}
	if preview := strings.TrimSpace(p.preview); preview != "" {
		b.WriteString(m.th.paint(m.th.dim, "ran: "+preview))
		b.WriteString("\n")
	}
	b.WriteString(m.th.paint(m.th.dim, "output:"))
	b.WriteString("\n")
	output := strings.TrimRight(p.result.Output, "\n")
	if output == "" {
		output = "(empty message wait result)"
	}
	b.WriteString(m.th.paint(m.th.brand, output))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.needsYou, "enter close | esc close"))
	return b.String()
}

// threadContextResultOverlayView renders the AMQ thread result after a
// read-only thread-context inspection completes.
func (m NOCModel) threadContextResultOverlayView() string {
	p := m.threadContextResult
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.needsYou, "THREAD CONTEXT"))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	if thread := strings.TrimSpace(p.result.Thread); thread != "" {
		b.WriteString(m.th.paint(m.th.dim, "thread: "+thread))
		b.WriteString("\n")
	}
	if subject := strings.TrimSpace(p.result.Subject); subject != "" {
		b.WriteString(m.th.paint(m.th.dim, "subject: "+subject))
		b.WriteString("\n")
	}
	if preview := strings.TrimSpace(p.preview); preview != "" {
		b.WriteString(m.th.paint(m.th.dim, "ran: "+preview))
		b.WriteString("\n")
	}
	b.WriteString(m.th.paint(m.th.dim, "output:"))
	b.WriteString("\n")
	output := strings.TrimRight(p.result.Output, "\n")
	if output == "" {
		output = "(no thread messages)"
	}
	b.WriteString(m.th.paint(m.th.brand, output))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.needsYou, "enter close | esc close"))
	return b.String()
}

// amqOpsResultOverlayView renders the AMQ doctor --ops result after a read-only
// bus-health inspection completes.
func (m NOCModel) amqOpsResultOverlayView() string {
	p := m.amqOpsResult
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.needsYou, "AMQ OPS"))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	if root := strings.TrimSpace(p.result.Root); root != "" {
		b.WriteString(m.th.paint(m.th.dim, "root: "+root))
		b.WriteString("\n")
	}
	if preview := strings.TrimSpace(p.preview); preview != "" {
		b.WriteString(m.th.paint(m.th.dim, "ran: "+preview))
		b.WriteString("\n")
	}
	b.WriteString(m.th.paint(m.th.dim, "output:"))
	b.WriteString("\n")
	output := strings.TrimRight(p.result.Output, "\n")
	if output == "" {
		output = "(no AMQ ops output)"
	}
	b.WriteString(m.th.paint(m.th.brand, output))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.needsYou, "enter close | esc close"))
	return b.String()
}

// presenceResultOverlayView renders the AMQ presence list result after a
// read-only session presence inspection completes.
func (m NOCModel) presenceResultOverlayView() string {
	p := m.presenceResult
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.needsYou, "PRESENCE"))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	if root := strings.TrimSpace(p.result.Root); root != "" {
		b.WriteString(m.th.paint(m.th.dim, "root: "+root))
		b.WriteString("\n")
	}
	if preview := strings.TrimSpace(p.preview); preview != "" {
		b.WriteString(m.th.paint(m.th.dim, "ran: "+preview))
		b.WriteString("\n")
	}
	b.WriteString(m.th.paint(m.th.dim, "output:"))
	b.WriteString("\n")
	output := strings.TrimRight(p.result.Output, "\n")
	if output == "" {
		output = "(no presence records)"
	}
	b.WriteString(m.th.paint(m.th.brand, output))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.needsYou, "enter close | esc close"))
	return b.String()
}

// amqWhoResultOverlayView renders the AMQ who project inventory after a
// read-only project base-root inspection completes.
func (m NOCModel) amqWhoResultOverlayView() string {
	p := m.amqWhoResult
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.needsYou, "AMQ WHO"))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	if root := strings.TrimSpace(p.result.Root); root != "" {
		b.WriteString(m.th.paint(m.th.dim, "root: "+root))
		b.WriteString("\n")
	}
	if preview := strings.TrimSpace(p.preview); preview != "" {
		b.WriteString(m.th.paint(m.th.dim, "ran: "+preview))
		b.WriteString("\n")
	}
	b.WriteString(m.th.paint(m.th.dim, "output:"))
	b.WriteString("\n")
	output := strings.TrimRight(p.result.Output, "\n")
	if output == "" {
		output = "(no sessions found)"
	}
	b.WriteString(m.th.paint(m.th.brand, output))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.needsYou, "enter close | esc close"))
	return b.String()
}

// amqEnvResultOverlayView renders the AMQ env project environment after a
// read-only project base-root inspection completes.
func (m NOCModel) amqEnvResultOverlayView() string {
	p := m.amqEnvResult
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.needsYou, "AMQ ENV"))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	if root := strings.TrimSpace(p.result.Root); root != "" {
		b.WriteString(m.th.paint(m.th.dim, "root: "+root))
		b.WriteString("\n")
	}
	if preview := strings.TrimSpace(p.preview); preview != "" {
		b.WriteString(m.th.paint(m.th.dim, "ran: "+preview))
		b.WriteString("\n")
	}
	b.WriteString(m.th.paint(m.th.dim, "output:"))
	b.WriteString("\n")
	output := strings.TrimRight(p.result.Output, "\n")
	if output == "" {
		output = "(no AMQ env output)"
	}
	b.WriteString(m.th.paint(m.th.brand, output))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.needsYou, "enter close | esc close"))
	return b.String()
}

// amqCleanupResultOverlayView renders the AMQ cleanup result after a confirmed
// session tmp-file cleanup completes.
func (m NOCModel) amqCleanupResultOverlayView() string {
	p := m.amqCleanupResult
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.needsYou, "AMQ CLEANUP"))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	if root := strings.TrimSpace(p.result.Root); root != "" {
		b.WriteString(m.th.paint(m.th.dim, "root: "+root))
		b.WriteString("\n")
	}
	if age := strings.TrimSpace(p.result.TmpOlderThan); age != "" {
		b.WriteString(m.th.paint(m.th.dim, "tmp older than: "+age))
		b.WriteString("\n")
	}
	if preview := strings.TrimSpace(p.preview); preview != "" {
		b.WriteString(m.th.paint(m.th.dim, "ran: "+preview))
		b.WriteString("\n")
	}
	b.WriteString(m.th.paint(m.th.dim, "output:"))
	b.WriteString("\n")
	output := strings.TrimRight(p.result.Output, "\n")
	if output == "" {
		output = "(no AMQ cleanup output)"
	}
	b.WriteString(m.th.paint(m.th.brand, output))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.needsYou, "enter close | esc close"))
	return b.String()
}

// projectDoctorResultOverlayView renders an amq-squad doctor report after a
// read-only project-health inspection completes.
func (m NOCModel) projectDoctorResultOverlayView() string {
	p := m.projectDoctorResult
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.needsYou, "PROJECT DOCTOR"))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	if dir := strings.TrimSpace(p.result.ProjectDir); dir != "" {
		b.WriteString(m.th.paint(m.th.dim, "project: "+dir))
		b.WriteString("\n")
	}
	if preview := strings.TrimSpace(p.preview); preview != "" {
		b.WriteString(m.th.paint(m.th.dim, "ran: "+preview))
		b.WriteString("\n")
	}
	b.WriteString(m.th.paint(m.th.dim, "output:"))
	b.WriteString("\n")
	output := strings.TrimRight(p.result.Output, "\n")
	if output == "" {
		output = "(no doctor output)"
	}
	b.WriteString(m.th.paint(m.th.brand, output))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.needsYou, "enter close | esc close"))
	return b.String()
}

// projectHistoryResultOverlayView renders launch-history records after a
// read-only project-history inspection completes.
func (m NOCModel) projectHistoryResultOverlayView() string {
	p := m.projectHistoryResult
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.needsYou, "HISTORY"))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	if dir := strings.TrimSpace(p.result.ProjectDir); dir != "" {
		b.WriteString(m.th.paint(m.th.dim, "project: "+dir))
		b.WriteString("\n")
	}
	if preview := strings.TrimSpace(p.preview); preview != "" {
		b.WriteString(m.th.paint(m.th.dim, "ran: "+preview))
		b.WriteString("\n")
	}
	b.WriteString(m.th.paint(m.th.dim, "output:"))
	b.WriteString("\n")
	output := strings.TrimRight(p.result.Output, "\n")
	if output == "" {
		output = "(no history output)"
	}
	b.WriteString(m.th.paint(m.th.brand, output))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.needsYou, "enter close | esc close"))
	return b.String()
}

// teamRulesResultOverlayView renders team-rules.md after a read-only
// project team-rules inspection completes.
func (m NOCModel) teamRulesResultOverlayView() string {
	p := m.teamRulesResult
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.needsYou, "TEAM RULES"))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	if dir := strings.TrimSpace(p.result.ProjectDir); dir != "" {
		b.WriteString(m.th.paint(m.th.dim, "project: "+dir))
		b.WriteString("\n")
	}
	if path := strings.TrimSpace(p.result.Path); path != "" {
		b.WriteString(m.th.paint(m.th.dim, "path: "+path))
		b.WriteString("\n")
	}
	if preview := strings.TrimSpace(p.preview); preview != "" {
		b.WriteString(m.th.paint(m.th.dim, "ran: "+preview))
		b.WriteString("\n")
	}
	b.WriteString(m.th.paint(m.th.dim, "content:"))
	b.WriteString("\n")
	content := strings.TrimRight(p.result.Content, "\n")
	if content == "" {
		content = "(no team rules content)"
	}
	b.WriteString(m.th.paint(m.th.brand, content))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.needsYou, "enter close | esc close"))
	return b.String()
}

// projectResumePlanResultOverlayView renders a recovery plan after a read-only
// project resume inspection completes.
func (m NOCModel) projectResumePlanResultOverlayView() string {
	p := m.projectResumePlanResult
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.needsYou, "RESUME PLAN"))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	if dir := strings.TrimSpace(p.result.ProjectDir); dir != "" {
		b.WriteString(m.th.paint(m.th.dim, "project: "+dir))
		b.WriteString("\n")
	}
	if profile := strings.TrimSpace(p.result.Profile); profile != "" {
		b.WriteString(m.th.paint(m.th.dim, "profile: "+profile))
		b.WriteString("\n")
	}
	if preview := strings.TrimSpace(p.preview); preview != "" {
		b.WriteString(m.th.paint(m.th.dim, "ran: "+preview))
		b.WriteString("\n")
	}
	b.WriteString(m.th.paint(m.th.dim, "output:"))
	b.WriteString("\n")
	output := strings.TrimRight(p.result.Output, "\n")
	if output == "" {
		output = "(no resume plan output)"
	}
	b.WriteString(m.th.paint(m.th.brand, output))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.needsYou, "enter close | esc close"))
	return b.String()
}

// forkPlanResultOverlayView renders a branch plan after a read-only fork
// inspection completes.
func (m NOCModel) forkPlanResultOverlayView() string {
	p := m.forkPlanResult
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.needsYou, "FORK PLAN"))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	if dir := strings.TrimSpace(p.result.ProjectDir); dir != "" {
		b.WriteString(m.th.paint(m.th.dim, "project: "+dir))
		b.WriteString("\n")
	}
	if profile := strings.TrimSpace(p.result.Profile); profile != "" {
		b.WriteString(m.th.paint(m.th.dim, "profile: "+profile))
		b.WriteString("\n")
	}
	if from := strings.TrimSpace(p.result.FromSession); from != "" {
		b.WriteString(m.th.paint(m.th.dim, "from: "+from))
		b.WriteString("\n")
	}
	if to := strings.TrimSpace(p.result.ToSession); to != "" {
		b.WriteString(m.th.paint(m.th.dim, "to: "+to))
		b.WriteString("\n")
	}
	if preview := strings.TrimSpace(p.preview); preview != "" {
		b.WriteString(m.th.paint(m.th.dim, "ran: "+preview))
		b.WriteString("\n")
	}
	b.WriteString(m.th.paint(m.th.dim, "output:"))
	b.WriteString("\n")
	output := strings.TrimRight(p.result.Output, "\n")
	if output == "" {
		output = "(no fork plan output)"
	}
	b.WriteString(m.th.paint(m.th.brand, output))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.needsYou, "enter close | esc close"))
	return b.String()
}

// briefResultOverlayView renders a workstream brief after a read-only session
// brief inspection completes.
func (m NOCModel) briefResultOverlayView() string {
	p := m.briefResult
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.needsYou, "BRIEF"))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	if dir := strings.TrimSpace(p.result.ProjectDir); dir != "" {
		b.WriteString(m.th.paint(m.th.dim, "project: "+dir))
		b.WriteString("\n")
	}
	if session := strings.TrimSpace(p.result.Session); session != "" {
		b.WriteString(m.th.paint(m.th.dim, "session: "+session))
		b.WriteString("\n")
	}
	if path := strings.TrimSpace(p.result.Path); path != "" {
		b.WriteString(m.th.paint(m.th.dim, "path: "+path))
		b.WriteString("\n")
	}
	if kind := strings.TrimSpace(p.result.Kind); kind != "" {
		b.WriteString(m.th.paint(m.th.dim, "kind: "+kind))
		b.WriteString("\n")
	}
	if preview := strings.TrimSpace(p.preview); preview != "" {
		b.WriteString(m.th.paint(m.th.dim, "ran: "+preview))
		b.WriteString("\n")
	}
	b.WriteString(m.th.paint(m.th.dim, "content:"))
	b.WriteString("\n")
	content := strings.TrimRight(p.result.Content, "\n")
	if content == "" {
		content = "(no brief)"
	}
	b.WriteString(m.th.paint(m.th.brand, content))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.needsYou, "enter close | esc close"))
	return b.String()
}

// statusResultOverlayView renders an amq-squad status report after a read-only
// project/session status inspection completes.
func (m NOCModel) statusResultOverlayView() string {
	p := m.statusResult
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.needsYou, "STATUS"))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	if dir := strings.TrimSpace(p.result.ProjectDir); dir != "" {
		b.WriteString(m.th.paint(m.th.dim, "project: "+dir))
		b.WriteString("\n")
	}
	if session := strings.TrimSpace(p.result.Session); session != "" {
		b.WriteString(m.th.paint(m.th.dim, "session: "+session))
		b.WriteString("\n")
	}
	if profile := strings.TrimSpace(p.result.Profile); profile != "" {
		b.WriteString(m.th.paint(m.th.dim, "profile: "+profile))
		b.WriteString("\n")
	}
	if preview := strings.TrimSpace(p.preview); preview != "" {
		b.WriteString(m.th.paint(m.th.dim, "ran: "+preview))
		b.WriteString("\n")
	}
	b.WriteString(m.th.paint(m.th.dim, "output:"))
	b.WriteString("\n")
	output := strings.TrimRight(p.result.Output, "\n")
	if output == "" {
		output = "(no status output)"
	}
	b.WriteString(m.th.paint(m.th.brand, output))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.needsYou, "enter close | esc close"))
	return b.String()
}

// threadsResultOverlayView renders an amq-squad threads report after a
// read-only session threads inspection completes.
func (m NOCModel) threadsResultOverlayView() string {
	p := m.threadsResult
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.needsYou, "THREADS"))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	if dir := strings.TrimSpace(p.result.ProjectDir); dir != "" {
		b.WriteString(m.th.paint(m.th.dim, "project: "+dir))
		b.WriteString("\n")
	}
	if session := strings.TrimSpace(p.result.Session); session != "" {
		b.WriteString(m.th.paint(m.th.dim, "session: "+session))
		b.WriteString("\n")
	}
	if preview := strings.TrimSpace(p.preview); preview != "" {
		b.WriteString(m.th.paint(m.th.dim, "ran: "+preview))
		b.WriteString("\n")
	}
	b.WriteString(m.th.paint(m.th.dim, "output:"))
	b.WriteString("\n")
	output := strings.TrimRight(p.result.Output, "\n")
	if output == "" {
		output = "(no threads output)"
	}
	b.WriteString(m.th.paint(m.th.brand, output))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.needsYou, "enter close | esc close"))
	return b.String()
}

func (m NOCModel) roleMarketOverlayView() string {
	p := m.roleMarket
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.needsYou, "ROLE MARKET"))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	if p != nil {
		if project := strings.TrimSpace(p.project); project != "" {
			b.WriteString(m.th.paint(m.th.dim, "project: "+project))
			b.WriteString("\n")
		}
		if dir := strings.TrimSpace(p.projectDir); dir != "" {
			b.WriteString(m.th.paint(m.th.dim, "team-home: "+dir))
			b.WriteString("\n")
		}
	}
	b.WriteString(m.th.paint(m.th.dim, "ran: amq-squad roles"))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.dim, "select roles with IDs, numbers, all, or role=binary overrides"))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.dim, padRight("NUM", 4)+" "+padRight("ROLE", 14)+" "+padRight("CLI", 7)+" PROFILE"))
	b.WriteString("\n")
	for i, role := range catalog.All() {
		line := padRight(itoaPalette(i+1), 4) + " " +
			padRight(role.ID, 14) + " " +
			padRight(role.PreferredBinary, 7) + " " +
			role.Profile
		b.WriteString(m.th.paint(m.th.brand, line))
		b.WriteString("\n")
	}
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.needsYou, "enter close | esc close"))
	return b.String()
}

func (m NOCModel) teamProfilesOverlayView() string {
	p := m.teamProfiles
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.needsYou, "TEAM PROFILES"))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	if p != nil {
		if project := strings.TrimSpace(p.project); project != "" {
			b.WriteString(m.th.paint(m.th.dim, "project: "+project))
			b.WriteString("\n")
		}
		if dir := strings.TrimSpace(p.projectDir); dir != "" {
			b.WriteString(m.th.paint(m.th.dim, "team-home: "+dir))
			b.WriteString("\n")
			b.WriteString(m.th.paint(m.th.dim, "ran: "+squadCommandToken()+" team profiles --project "+shellToken(dir)))
			b.WriteString("\n")
		} else {
			b.WriteString(m.th.paint(m.th.dim, "ran: "+squadCommandToken()+" team profiles"))
			b.WriteString("\n")
		}
	}
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	profiles := []string{}
	if p != nil {
		profiles = append(profiles, p.profiles...)
	}
	if len(profiles) == 0 {
		b.WriteString(m.th.paint(m.th.dim, "(no configured profiles)"))
		b.WriteString("\n")
		b.WriteString(m.th.paint(m.th.dim, "press T to create a team profile"))
		b.WriteString("\n")
	} else {
		b.WriteString(m.th.paint(m.th.dim, padRight("PROFILE", 14)+" "+padRight("TYPE", 8)+" PATH"))
		b.WriteString("\n")
		for _, profile := range profiles {
			kind := "named"
			path := ".amq-squad/teams/" + profile + ".json"
			if profile == team.DefaultProfile {
				kind = "default"
				path = ".amq-squad/team.json"
			}
			line := padRight(profile, 14) + " " + padRight(kind, 8) + " " + path
			b.WriteString(m.th.paint(m.th.brand, line))
			b.WriteString("\n")
		}
		b.WriteString(m.th.paint(m.th.dim, "press N to start a session; press T to add a profile"))
		b.WriteString("\n")
	}
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.needsYou, "enter close | esc close"))
	return b.String()
}

// inputOverlayView renders the body/subject editor. It shows which field is
// being typed and the running buffer, plus the cancel affordance. It is NOT a
// confirm step — the preview comes after, on the confirm overlay.
func (m NOCModel) inputOverlayView() string {
	in := m.input
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.brand, in.kind.label()))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	cursor := "▏"
	if m.colorMode == ColorAscii {
		cursor = "_"
	}
	field := "body"
	val := in.body
	if inputEditingSubject(in) {
		field = "subject"
		val = in.subject
		if in.kind == ctlMessageWait {
			field = "timeout"
		} else if in.kind == ctlDeleteTeam {
			field = "choice"
		} else if in.kind == ctlNewSession || in.kind == ctlNewTeam || in.kind == ctlSyncPointers || lifecycleControlKind(in.kind) {
			field = "profile"
		}
	} else if in.kind == ctlDeny {
		field = "reason"
	} else if in.kind == ctlNewSession {
		field = "session"
	} else if in.kind == ctlNewTeam {
		field = "roles"
	} else if in.kind == ctlForkPlan {
		field = "target session"
	} else if in.kind == ctlDLQRead || in.kind == ctlDLQRetry {
		field = "dlq id"
	} else if in.kind == ctlDLQPurge {
		field = "older-than"
	} else if in.kind == ctlReceiptsWait {
		field = "receipt"
	} else if in.kind == ctlDeleteTeam {
		if strings.TrimSpace(in.subject) == "all-team-config" {
			field = "project name"
		} else {
			field = "profile"
		}
	}
	// On the broadcast body stage, show the captured subject for context.
	if strings.TrimSpace(in.hint) != "" {
		b.WriteString(m.th.paint(m.th.dim, in.hint))
		b.WriteString("\n")
	}
	if in.kind == ctlBroadcast && in.stage == 1 {
		b.WriteString(m.th.paint(m.th.dim, "subject: "+in.subject))
		b.WriteString("\n")
	}
	if in.kind == ctlNewSession && in.stage == 1 && strings.TrimSpace(in.subject) != "" {
		b.WriteString(m.th.paint(m.th.dim, "profile: "+in.subject))
		b.WriteString("\n")
	}
	if in.kind == ctlNewTeam && in.stage == 1 && strings.TrimSpace(in.subject) != "" {
		b.WriteString(m.th.paint(m.th.dim, "profile: "+in.subject))
		b.WriteString("\n")
	}
	if in.kind == ctlDeleteTeam && in.stage == 1 && strings.TrimSpace(in.subject) != "" {
		b.WriteString(m.th.paint(m.th.dim, "delete: "+deleteModeLabel(in.subject)))
		b.WriteString("\n")
	}
	b.WriteString(m.th.paint(m.th.atRisk, field+": "+val+cursor))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	next := "enter preview · esc cancel"
	if inputEditingSubject(in) {
		next = "enter next · esc cancel"
	}
	if m.colorMode == ColorAscii {
		next = strings.ReplaceAll(next, "·", "|")
	}
	b.WriteString(m.th.paint(m.th.dim, next))
	return b.String()
}

// controlFooterKeys is the additive control-key legend appended to the footer.
// controlFooterKeys renders the control legend for the footer from the
// single-source keymap (noc_keymap.go) so it cannot drift from the router.
func controlFooterKeys(ascii bool) string {
	if ascii {
		return nocFooterLegend(keyGroupAction, true, " | ")
	}
	return nocFooterLegend(keyGroupAction, false, " · ")
}

// controlHelpLines renders the CONTROL section of the help overlay from the same
// single-source keymap, for the same reason.
func controlHelpLines() []string {
	return nocControlHelpLines()
}

func validateNOCSessionName(name string) error {
	if strings.TrimSpace(name) == "" {
		return errString("session name cannot be empty")
	}
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			continue
		}
		return errString("session names allow lowercase a-z, 0-9, - and _ only")
	}
	return nil
}

func validateNOCNewSessionName(name string, project noc.ProjectSnapshot) error {
	seen := map[string]bool{}
	for _, sess := range project.Snap.Sessions {
		seen[strings.TrimSpace(sess.Name)] = true
	}
	for _, sessionName := range project.SessionNames {
		seen[strings.TrimSpace(sessionName)] = true
	}
	if seen[name] {
		return errString("session " + name + " already exists; select it and press R to resume, X to restart, or choose a new name")
	}
	return nil
}

func parseNOCNewSessionInput(raw string) (session, seedFrom string, err error) {
	parts := strings.Fields(strings.TrimSpace(raw))
	if len(parts) == 0 {
		return "", "", errString("session name cannot be empty")
	}
	session = parts[0]
	for _, part := range parts[1:] {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			return "", "", errString("unexpected new-session option " + part + "; use seed-from=<ref>")
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if key != "seed-from" && key != "seed" {
			return "", "", errString("unknown new-session option " + key + "; use seed-from=<ref>")
		}
		if seedFrom != "" {
			return "", "", errString("seed-from specified more than once")
		}
		if err := validateNOCSeedFrom(value); err != nil {
			return "", "", err
		}
		seedFrom = value
	}
	return session, seedFrom, nil
}

func validateNOCSeedFrom(seedFrom string) error {
	seedFrom = strings.TrimSpace(seedFrom)
	if seedFrom == "" {
		return errString("seed-from cannot be empty")
	}
	kind, rest, ok := strings.Cut(seedFrom, ":")
	if !ok || strings.TrimSpace(kind) == "" || strings.TrimSpace(rest) == "" {
		return errString("invalid seed-from " + seedFrom + "; use file:<path>, issue:<n>, or gh:owner/repo#<n>")
	}
	switch kind {
	case "file":
		return nil
	case "issue":
		if isPositiveDecimal(rest) {
			return nil
		}
		return errString("invalid seed-from " + seedFrom + "; issue seeds must look like issue:<n>")
	case "gh":
		if looksLikeGitHubIssueSeed(rest) {
			return nil
		}
		return errString("invalid seed-from " + seedFrom + "; GitHub seeds must look like gh:owner/repo#<n>")
	default:
		return errString("invalid seed-from " + seedFrom + "; use file:<path>, issue:<n>, or gh:owner/repo#<n>")
	}
}

func parseNOCBriefSeedInput(raw string) (seedFrom string, force bool, err error) {
	parts := strings.Fields(strings.TrimSpace(raw))
	if len(parts) == 0 {
		return "", false, errString("seed source cannot be empty")
	}
	for _, part := range parts {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			if seedFrom != "" {
				return "", false, errString("seed source specified more than once")
			}
			seedFrom = strings.TrimSpace(part)
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch key {
		case "seed-from", "seed":
			if seedFrom != "" {
				return "", false, errString("seed-from specified more than once")
			}
			seedFrom = value
		case "force":
			parsed, err := parseNOCBool(value)
			if err != nil {
				return "", false, err
			}
			force = parsed
		default:
			return "", false, errString("unknown brief seed option " + key + "; use seed-from=<ref> or force=true")
		}
	}
	if seedFrom == "" {
		return "", false, errString("seed source cannot be empty")
	}
	return seedFrom, force, nil
}

func validateNOCBriefSeedSource(seedFrom string) error {
	return validateNOCSeedFrom(seedFrom)
}

func parseNOCBool(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "1", "yes", "y", "on":
		return true, nil
	case "false", "0", "no", "n", "off":
		return false, nil
	default:
		return false, errString("invalid boolean " + raw + "; use true or false")
	}
}

func validateNOCDLQID(raw string) error {
	id := strings.TrimSpace(raw)
	if id == "" {
		return errString("DLQ id cannot be empty")
	}
	if strings.HasPrefix(id, ".") || filepath.Base(id) != id || strings.ContainsAny(id, `/\`) {
		return errString("invalid DLQ id " + id + "; use the ID from amq dlq list, not a path")
	}
	return nil
}

func validateNOCThreadID(raw string) error {
	id := strings.TrimSpace(raw)
	if id == "" {
		return errString("thread id cannot be empty")
	}
	if strings.ContainsAny(id, " \t\r\n") {
		return errString("invalid thread id " + id + "; thread ids cannot contain whitespace")
	}
	return nil
}

func validateNOCDLQPurgeOlderThan(raw string) error {
	olderThan := strings.TrimSpace(raw)
	if olderThan == "" {
		return errString("DLQ purge age cannot be empty")
	}
	dur, err := time.ParseDuration(olderThan)
	if err != nil || dur <= 0 {
		return errString("invalid DLQ purge age " + olderThan + "; use a positive duration like 24h or 168h")
	}
	return nil
}

func validateNOCAMQCleanupTmpOlderThan(raw string) error {
	olderThan := strings.TrimSpace(raw)
	if olderThan == "" {
		return errString("AMQ cleanup tmp age cannot be empty")
	}
	dur, err := time.ParseDuration(olderThan)
	if err != nil || dur <= 0 {
		return errString("invalid AMQ cleanup tmp age " + olderThan + "; use a positive duration like 36h or 168h")
	}
	return nil
}

type nocReceiptsWaitSpec struct {
	MsgID   string
	Stage   string
	Timeout string
}

func validateNOCReceiptsWaitInput(raw string) error {
	_, err := parseNOCReceiptsWaitInput(raw)
	return err
}

func parseNOCReceiptsWaitInput(raw string) (nocReceiptsWaitSpec, error) {
	parts := strings.Fields(strings.TrimSpace(raw))
	if len(parts) == 0 {
		return nocReceiptsWaitSpec{}, errString("message ID cannot be empty")
	}
	spec := nocReceiptsWaitSpec{MsgID: parts[0], Stage: "drained", Timeout: "60s"}
	seen := map[string]bool{}
	for _, part := range parts[1:] {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			return nocReceiptsWaitSpec{}, errString("unexpected receipts-wait option " + part + "; use stage=<drained|dlq> or timeout=<duration>")
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if seen[key] {
			return nocReceiptsWaitSpec{}, errString(key + " specified more than once")
		}
		seen[key] = true
		switch key {
		case "stage":
			if err := validateNOCReceiptStage(value); err != nil {
				return nocReceiptsWaitSpec{}, err
			}
			spec.Stage = value
		case "timeout":
			if err := validateNOCReceiptsWaitTimeout(value); err != nil {
				return nocReceiptsWaitSpec{}, err
			}
			spec.Timeout = value
		default:
			return nocReceiptsWaitSpec{}, errString("unknown receipts-wait option " + key + "; use stage=<drained|dlq> or timeout=<duration>")
		}
	}
	if strings.TrimSpace(spec.MsgID) == "" {
		return nocReceiptsWaitSpec{}, errString("message ID cannot be empty")
	}
	return spec, nil
}

func validateNOCReceiptStage(stage string) error {
	stage = strings.TrimSpace(stage)
	if stage == "drained" || stage == "dlq" {
		return nil
	}
	return errString("stage must be drained or dlq")
}

func validateNOCReceiptsWaitTimeout(raw string) error {
	timeout := strings.TrimSpace(raw)
	if timeout == "" {
		return errString("receipts wait timeout cannot be empty")
	}
	dur, err := time.ParseDuration(timeout)
	if err != nil || dur < 0 {
		return errString("invalid receipts wait timeout " + timeout + "; use a non-negative duration like 60s or 5m")
	}
	return nil
}

func validateNOCMessageWaitTimeout(raw string) error {
	timeout := strings.TrimSpace(raw)
	if timeout == "" {
		return errString("message wait timeout cannot be empty")
	}
	dur, err := time.ParseDuration(timeout)
	if err != nil || dur < 0 {
		return errString("invalid message wait timeout " + timeout + "; use a non-negative duration like 60s or 5m")
	}
	return nil
}

func isPositiveDecimal(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return strings.TrimLeft(s, "0") != ""
}

func looksLikeGitHubIssueSeed(s string) bool {
	repo, number, ok := strings.Cut(strings.TrimSpace(s), "#")
	if !ok || !isPositiveDecimal(number) {
		return false
	}
	owner, name, ok := strings.Cut(repo, "/")
	return ok && strings.TrimSpace(owner) != "" && strings.TrimSpace(name) != ""
}

func validateNOCProfileName(name string) error {
	if strings.TrimSpace(name) == "" {
		return errString("profile name cannot be empty")
	}
	if name == team.DefaultProfile {
		return errString("profile default already exists; choose a named profile")
	}
	return team.ValidateProfileName(name)
}

func validateNOCNewTeamProfile(name string, project noc.ProjectSnapshot) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errString("profile name cannot be empty")
	}
	if name == team.DefaultProfile {
		if project.DefaultTeam {
			return errString("profile default already exists; choose a named profile")
		}
		return nil
	}
	if err := team.ValidateProfileName(name); err != nil {
		return err
	}
	for _, existing := range project.Profiles {
		if strings.TrimSpace(existing) == name {
			return errString("profile " + name + " already exists")
		}
	}
	return nil
}

type nocTeamSpec struct {
	Roles   string
	Binary  string
	Session string
}

func nocTeamSpecHint() string {
	return strings.Join([]string{
		"Create an amq-squad team profile for this project; enter previews the exact command before anything runs.",
		"built-in roles: cto,fullstack,qa or role numbers like 2,9; use all for every built-in role",
		"optional: override built-ins with qa=codex; add session=issue-96 for the initial workstream",
	}, "\n")
}

func nocNewTeamProfileHint(profiles []string) string {
	lines := []string{
		"Create another amq-squad profile for this project; enter the new profile name, then choose roles.",
	}
	if len(profiles) > 0 {
		lines = append(lines, "existing profiles: "+strings.Join(profiles, ", "))
	}
	return strings.Join(lines, "\n")
}

func parseNOCTeamSpec(raw string) (nocTeamSpec, error) {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	roles := make([]string, 0, len(parts))
	seen := map[string]bool{}
	binaryByRole := map[string]string{}
	session := ""
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		rolePart, binary, hasBinary := strings.Cut(part, "=")
		selection := strings.TrimSpace(rolePart)
		if selection == "" {
			return nocTeamSpec{}, errString("role cannot be empty")
		}
		if hasBinary && strings.EqualFold(selection, "session") {
			if session != "" {
				return nocTeamSpec{}, errString("session specified more than once")
			}
			session = strings.TrimSpace(binary)
			if err := validateNOCSessionName(session); err != nil {
				return nocTeamSpec{}, err
			}
			continue
		}
		if hasBinary {
			binary = strings.TrimSpace(binary)
			if err := team.ValidateDisplayValue("binary", binary); err != nil {
				return nocTeamSpec{}, err
			}
		}
		resolved, err := catalog.ResolveSelection(selection)
		if err != nil {
			return nocTeamSpec{}, err
		}
		for _, role := range resolved {
			if hasBinary {
				binaryByRole[role] = binary
			}
			if !seen[role] {
				seen[role] = true
				roles = append(roles, role)
			}
		}
	}
	if len(roles) == 0 {
		return nocTeamSpec{}, errString("enter at least one role, for example cto,fullstack")
	}
	binaryParts := make([]string, 0, len(binaryByRole))
	for _, role := range roles {
		if binary := binaryByRole[role]; binary != "" {
			binaryParts = append(binaryParts, role+"="+binary)
		}
	}
	return nocTeamSpec{Roles: strings.Join(roles, ","), Binary: strings.Join(binaryParts, ","), Session: session}, nil
}

type errString string

func (e errString) Error() string { return string(e) }
