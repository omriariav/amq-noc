package noc

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-noc/internal/state"
)

// SessionCorrelation is the read-only t4 surface joining amq-squad task state,
// AMQ worker reports, runtime/liveness facts, and release evidence. It never
// mutates task/runtime state and never treats message bodies as authorization.
type SessionCorrelation struct {
	Status        string              `json:"status,omitempty"`
	Summary       string              `json:"summary,omitempty"`
	Tasks         TaskStoreSnapshot   `json:"tasks,omitempty"`
	WorkerReports []WorkerReport      `json:"worker_reports,omitempty"`
	Mismatches    []CorrelationSignal `json:"mismatches,omitempty"`
	Runtime       RuntimeBoard        `json:"runtime,omitempty"`
	Evidence      EvidenceSummary     `json:"evidence,omitempty"`
}

type TaskStoreSnapshot struct {
	Status string     `json:"status,omitempty"`
	Error  string     `json:"error,omitempty"`
	Tasks  []TaskItem `json:"tasks,omitempty"`
}

type TaskItem struct {
	ID          string     `json:"id,omitempty"`
	Title       string     `json:"title,omitempty"`
	Description string     `json:"description,omitempty"`
	Status      string     `json:"status,omitempty"`
	AssignedTo  string     `json:"assigned_to,omitempty"`
	DependsOn   []string   `json:"depends_on,omitempty"`
	CreatedAt   *time.Time `json:"created_at,omitempty"`
	UpdatedAt   *time.Time `json:"updated_at,omitempty"`
	Evidence    string     `json:"evidence,omitempty"`
}

type WorkerReport struct {
	Type      string     `json:"type,omitempty"`
	From      string     `json:"from,omitempty"`
	Thread    string     `json:"thread,omitempty"`
	Subject   string     `json:"subject,omitempty"`
	At        *time.Time `json:"at,omitempty"`
	Kind      string     `json:"kind,omitempty"`
	Preview   string     `json:"preview,omitempty"`
	Decision  bool       `json:"decision,omitempty"`
	Review    bool       `json:"review,omitempty"`
	Done      bool       `json:"done,omitempty"`
	Blocked   bool       `json:"blocked,omitempty"`
	NeedsLead bool       `json:"needs_lead,omitempty"`
}

type CorrelationSignal struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
	TaskID string `json:"task_id,omitempty"`
	Thread string `json:"thread,omitempty"`
}

type RuntimeBoard struct {
	Status         string               `json:"status,omitempty"`
	Source         string               `json:"source,omitempty"`
	Error          string               `json:"error,omitempty"`
	Namespace      NamespaceRef         `json:"namespace,omitempty"`
	GoalBinding    GoalBinding          `json:"goal_binding,omitempty"`
	Topology       *RuntimeTopology     `json:"topology,omitempty"`
	LeadDown       bool                 `json:"lead_down,omitempty"`
	WorkersTotal   int                  `json:"workers_total,omitempty"`
	WorkersLive    int                  `json:"workers_live,omitempty"`
	WorkersAtRisk  int                  `json:"workers_at_risk,omitempty"`
	ExternalLead   bool                 `json:"external_lead,omitempty"`
	ExternalDetail string               `json:"external_detail,omitempty"`
	Rows           []RuntimeWorkerState `json:"rows,omitempty"`
}

type RuntimeWorkerState struct {
	Handle      string `json:"handle,omitempty"`
	Role        string `json:"role,omitempty"`
	Status      string `json:"status,omitempty"`
	RecordState string `json:"record_state,omitempty"`
	Source      string `json:"source,omitempty"`
	Liveness    string `json:"liveness,omitempty"`
	WakeHealth  string `json:"wake_health,omitempty"`
	IsLead      bool   `json:"is_lead,omitempty"`
	PaneAlive   bool   `json:"pane_alive,omitempty"`
	NextAction  string `json:"next_action,omitempty"`
}

