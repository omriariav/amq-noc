// Package center is the amq-noc 1.0 command center (amq-squad 3.0 plan 4.7,
// R44). Its single input is the JSON of `amq-squad status --json` (one squad)
// or `amq-squad status --fleet --json` (many squads), schema 1. The renderer
// never reads `.amq-squad/` files, never computes a bucket and never composes
// an amq-squad command: the buckets and the "new task" argv come from the
// contract.
package center

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// SchemaVersion is the status contract major version this renderer reads.
const SchemaVersion = 1

// Bucket ids as the contract spells them.
const (
	BucketNeedsYou = "needs_you"
	BucketWorking  = "working"
	BucketReady    = "ready"
	BucketInactive = "inactive"
)

// MarkerUnknown is the contract's bucket_marker of a row it could not classify.
const MarkerUnknown = "unknown"

// BucketInfo names a bucket, in the contract's display order.
type BucketInfo struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Rule  string `json:"rule"`
}

// ActionInput is one value the user supplies for an Action.
type ActionInput struct {
	Name     string   `json:"name"`
	Flag     string   `json:"flag"`
	Required bool     `json:"required"`
	Choices  []string `json:"choices"`
}

// Action is a suggested command carried as argv, never a shell string.
type Action struct {
	ID     string        `json:"id"`
	Label  string        `json:"label"`
	Argv   []string      `json:"argv"`
	Inputs []ActionInput `json:"inputs"`
}

// Pool is the runtime, model and effort key of a task.
type Pool struct {
	Runtime string `json:"runtime"`
	Model   string `json:"model"`
	Effort  string `json:"effort"`
}

// Seat is one seat row of the contract.
type Seat struct {
	Handle         string     `json:"handle"`
	Role           string     `json:"role"`
	Runtime        string     `json:"runtime"`
	Model          string     `json:"model"`
	Effort         string     `json:"effort"`
	State          string     `json:"state"`
	Live           *bool      `json:"live"` // nil when the producer predates the field
	ReasonCode     string     `json:"reason_code"`
	Bucket         string     `json:"bucket"`
	BucketMarker   string     `json:"bucket_marker"`
	TaskID         string     `json:"task_id"`
	ProjectPath    string     `json:"project_path"`
	LastActivityAt *time.Time `json:"last_activity_at"`
	LastMessageAt  *time.Time `json:"last_message_at"`
	LastMessage    string     `json:"last_message"`
	PromptState    string     `json:"prompt_state"`
	TokensIn       *int64     `json:"tokens_in"`
	TokensOut      *int64     `json:"tokens_out"`
}

// Task is one task row of the contract.
type Task struct {
	ID             string     `json:"id"`
	Title          string     `json:"title"`
	Class          string     `json:"class"`
	TaskState      string     `json:"task_state"`
	State          string     `json:"state"`
	ReasonCode     string     `json:"reason_code"`
	Bucket         string     `json:"bucket"`
	BucketMarker   string     `json:"bucket_marker"`
	Pool           *Pool      `json:"pool"`
	LeaseHolder    string     `json:"lease_holder"`
	LeaseExpiresAt *time.Time `json:"lease_expires_at"`
	LeaseClaimed   bool       `json:"lease_claimed"`
	Attempt        int        `json:"attempt"`
	WaitReason     string     `json:"wait_reason"`
	BlockReason    string     `json:"block_reason"`
	DependsOn      []string   `json:"depends_on"`
	ProjectPath    string     `json:"project_path"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// Gate is one gate record; the renderer only shows it.
type Gate struct {
	ID           string `json:"id"`
	ActionClass  string `json:"action_class"`
	TargetDigest string `json:"target_digest"`
	State        string `json:"state"`
	RaisedBy     string `json:"raised_by"`
}

// Attention is one attention-queue item.
type Attention struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"`
	Severity string    `json:"severity"`
	Subject  string    `json:"subject"`
	Since    time.Time `json:"since"`
	Seat     string    `json:"seat"`
	TaskID   string    `json:"task_id"`
	GateID   string    `json:"gate_id"`
	Bucket   string    `json:"bucket"`
	Action   *Action   `json:"action"`
}

// Status is one squad's `status --json` document. LeadHandle is the handle of
// the seat whose role is lead (null in the document when none has it).
type Status struct {
	SchemaVersion   int          `json:"schema_version"`
	Producer        string       `json:"producer"`
	ProducerVersion string       `json:"producer_version"`
	GeneratedAt     time.Time    `json:"generated_at"`
	Root            string       `json:"root"`
	Session         string       `json:"session"`
	ProjectPath     string       `json:"project_path"`
	Capabilities    []string     `json:"capabilities"`
	Buckets         []BucketInfo `json:"buckets"`
	Seats           []Seat       `json:"seats"`
	Tasks           []Task       `json:"tasks"`
	Attention       []Attention  `json:"attention"`
	Gates           []Gate       `json:"gates"`
	Actions         struct {
		NewTask Action `json:"new_task"`
	} `json:"actions"`
	Warnings   []string `json:"warnings"`
	LeadHandle string   `json:"lead_handle"`
}

