package center

import (
	"fmt"
	"time"
)

var testNow = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func secs(n int) time.Duration { return time.Duration(n) * time.Second }

func ptrTime(t time.Time) *time.Time { return &t }
func ptrInt(n int64) *int64          { return &n }

// bucketLabels is the contract's bucket table as the fixture spells it.
var testBuckets = []BucketInfo{
	{ID: BucketNeedsYou, Label: "Needs you"},
	{ID: BucketWorking, Label: "Working"},
	{ID: BucketReady, Label: "Ready"},
	{ID: BucketInactive, Label: "Inactive"},
}

// testSquad builds one squad status with the given project and one task per
// bucket in order, with ages in minutes.
func testSquad(project string, buckets ...string) Status {
	s := Status{SchemaVersion: 1, Producer: "amq-squad", GeneratedAt: testNow, Root: "/amq/" + project,
		Session: "squad", ProjectPath: project, Buckets: testBuckets, LeadHandle: "lead"}
	s.Actions.NewTask = Action{ID: "task.new", Label: "new task",
		Argv:   []string{"amq-squad", "task", "add", "--root", s.Root},
		Inputs: []ActionInput{{Name: "title", Flag: "--title", Required: true}, {Name: "class", Flag: "--class", Required: true, Choices: []string{"implement", "review"}}}}
	for i, b := range buckets {
		id := fmt.Sprintf("t%d", i+1)
		s.Tasks = append(s.Tasks, Task{ID: id, Title: "task " + id + " of " + project, Bucket: b,
			State: "online", ProjectPath: project, UpdatedAt: testNow.Add(-time.Duration(i+1) * time.Minute)})
	}
	return s
}

// bigFleet builds n seats and m tasks over p projects, for the frame benchmark.
func bigFleet(p, seats, tasks int) Fleet {
	var f Fleet
	order := []string{BucketNeedsYou, BucketWorking, BucketReady, BucketInactive}
	for pi := 0; pi < p; pi++ {
		s := testSquad(fmt.Sprintf("/work/project-%02d", pi))
		for i := 0; i < tasks/p; i++ {
			id := fmt.Sprintf("t%d", i)
			s.Tasks = append(s.Tasks, Task{ID: id, Title: fmt.Sprintf("task %s of project %d with a title long enough to be clamped at the column edge", id, pi),
				Bucket: order[i%4], State: "online", LeaseHolder: fmt.Sprintf("seat-%d", i%(seats/p)), ProjectPath: s.ProjectPath,
				UpdatedAt: testNow.Add(-time.Duration(i) * time.Minute)})
		}
		for i := 0; i < seats/p; i++ {
			s.Seats = append(s.Seats, Seat{Handle: fmt.Sprintf("seat-%d", i), Role: "implementer", Model: "m", Effort: "medium",
				Bucket: order[i%4], ProjectPath: s.ProjectPath, LastActivityAt: ptrTime(testNow.Add(-time.Duration(i) * time.Second)), PromptState: "idle"})
		}
		f.Squads = append(f.Squads, s)
	}
	return f
}
