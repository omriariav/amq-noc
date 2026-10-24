package center

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Client is the command center's whole reach outside the process. It has
// exactly four calls: the status read, the contract's new-task argv, and the
// two AMQ primitives of the lead conversation (thread read, send). No other
// program is ever started and no file is opened.
type Client struct {
	Runner Runner
	// Fleet selects `status --fleet --json`.
	Fleet bool
}

// Status runs the status call and parses it.
func (c Client) Status(ctx context.Context) (Fleet, error) {
	out, err := c.Runner.Run(ctx, StatusArgv(c.Fleet))
	if err != nil {
		return Fleet{}, err
	}
	return Parse(out)
}

// NewTask runs the contract's new_task argv with the user's values.
func (c Client) NewTask(ctx context.Context, a Action, values map[string]string) (string, error) {
	argv, err := NewTaskArgv(a, values)
	if err != nil {
		return "", err
	}
	out, err := c.Runner.Run(ctx, argv)
	return strings.TrimSpace(firstLine(strings.TrimSpace(string(out)))), err
}

// Message is one message of the lead conversation.
type Message struct {
	ID      string    `json:"id"`
	From    string    `json:"from"`
	To      []string  `json:"to"`
	Thread  string    `json:"thread"`
	Subject string    `json:"subject"`
	Created time.Time `json:"created"`
	Body    string    `json:"body"`
}

// P2PThread is the AMQ thread id of the dialogue between two handles.
func P2PThread(a, b string) string {
	h := []string{a, b}
	sort.Strings(h)
	return "p2p/" + h[0] + "__" + h[1]
}

// Conversation reads the operator-lead thread through `amq thread`.
func (c Client) Conversation(ctx context.Context, root, operator, lead string, limit int) ([]Message, error) {
	argv := []string{"amq", "thread", "--id", P2PThread(operator, lead), "--me", operator,
		"--root", root, "--json", "--include-body", "--limit", fmt.Sprint(limit)}
	out, err := c.Runner.Run(ctx, argv)
	if err != nil {
		return nil, err
	}
	var msgs []Message
	if err := json.Unmarshal(out, &msgs); err != nil {
		return nil, fmt.Errorf("amq thread: %w", err)
	}
	sort.SliceStable(msgs, func(i, j int) bool { return msgs[i].Created.Before(msgs[j].Created) })
	return msgs, nil
}

// Send posts one message to the lead on the operator-lead thread through
// `amq send`.
func (c Client) Send(ctx context.Context, root, operator, lead, body string) error {
	argv := []string{"amq", "send", "--root", root, "--me", operator, "--to", lead,
		"--thread", P2PThread(operator, lead), "--body", body}
	_, err := c.Runner.Run(ctx, argv)
	return err
}