type EvidenceSummary struct {
	Status             string   `json:"status,omitempty"`
	PRURLs             []string `json:"pr_urls,omitempty"`
	IssueRefs          []string `json:"issue_refs,omitempty"`
	HeadSHAs           []string `json:"head_shas,omitempty"`
	LocalTests         []string `json:"local_tests,omitempty"`
	CITests            []string `json:"ci_tests,omitempty"`
	ReviewThreads      []string `json:"review_threads,omitempty"`
	DecisionThreads    []string `json:"decision_threads,omitempty"`
	MergeReleaseGates  []string `json:"merge_release_gates,omitempty"`
	TaskEvidence       []string `json:"task_evidence,omitempty"`
	Blockers           []string `json:"blockers,omitempty"`
	HumanGateRequired  bool     `json:"human_gate_required,omitempty"`
	CapabilityGap      string   `json:"capability_gap,omitempty"`
	VerifyMergeVerdict string   `json:"verify_merge_verdict,omitempty"`
}

type taskCommandRunner func(dir string, name string, args ...string) ([]byte, error)

func defaultTaskCommandRunner(dir string, name string, args ...string) ([]byte, error) {
	return DefaultHealthCommandRunner(dir, nil, name, args...)
}

// EnrichCorrelations adds t4 read-only correlations to an already collected
// snapshot. Command failures become capability gaps inside the correlation.
func EnrichCorrelations(ms *MultiSnapshot, run taskCommandRunner) {
	if ms == nil {
		return
	}
	if run == nil {
		run = defaultTaskCommandRunner
	}
	for i := range ms.Projects {
		ps := &ms.Projects[i]
		if !ps.TeamConfigured || len(ps.Snap.Sessions) == 0 {
			continue
		}
		ps.SessionCorrelations = map[string]SessionCorrelation{}
		for _, sess := range ps.Snap.Sessions {
			if strings.TrimSpace(sess.Name) == "" {
				continue
			}
			ps.SessionCorrelations[sess.Name] = FetchSessionCorrelation(run, *ps, sess)
		}
	}
}

func FetchSessionCorrelation(run taskCommandRunner, ps ProjectSnapshot, sess state.Session) SessionCorrelation {
	tasks := fetchTaskStore(run, ps.Dir, sess)
	runtimeStatus, runtimeErr := fetchRuntimeStatusForCorrelation(run, ps.Dir, sess)
	messages := correlationMessages(sess)
	reports := deriveWorkerReports(sess, messages)
	c := SessionCorrelation{
		Tasks:         tasks,
		WorkerReports: reports,
		Runtime:       deriveRuntimeBoard(sess, runtimeStatus, runtimeErr),
		Evidence:      deriveEvidenceSummary(tasks.Tasks, sess, reports),
	}
	c.Mismatches = deriveCorrelationSignals(tasks, reports, sess, messages)
	c.Status, c.Summary = summarizeCorrelation(c)
	return c
}

func fetchRuntimeStatusForCorrelation(run taskCommandRunner, dir string, sess state.Session) (RuntimeStatus, string) {
	args := []string{"status", "--project", dir}
	args = append(args, squadProfileArgs(sess.TeamProfile)...)
	args = append(args, "--session", sess.Name, "--json")
	out, err := run(dir, "amq-squad", args...)
	if err != nil {
		return RuntimeStatus{}, err.Error()
	}
	rs := parseRuntimeStatus(out)
	if !rs.HasActions() && !rs.HasStatusMetadata() {
		return RuntimeStatus{}, "amq-squad status did not publish runtime actions or namespace metadata"
	}
	return rs, ""
}

