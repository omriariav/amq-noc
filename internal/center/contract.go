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

// Status is one squad's `status --json` document. LeadHandle is an
// optional additive field (lead ruling; amq-squad is adding it).
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

// Fleet is every squad the input described. A one-squad document is a Fleet
// of one.
type Fleet struct {
	Squads []Status
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
		Squads []json.RawMessage `json:"squads"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return Fleet{}, fmt.Errorf("status: %w", err)
	}
	if probe.Squads != nil {
		var f Fleet
		for i, raw := range probe.Squads {
			s, err := parseOne(raw)
			if err != nil {
				return Fleet{}, fmt.Errorf("squad %d: %w", i, err)
			}
			f.Squads = append(f.Squads, s)
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
	return s, nil
}