// WaitingSquad is one entry of the fleet document's waiting[] list: a squad
// the mayor held back for the fleet grant's cap, with no squad document yet.
type WaitingSquad struct {
	Squad       string    `json:"squad"`
	Repository  string    `json:"repository"`
	ProjectPath string    `json:"project_path"`
	WaitReason  string    `json:"wait_reason"`
	Cap         string    `json:"cap"`
	Since       time.Time `json:"since"`
	Detail      string    `json:"detail"`
}

// Fleet is every squad the input described. A one-squad document is a Fleet
// of one. Operator and Waiting come from the fleet document only.
type Fleet struct {
	Squads   []Status
	Operator *Seat // the operator's own seat row (handle user, role operator), or nil
	Waiting  []WaitingSquad
	Warnings []string
	IsFleet  bool // the input was a fleet document
}

// Parse reads the output of `status --json` or `status --fleet --json`. A
// fleet document (briefs lead ruling: `{"schema_version":1,"kind":"fleet","producer",
// "producer_version","generated_at","squads":[<one status per squad>]}`) is an
// object with a "squads" array of per-squad documents;
// anything else is read as one squad's document. A schema_version other than 1
// is refused.
func Parse(data []byte) (Fleet, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' {
		return Fleet{}, fmt.Errorf("status: not a JSON object")
	}
	var probe struct {
		SchemaVersion int               `json:"schema_version"`
		Kind          string            `json:"kind"`
		Squads        []json.RawMessage `json:"squads"`
		Operator      *Seat             `json:"operator"`
		Waiting       []WaitingSquad    `json:"waiting"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return Fleet{}, fmt.Errorf("status: %w", err)
	}
	// A fleet document says kind "fleet" or carries squads[]; a one-squad
	// document has neither, whatever other keys it grows.
	if probe.Kind == "fleet" || probe.Squads != nil {
		if probe.SchemaVersion != SchemaVersion {
			return Fleet{}, fmt.Errorf("status: fleet schema_version %d, this amq-noc reads %d", probe.SchemaVersion, SchemaVersion)
		}
		// An empty fleet (no squads in fleet.yaml) is a normal producer
		// output: it renders as an empty board, not an error.
		f := Fleet{IsFleet: true}
		for i, raw := range probe.Squads {
			s, err := parseOne(raw)
			if err != nil {
				return Fleet{}, fmt.Errorf("squad %d: %w", i, err)
			}
			f.Squads = append(f.Squads, s)
		}
		f.Operator, f.Waiting = probe.Operator, probe.Waiting
		if op := f.Operator; op != nil && !validBucket(op.Bucket) {
			f.Warnings = append(f.Warnings, fmt.Sprintf("operator %s: unknown bucket %q, shown as inactive", op.Handle, op.Bucket))
			op.Bucket, op.BucketMarker = BucketInactive, MarkerUnknown
		}
		return f, nil
	}
	s, err := parseOne(data)
	if err != nil {
		return Fleet{}, err
	}
	return Fleet{Squads: []Status{s}}, nil
}

func parseOne(data []byte) (Status, error) {
	var s Status
	if err := json.Unmarshal(data, &s); err != nil {
		return Status{}, fmt.Errorf("status: %w", err)
	}
	if s.SchemaVersion != SchemaVersion {
		return Status{}, fmt.Errorf("status: schema_version %d, this amq-noc reads %d", s.SchemaVersion, SchemaVersion)
	}
	normalizeBuckets(&s)
	return s, nil
}

// normalizeBuckets maps a row whose bucket is not one of the four the
// contract names to inactive with the unknown marker, and adds a warning line
// naming the row and the value. The document is not refused: one bad row must
// not blank the screen, and no row may sit under All without a tab.
func normalizeBuckets(s *Status) {
	ok := validBucket
	for i := range s.Tasks {
		if t := &s.Tasks[i]; !ok(t.Bucket) {
			s.Warnings = append(s.Warnings, fmt.Sprintf("task %s: unknown bucket %q, shown as inactive", t.ID, t.Bucket))
			t.Bucket, t.BucketMarker = BucketInactive, MarkerUnknown
		}
	}
	for i := range s.Seats {
		if st := &s.Seats[i]; !ok(st.Bucket) {
			s.Warnings = append(s.Warnings, fmt.Sprintf("seat %s: unknown bucket %q, shown as inactive", st.Handle, st.Bucket))
			st.Bucket, st.BucketMarker = BucketInactive, MarkerUnknown
		}
	}
}

func validBucket(b string) bool {
	return b == BucketNeedsYou || b == BucketWorking || b == BucketReady || b == BucketInactive
}
