package center

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// Tab is one filter tab: All plus the four buckets, in display order.
type Tab int

const (
	TabAll Tab = iota
	TabNeedsYou
	TabWorking
	TabReady
	TabInactive
	tabCount
)

// tabBucket maps a tab to the contract bucket id it filters on ("" for All).
var tabBucket = [tabCount]string{"", BucketNeedsYou, BucketWorking, BucketReady, BucketInactive}

var tabLabel = [tabCount]string{"All", "Needs you", "Working", "Ready", "Inactive"}

// Row is one list row: a task, or a seat that holds no task. Every field is
// read from the contract; the bucket is the contract's, never recomputed.
type Row struct {
	Squad   int // index into Fleet.Squads
	Project string
	Task    *Task // nil for a seat row
	Seat    *Seat // the lease holder of a task row (nil if unknown), or the seat itself

	Section int           // sectionOperator, sectionSquad or sectionWaiting
	Waiting *WaitingSquad // set for a row of the fleet's waiting[] list

	Title   string
	Bucket  string
	Marker  string
	State   string
	Reason  string
	Updated *time.Time
}

// Row sections, in list order: the operator's own row first, then the squads'
// rows grouped by project, then the squads waiting for the fleet cap.
const (
	sectionOperator = iota
	sectionSquad
	sectionWaiting
)

// NotLive reports that a seat row's `live` is false while its bucket is not
// Inactive. The contract classifies; the renderer only shows it.
func (r Row) NotLive() bool {
	return r.Task == nil && r.Seat != nil && r.Seat.Live != nil && !*r.Seat.Live && r.Bucket != BucketInactive
}

// IsTask reports whether the row is a task row.
func (r Row) IsTask() bool { return r.Task != nil }

// BuildRows turns the fleet into rows: one per task, plus one per seat that
// holds no task. Order is by project path, then bucket urgency, then the most
// recently updated first.
func BuildRows(f Fleet) []Row {
	var rows []Row
	for si := range f.Squads {
		s := &f.Squads[si]
		seatByHandle := make(map[string]*Seat, len(s.Seats))
		for i := range s.Seats {
			seatByHandle[s.Seats[i].Handle] = &s.Seats[i]
		}
		taskIDs := make(map[string]bool, len(s.Tasks))
		for i := range s.Tasks {
			t := &s.Tasks[i]
			taskIDs[t.ID] = true
			u := t.UpdatedAt
			rows = append(rows, Row{
				Squad: si, Section: sectionSquad, Project: orProject(t.ProjectPath, s.ProjectPath), Task: t,
				Seat: seatByHandle[t.LeaseHolder], Title: t.Title,
				Bucket: t.Bucket, Marker: t.BucketMarker, State: t.State, Reason: t.ReasonCode,
				Updated: &u,
			})
		}
		for i := range s.Seats {
			st := &s.Seats[i]
			if st.TaskID != "" && taskIDs[st.TaskID] {
				continue // shown through its task row
			}
			title := st.Handle
			if st.Role != "" {
				title = st.Handle + " (" + st.Role + ")"
			}
			rows = append(rows, Row{
				Squad: si, Section: sectionSquad, Project: orProject(st.ProjectPath, s.ProjectPath), Seat: st, Title: title,
				Bucket: st.Bucket, Marker: st.BucketMarker, State: st.State, Reason: st.ReasonCode,
				Updated: st.LastActivityAt,
			})
		}
	}
	// The operator row and the waiting rows belong to no squad; their Squad
	// index is past the end so every squad lookup misses.
	if op := f.Operator; op != nil {
		title := op.Handle
		if op.Role != "" {
			title += " (" + op.Role + ")"
		}
		rows = append(rows, Row{Squad: len(f.Squads), Section: sectionOperator, Project: op.ProjectPath, Seat: op, Title: title,
			Bucket: op.Bucket, Marker: op.BucketMarker, State: op.State, Reason: op.ReasonCode, Updated: op.LastActivityAt})
	}
	for i := range f.Waiting {
		w := &f.Waiting[i]
		since := w.Since
		rows = append(rows, Row{Squad: len(f.Squads), Section: sectionWaiting, Project: w.ProjectPath, Waiting: w, Title: w.Squad,
			State: w.WaitReason, Reason: w.Cap, Updated: &since})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Section != b.Section {
			return a.Section < b.Section
		}
		if a.Project != b.Project {
			return a.Project < b.Project
		}
		if ra, rb := bucketRank(a.Bucket), bucketRank(b.Bucket); ra != rb {
			return ra < rb
		}
		return newer(a.Updated, b.Updated)
	})
	return rows
}