func fetchTaskStore(run taskCommandRunner, dir string, sess state.Session) TaskStoreSnapshot {
	args := []string{"task", "list", "--project", dir}
	args = append(args, squadProfileArgs(sess.TeamProfile)...)
	args = append(args, "--session", sess.Name, "--json")
	out, err := run(dir, "amq-squad", args...)
	if err != nil {
		return TaskStoreSnapshot{Status: healthStatusCapabilityGap, Error: err.Error()}
	}
	var env struct {
		Kind string `json:"kind"`
		Data struct {
			Tasks []struct {
				ID          string   `json:"id"`
				Title       string   `json:"title"`
				Description string   `json:"description"`
				Status      string   `json:"status"`
				AssignedTo  string   `json:"assigned_to"`
				DependsOn   []string `json:"depends_on"`
				CreatedAt   string   `json:"created_at"`
				UpdatedAt   string   `json:"updated_at"`
				Evidence    string   `json:"evidence"`
			} `json:"tasks"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &env); err != nil || env.Kind != "tasks" {
		return TaskStoreSnapshot{Status: healthStatusCapabilityGap, Error: "amq-squad task list did not return a tasks JSON envelope"}
	}
	tasks := make([]TaskItem, 0, len(env.Data.Tasks))
	for _, t := range env.Data.Tasks {
		tasks = append(tasks, TaskItem{
			ID:          strings.TrimSpace(t.ID),
			Title:       strings.TrimSpace(t.Title),
			Description: strings.TrimSpace(t.Description),
			Status:      strings.TrimSpace(t.Status),
			AssignedTo:  strings.TrimSpace(t.AssignedTo),
			DependsOn:   append([]string(nil), t.DependsOn...),
			CreatedAt:   parseOptionalTime(t.CreatedAt),
			UpdatedAt:   parseOptionalTime(t.UpdatedAt),
			Evidence:    strings.TrimSpace(t.Evidence),
		})
	}
	return TaskStoreSnapshot{Status: healthStatusOK, Tasks: tasks}
}

func squadProfileArgs(profile string) []string {
	profile = strings.TrimSpace(profile)
	if profile == "" || profile == "default" {
		return nil
	}
	return []string{"--profile", profile}
}

func correlationMessages(sess state.Session) []state.Message {
	if msgs := state.SessionMessages(sess.Root, 0); len(msgs) > 0 {
		return msgs
	}
	return append([]state.Message(nil), sess.Coordination.Messages...)
}

func deriveWorkerReports(sess state.Session, messages []state.Message) []WorkerReport {
	if len(messages) > 0 {
		return deriveWorkerReportsFromMessages(sess, messages)
	}
	var reports []WorkerReport
	for _, th := range sess.Coordination.Threads {
		if th.Gate {
			continue
		}
		reportType := workerReportType(th)
		if reportType == "" {
			continue
		}
		reports = append(reports, WorkerReport{
			Type:      reportType,
			From:      th.LastFrom,
			Thread:    th.ID,
			Subject:   th.Subject,
			At:        jsonTimePtrLocal(th.LastEventAt),
			Kind:      string(th.Kind),
			Preview:   compactPreview(th.LatestBody),
			Decision:  strings.HasPrefix(th.ID, "decision/") || th.Kind == state.KindDecision,
			Review:    th.Kind == state.KindReviewRequest || th.Kind == state.KindReviewResponse,
			Done:      doneSubject(th.Subject),
			Blocked:   th.Triage == state.TriageBlocked,
			NeedsLead: th.Status == state.ThreadAwaitingReply && th.Triage != state.TriageNeedsYou,
		})
	}
	sort.SliceStable(reports, func(i, j int) bool {
		if reports[i].At != nil && reports[j].At != nil && !reports[i].At.Equal(*reports[j].At) {
			return reports[i].At.Before(*reports[j].At)
		}
		return reports[i].Thread < reports[j].Thread
	})
	return reports
}

func deriveWorkerReportsFromMessages(sess state.Session, messages []state.Message) []WorkerReport {
	agentHandles := map[string]bool{}
	for _, ag := range sess.Agents {
		if strings.TrimSpace(ag.Handle) != "" {
			agentHandles[ag.Handle] = true
		}
	}
	seen := map[string]bool{}
	var reports []WorkerReport
	for _, m := range messages {
		if strings.HasPrefix(m.Thread, "gate/") {
			continue
		}
		if len(agentHandles) > 0 && !agentHandles[m.From] {
			continue
		}
		reportType := workerReportTypeMessage(m)
		if reportType == "" {
			continue
		}
		key := strings.TrimSpace(m.ID)
		if key == "" {
			key = m.From + "|" + m.Thread + "|" + m.Created.Format(time.RFC3339Nano) + "|" + m.Subject
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		reports = append(reports, WorkerReport{
			Type:      reportType,
			From:      m.From,
			Thread:    m.Thread,
			Subject:   m.Subject,
			At:        jsonTimePtrLocal(m.Created),
			Kind:      string(m.Kind),
			Preview:   compactPreview(m.Body),
			Decision:  strings.HasPrefix(m.Thread, "decision/") || m.Kind == state.KindDecision,
			Review:    m.Kind == state.KindReviewRequest || m.Kind == state.KindReviewResponse,
			Done:      doneSubject(m.Subject),
			Blocked:   messageDeclaresBlockForCorrelation(m),
			NeedsLead: m.Kind == state.KindQuestion || m.Kind == state.KindReviewRequest || m.Kind == state.KindDecision,
		})
	}
	sort.SliceStable(reports, func(i, j int) bool {
		if reports[i].At != nil && reports[j].At != nil && !reports[i].At.Equal(*reports[j].At) {
			return reports[i].At.Before(*reports[j].At)
		}
		return reports[i].Thread < reports[j].Thread
	})
	return reports
}

func workerReportType(th state.ThreadSummary) string {
	if doneSubject(th.Subject) {
		return "done"
	}
	switch th.Kind {
	case state.KindQuestion:
		return "question"
	case state.KindReviewRequest:
		return "review_request"
	case state.KindReviewResponse:
		return "review_response"
	case state.KindDecision:
		return "decision"
	case state.KindStatus:
		if th.Triage == state.TriageBlocked {
			return "blocked"
		}
		return "progress"
	default:
		if strings.HasPrefix(th.ID, "decision/") {
			return "decision"
		}
		return ""
	}
}

func workerReportTypeMessage(m state.Message) string {
	if doneSubject(m.Subject) {
		return "done"
	}
	switch m.Kind {
	case state.KindQuestion:
		return "question"
	case state.KindReviewRequest:
		return "review_request"
	case state.KindReviewResponse:
		return "review_response"
	case state.KindDecision:
		return "decision"
	case state.KindStatus:
		if messageDeclaresBlockForCorrelation(m) {
			return "blocked"
		}
		return "progress"
	default:
		if strings.HasPrefix(m.Thread, "decision/") {
			return "decision"
		}
		return ""
	}
}

func deriveRuntimeBoard(sess state.Session, rs RuntimeStatus, runtimeErr string) RuntimeBoard {
	if rs.HasActions() || rs.HasStatusMetadata() {
		return deriveRuntimeBoardFromStatus(sess, rs)
	}
	b := RuntimeBoard{
		Status:       healthStatusCapabilityGap,
		Source:       "state snapshot fallback",
		Error:        runtimeErr,
		LeadDown:     state.SessionLeadDown(sess),
		WorkersTotal: len(sess.Agents),
	}
	for _, ag := range sess.Agents {
		live := agentOperationalForCorrelation(ag)
		if live {
			b.WorkersLive++
		} else {
			b.WorkersAtRisk++
		}
		row := RuntimeWorkerState{
			Handle:     ag.Handle,
			Role:       ag.Role,
			Source:     "state snapshot",
			Liveness:   string(ag.Liveness),
			WakeHealth: string(ag.WakeHealth),
			IsLead:     ag.IsLead,
			PaneAlive:  live,
			NextAction: nextRuntimeAction(ag),
		}
		if ag.IsLead && strings.TrimSpace(ag.AgentDir) == "" && live {
			b.ExternalLead = true
			b.ExternalDetail = "lead has live signals without a squad-owned launch directory"
		}
		b.Rows = append(b.Rows, row)
	}
	return b
}

func deriveRuntimeBoardFromStatus(sess state.Session, rs RuntimeStatus) RuntimeBoard {
	b := RuntimeBoard{
		Status:       healthStatusOK,
		Source:       "amq-squad status --json",
		Namespace:    rs.Namespace,
		GoalBinding:  rs.GoalBinding,
		Topology:     rs.Topology,
		LeadDown:     state.SessionLeadDown(sess),
		WorkersTotal: len(rs.Members),
	}
	for _, mem := range rs.Members {
		live := runtimeMemberLive(mem)
		if live {
			b.WorkersLive++
		} else {
			b.WorkersAtRisk++
		}
		if mem.IsLead && !live {
			b.LeadDown = true
		}
		if mem.External {
			b.ExternalLead = true
			b.ExternalDetail = "lead/member reported by amq-squad as external runtime"
		}
		b.Rows = append(b.Rows, RuntimeWorkerState{
			Handle:      mem.Handle,
			Role:        mem.Role,
			Status:      mem.Status,
			RecordState: mem.RecordState,
			Source:      "amq-squad status --json",
			Liveness:    runtimeMemberLiveness(mem),
			IsLead:      mem.IsLead,
			PaneAlive:   mem.PaneAlive,
			NextAction:  nextRuntimeStatusAction(mem),
		})
	}
	return b
}

func deriveCorrelationSignals(tasks TaskStoreSnapshot, reports []WorkerReport, sess state.Session, messages []state.Message) []CorrelationSignal {
	var signals []CorrelationSignal
	if len(messages) == 0 && len(sess.Coordination.Threads) > 0 {
		signals = append(signals, CorrelationSignal{Name: "worker_report_history_unavailable", Status: healthStatusCapabilityGap, Detail: "coordination snapshot has only collapsed thread summaries; worker report history may be incomplete"})
	}
	if tasks.Status != healthStatusOK {
		return append(signals, CorrelationSignal{Name: "task_store_unavailable", Status: healthStatusCapabilityGap, Detail: tasks.Error})
	}
	taskByID := map[string]TaskItem{}
	for _, task := range tasks.Tasks {
		taskByID[task.ID] = task
	}
	for _, task := range tasks.Tasks {
		if strings.EqualFold(task.Status, "pending") && hasAssigneeReport(reports, task.AssignedTo) {
			signals = append(signals, CorrelationSignal{Name: "pending_task_has_worker_report", Status: healthStatusWarn, TaskID: task.ID, Detail: task.AssignedTo + " has AMQ report evidence while task is pending"})
		}
		if (strings.EqualFold(task.Status, "blocked") || strings.EqualFold(task.Status, "failed")) && !hasAssigneeBlockReport(reports, task.AssignedTo) {
			signals = append(signals, CorrelationSignal{Name: "blocked_task_without_report", Status: healthStatusWarn, TaskID: task.ID, Detail: "task is " + task.Status + " but no visible blocker report from assignee"})
		}
		for _, dep := range task.DependsOn {
			if !taskCompleted(taskByID[dep]) && strings.EqualFold(task.Status, "pending") {
				signals = append(signals, CorrelationSignal{Name: "dependency_gated_task", Status: healthStatusOK, TaskID: task.ID, Detail: "waiting on dependency " + dep})
			}
		}
	}
	if hasReviewRequest(reports) && !hasCompletedImplementationTask(tasks.Tasks) {
		signals = append(signals, CorrelationSignal{Name: "review_request_before_task_done", Status: healthStatusWarn, Detail: "review_request exists but implementation tasks are not all completed"})
	}
	for _, th := range sess.Coordination.Threads {
		if strings.HasPrefix(th.ID, "decision/") && th.Triage != state.TriageClear {
			signals = append(signals, CorrelationSignal{Name: "decision_thread_attention", Status: healthStatusWarn, Thread: th.ID, Detail: th.Subject})
		}
	}
	return signals
}

func deriveEvidenceSummary(tasks []TaskItem, sess state.Session, reports []WorkerReport) EvidenceSummary {
	var e EvidenceSummary
	for _, task := range tasks {
		addEvidenceText(&e, task.Title)
		addEvidenceText(&e, task.Description)
		addEvidenceText(&e, task.Evidence)
	}
	for _, th := range sess.Coordination.Threads {
		text := strings.TrimSpace(th.Subject + "\n" + th.LatestBody)
		addEvidenceText(&e, text)
		if th.Kind == state.KindReviewRequest || th.Kind == state.KindReviewResponse {
			e.ReviewThreads = appendUnique(e.ReviewThreads, th.ID)
		}
		if strings.HasPrefix(th.ID, "decision/") || th.Kind == state.KindDecision {
			e.DecisionThreads = appendUnique(e.DecisionThreads, th.ID)
		}
		if th.Gate && containsMergeRelease(text) {
			e.MergeReleaseGates = appendUnique(e.MergeReleaseGates, th.ID)
			e.HumanGateRequired = !th.GateAnswered
		}
	}
	for _, report := range reports {
		if report.Review {
			e.ReviewThreads = appendUnique(e.ReviewThreads, report.Thread)
		}
	}
	if len(e.PRURLs) == 0 && len(e.HeadSHAs) == 0 && len(e.LocalTests) == 0 && len(e.CITests) == 0 {
		e.CapabilityGap = "no stable upstream merge/release evidence schema; derived from task evidence and AMQ thread text only"
	}
	if hasReviewReport(reports) && len(e.ReviewThreads) == 0 {
		e.Blockers = appendUnique(e.Blockers, "review report exists without review thread evidence")
	}
	if len(e.HeadSHAs) > 1 {
		e.Blockers = appendUnique(e.Blockers, "multiple head SHAs observed; verify SHA match before merge/release")
	}
	if releaseEvidenceExpected(tasks, sess, reports, e) {
		if len(e.PRURLs) == 0 {
			e.Blockers = appendUnique(e.Blockers, "missing PR URL evidence")
		}
		if len(e.HeadSHAs) == 0 {
			e.Blockers = appendUnique(e.Blockers, "missing reviewed head SHA evidence")
		}
		if len(e.LocalTests) == 0 && len(e.CITests) == 0 {
			e.Blockers = appendUnique(e.Blockers, "missing local or CI test evidence")
		}
		if len(e.ReviewThreads) == 0 {
			e.Blockers = appendUnique(e.Blockers, "missing review evidence")
		}
		if len(e.MergeReleaseGates) == 0 {
			e.Blockers = appendUnique(e.Blockers, "missing human-owned merge/release gate")
		}
	}
	if len(e.Blockers) > 0 {
		e.Status = healthStatusWarn
	} else if e.CapabilityGap != "" {
		e.Status = healthStatusCapabilityGap
	} else {
		e.Status = healthStatusOK
	}
	return e
}

func releaseEvidenceExpected(tasks []TaskItem, sess state.Session, reports []WorkerReport, e EvidenceSummary) bool {
	if len(e.PRURLs) > 0 || len(e.HeadSHAs) > 0 || len(e.LocalTests) > 0 || len(e.CITests) > 0 || len(e.MergeReleaseGates) > 0 || e.VerifyMergeVerdict != "" {
		return true
	}
	for _, report := range reports {
		text := report.Subject + " " + report.Preview
		if releaseEvidenceText(text) {
			return true
		}
	}
	for _, task := range tasks {
		text := strings.ToLower(task.Title + " " + task.Description + " " + task.Evidence)
		if releaseEvidenceText(text) {
			return true
		}
	}
	for _, th := range sess.Coordination.Threads {
		if releaseEvidenceText(th.Subject + " " + th.LatestBody) {
			return true
		}
	}
	return false
}

func releaseEvidenceText(text string) bool {
	text = strings.ToLower(text)
	if containsCIEvidence(text) {
		return true
	}
	for _, marker := range []string{"merge", "release", "pr ", "pull request", "head sha", "reviewed head", "test evidence", "verify merge"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func containsCIEvidence(text string) bool {
	return ciEvidenceRE.MatchString(text)
}

func summarizeCorrelation(c SessionCorrelation) (string, string) {
	status := healthStatusOK
	status = worseHealth(status, c.Tasks.Status)
	status = worseHealth(status, c.Runtime.Status)
	for _, m := range c.Mismatches {
		status = worseHealth(status, m.Status)
	}
	status = worseHealth(status, c.Evidence.Status)
	return status, "task store, worker reports, runtime/liveness, and release evidence correlation"
}

func addEvidenceText(e *EvidenceSummary, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	e.TaskEvidence = appendUnique(e.TaskEvidence, strings.TrimSpace(text))
	for _, match := range prURLRE.FindAllString(text, -1) {
		e.PRURLs = appendUnique(e.PRURLs, match)
	}
	for _, match := range issueRefRE.FindAllString(text, -1) {
		e.IssueRefs = appendUnique(e.IssueRefs, match)
	}
	for _, match := range shaRE.FindAllString(text, -1) {
		e.HeadSHAs = appendUnique(e.HeadSHAs, match)
	}
	lower := strings.ToLower(text)
	if strings.Contains(lower, "go test") || strings.Contains(lower, "gofmt") || strings.Contains(lower, "git diff --check") {
		e.LocalTests = appendUnique(e.LocalTests, compactPreview(text))
	}
	if containsCIEvidence(text) {
		e.CITests = appendUnique(e.CITests, compactPreview(text))
	}
	if strings.Contains(lower, "verify merge") || strings.Contains(lower, "merge verified") || strings.Contains(lower, "sha match") {
		e.VerifyMergeVerdict = compactPreview(text)
	}
}

var (
	prURLRE      = regexp.MustCompile(`https?://[^\s)]+/(pull|pulls|merge_requests)/[0-9]+`)
	issueRefRE   = regexp.MustCompile(`#\d+`)
	shaRE        = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)
	ciEvidenceRE = regexp.MustCompile(`(?i)\b(ci|github actions)\b`)
)

func hasAssigneeReport(reports []WorkerReport, assignee string) bool {
	assignee = strings.TrimSpace(assignee)
	if assignee == "" {
		return false
	}
	for _, r := range reports {
		if r.From == assignee && (r.Type == "progress" || r.Type == "done" || r.Review) {
			return true
		}
	}
	return false
}

func hasAssigneeBlockReport(reports []WorkerReport, assignee string) bool {
	for _, r := range reports {
		if r.From == assignee && (r.Blocked || r.Type == "question") {
			return true
		}
	}
	return false
}

func hasReviewRequest(reports []WorkerReport) bool {
	for _, r := range reports {
		if r.Type == "review_request" {
			return true
		}
	}
	return false
}

func hasReviewReport(reports []WorkerReport) bool {
	for _, r := range reports {
		if r.Review {
			return true
		}
	}
	return false
}

func hasCompletedImplementationTask(tasks []TaskItem) bool {
	for _, task := range tasks {
		if strings.EqualFold(task.Status, "completed") && !strings.Contains(strings.ToLower(task.Title), "review") {
			return true
		}
	}
	return false
}

func taskCompleted(task TaskItem) bool {
	return strings.EqualFold(task.Status, "completed")
}

func agentOperationalForCorrelation(ag state.Agent) bool {
	switch ag.Liveness {
	case state.LivenessAlive, state.LivenessWakeLive, state.LivenessDeadMailboxLive:
		return true
	default:
		return false
	}
}

func runtimeMemberLive(mem RuntimeMember) bool {
	status := strings.ToLower(strings.TrimSpace(mem.Status))
	return status == "live" || mem.PaneAlive
}

func runtimeMemberLiveness(mem RuntimeMember) string {
	if s := strings.TrimSpace(mem.Status); s != "" {
		return s
	}
	if mem.PaneAlive {
		return "live"
	}
	return "unavailable"
}

func nextRuntimeStatusAction(mem RuntimeMember) string {
	order := []string{"resume", "status"}
	if runtimeMemberLive(mem) {
		order = []string{"focus", "status", "resume"}
	}
	for _, want := range order {
		for _, action := range mem.Actions {
			if action.Available && action.Kind == want {
				return want
			}
		}
	}
	return ""
}

func nextRuntimeAction(ag state.Agent) string {
	if agentOperationalForCorrelation(ag) {
		return "status"
	}
	return "agent_resume"
}

func doneSubject(subject string) bool {
	return strings.HasPrefix(strings.ToUpper(strings.TrimSpace(subject)), "DONE:")
}

func containsMergeRelease(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "merge") || strings.Contains(lower, "release")
}

func messageDeclaresBlockForCorrelation(m state.Message) bool {
	text := strings.ToLower(m.Subject + "\n" + m.Body)
	for _, marker := range []string{"no-go", "blocker:", "blocked on", "i am blocked", "we are blocked", "blocking:"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func compactPreview(s string) string {
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	if len(s) <= 180 {
		return s
	}
	return strings.TrimSpace(s[:177]) + "..."
}

func appendUnique(list []string, v string) []string {
	v = strings.TrimSpace(v)
	if v == "" {
		return list
	}
	for _, existing := range list {
		if existing == v {
			return list
		}
	}
	return append(list, v)
}

func parseOptionalTime(s string) *time.Time {
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(s))
	if err != nil {
		return nil
	}
	return &t
}

func jsonTimePtrLocal(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
