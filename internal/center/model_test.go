package center

import (
	"testing"
)

// Acceptance 3: a fixture with one item per bucket shows the right count in
// each tab, and All equals the sum.
func TestBucketCountsOnePerBucket(t *testing.T) {
	f := Fleet{Squads: []Status{testSquad("/p/a", BucketNeedsYou, BucketWorking, BucketReady, BucketInactive)}}
	rows := BuildRows(f)
	c := Counts(rows)
	want := [tabCount]int{4, 1, 1, 1, 1}
	if c != want {
		t.Fatalf("counts = %v, want %v", c, want)
	}
	if c[TabAll] != c[TabNeedsYou]+c[TabWorking]+c[TabReady]+c[TabInactive] {
		t.Fatalf("All is not the sum: %v", c)
	}
	for tab := TabNeedsYou; tab < tabCount; tab++ {
		got := Filter(rows, tab)
		if len(got) != 1 || got[0].Bucket != tabBucket[tab] {
			t.Fatalf("tab %d filter = %+v", tab, got)
		}
	}
}

// The renderer takes the bucket from the contract: a task whose state says
// online but whose bucket says needs_you is a Needs you row.
func TestBucketIsNotReclassified(t *testing.T) {
	s := testSquad("/p/a", BucketNeedsYou)
	s.Tasks[0].State = "online"
	rows := BuildRows(Fleet{Squads: []Status{s}})
	if rows[0].Bucket != BucketNeedsYou {
		t.Fatalf("bucket = %s", rows[0].Bucket)
	}
}

// An unknown row is Inactive with a marker (briefs ruling 6).
func TestUnknownRendersInactiveWithMarker(t *testing.T) {
	s := testSquad("/p/a", BucketInactive)
	s.Tasks[0].State = "unknown"
	s.Tasks[0].BucketMarker = MarkerUnknown
	f := Fleet{Squads: []Status{s}}
	rows := BuildRows(f)
	if got := StatusLabel(f, rows[0]); got != "Inactive (unknown)" {
		t.Fatalf("label = %q", got)
	}
	if Counts(rows)[TabInactive] != 1 {
		t.Fatal("unknown not counted as inactive")
	}
}

// Seats without a task are their own rows; a seat that holds a task is shown
// through the task row only.
func TestSeatRows(t *testing.T) {
	f, err := Parse(goldenFixture)
	if err != nil {
		t.Fatal(err)
	}
	rows := BuildRows(f)
	// golden: tasks t1, t2 plus seat impl-2 (no task); impl-1 holds t1.
	if len(rows) != 3 {
		t.Fatalf("rows = %d", len(rows))
	}
	var seatRows int
	for _, r := range rows {
		if !r.IsTask() {
			seatRows++
			if r.Seat.Handle != "impl-2" {
				t.Fatalf("seat row %s", r.Seat.Handle)
			}
		} else if r.Task.ID == "t1" && (r.Seat == nil || r.Seat.Handle != "impl-1") {
			t.Fatal("task t1 not joined to impl-1")
		}
	}
	if seatRows != 1 {
		t.Fatalf("seat rows = %d", seatRows)
	}
	c := Counts(rows)
	if c != ([tabCount]int{3, 1, 1, 1, 0}) {
		t.Fatalf("golden counts = %v (contract counts are 3/1/1/1/0)", c)
	}
}

func TestRelTime(t *testing.T) {
	for _, tc := range []struct {
		ago  int // seconds
		want string
	}{{38, "38s ago"}, {300, "5m ago"}, {3 * 3600, "3h ago"}, {86400, "24h ago"}, {3 * 86400, "3d ago"}} {
		ts := testNow.Add(-secs(tc.ago))
		if got := RelTime(&ts, testNow); got != tc.want {
			t.Errorf("%ds: %q want %q", tc.ago, got, tc.want)
		}
	}
	if RelTime(nil, testNow) != "-" {
		t.Error("nil time must be a dash")
	}
}