func orProject(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func bucketRank(b string) int {
	switch b {
	case BucketNeedsYou:
		return 0
	case BucketWorking:
		return 1
	case BucketReady:
		return 2
	case BucketInactive:
		return 3
	}
	return 4
}

// newer orders rows with a known time before rows without one, newest first.
func newer(a, b *time.Time) bool {
	switch {
	case a == nil:
		return false
	case b == nil:
		return true
	}
	return a.After(*b)
}

// Counts is the row count per tab, indexed by Tab. All is the sum of the four
// buckets plus any row whose bucket the contract left unnamed.
func Counts(rows []Row) [tabCount]int {
	var c [tabCount]int
	for _, r := range rows {
		c[TabAll]++
		for t := TabNeedsYou; t < tabCount; t++ {
			if r.Bucket == tabBucket[t] {
				c[t]++
			}
		}
	}
	return c
}

// Filter returns the rows of one tab.
func Filter(rows []Row, tab Tab) []Row {
	if tab == TabAll {
		return rows
	}
	var out []Row
	for _, r := range rows {
		if r.Bucket == tabBucket[tab] {
			out = append(out, r)
		}
	}
	return out
}

// Group is the rows of one project path, or the Operator or Waiting group.
type Group struct {
	Key   string // unique per group
	Label string // the header text
	Rows  []Row
}

// GroupRows groups already sorted rows by section and project path, keeping
// order. The operator row is a group of its own, and so are the waiting rows.
func GroupRows(rows []Row) []Group {
	var gs []Group
	for _, r := range rows {
		key, label := fmt.Sprintf("%d|%s", r.Section, r.Project), DisplayPath(r.Project)
		switch r.Section {
		case sectionOperator:
			key, label = "operator", "Operator"
		case sectionWaiting:
			key, label = "waiting", "Waiting"
		}
		if n := len(gs); n > 0 && gs[n-1].Key == key {
			gs[n-1].Rows = append(gs[n-1].Rows, r)
			continue
		}
		gs = append(gs, Group{Key: key, Label: label, Rows: []Row{r}})
	}
	return gs
}

// StatusLabel is the text of a row's status column: the label of its bucket
// as the contract names it, with a marker for a row the contract could not
// classify.
func StatusLabel(f Fleet, r Row) string {
	if r.Waiting != nil {
		return orDash(r.Waiting.WaitReason)
	}
	label := bucketLabel(f, r)
	if r.Marker != "" {
		return label + " (" + r.Marker + ")"
	}
	return label
}

func bucketLabel(f Fleet, r Row) string {
	if r.Squad < len(f.Squads) {
		for _, b := range f.Squads[r.Squad].Buckets {
			if b.ID == r.Bucket {
				return b.Label
			}
		}
	}
	for t := TabNeedsYou; t < tabCount; t++ {
		if tabBucket[t] == r.Bucket {
			return tabLabel[t]
		}
	}
	return r.Bucket
}

// RelTime renders a relative time such as "38s ago" or "3d ago".
func RelTime(t *time.Time, now time.Time) string {
	if t == nil {
		return "-"
	}
	d := now.Sub(*t)
	switch {
	case d < 0:
		return "now"
	case d < time.Minute:
		return itoa(int(d/time.Second)) + "s ago"
	case d < time.Hour:
		return itoa(int(d/time.Minute)) + "m ago"
	case d < 48*time.Hour:
		return itoa(int(d/time.Hour)) + "h ago"
	default:
		return itoa(int(d/(24*time.Hour))) + "d ago"
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// DisplayPath shortens the home directory prefix to "~".
func DisplayPath(p string) string {
	if p == "" {
		return "(no project)"
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(p, home+"/") {
		return "~" + p[len(home):]
	}
	return p
}
