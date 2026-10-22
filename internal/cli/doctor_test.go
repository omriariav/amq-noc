package cli

import "testing"

func TestDoctorParseSemverParts(t *testing.T) {
	tests := []struct {
		in     string
		want   [3]int
		wantOK bool
	}{
		{"1.2.3", [3]int{1, 2, 3}, true},
		{"0.0.0", [3]int{0, 0, 0}, true},
		{"v1.2.3", [3]int{1, 2, 3}, true},
		{"  v0.51.1  ", [3]int{0, 51, 1}, true},
		{"2.28.0-rc1", [3]int{2, 28, 0}, true},
		{"2.28.0+build.5", [3]int{2, 28, 0}, true},
		{"v2.28.0-rc.1+build", [3]int{2, 28, 0}, true},
		{"10.20.30", [3]int{10, 20, 30}, true},
		{"01.02.03", [3]int{1, 2, 3}, true},
		{"", [3]int{}, false},
		{"   ", [3]int{}, false},
		{"v", [3]int{}, false},
		{"1", [3]int{}, false},
		{"1.2", [3]int{}, false},
		{"1.2.", [3]int{}, false},
		{"1..3", [3]int{}, false},
		{"a.b.c", [3]int{}, false},
		{"1.2.x", [3]int{}, false},
		{"1.2.3.4", [3]int{}, false},
		{"vv1.2.3", [3]int{}, false},
		{"-rc1", [3]int{}, false},
		{"dev", [3]int{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, ok := parseSemverParts(tt.in)
			if ok != tt.wantOK {
				t.Fatalf("parseSemverParts(%q) ok = %v, want %v (parts %v)", tt.in, ok, tt.wantOK, got)
			}
			if ok && got != tt.want {
				t.Fatalf("parseSemverParts(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestDoctorCompareSemverParts(t *testing.T) {
	tests := []struct {
		name string
		a, b [3]int
		want int
	}{
		{"equal", [3]int{1, 2, 3}, [3]int{1, 2, 3}, 0},
		{"zero equal", [3]int{}, [3]int{}, 0},
		{"major less", [3]int{1, 9, 9}, [3]int{2, 0, 0}, -1},
		{"major greater", [3]int{3, 0, 0}, [3]int{2, 9, 9}, 1},
		{"minor less", [3]int{1, 2, 9}, [3]int{1, 3, 0}, -1},
		{"minor greater", [3]int{1, 3, 0}, [3]int{1, 2, 9}, 1},
		{"patch less", [3]int{1, 2, 3}, [3]int{1, 2, 4}, -1},
		{"patch greater", [3]int{1, 2, 4}, [3]int{1, 2, 3}, 1},
		{"numeric not lexical", [3]int{1, 10, 0}, [3]int{1, 9, 0}, 1},
		{"major outranks minor and patch", [3]int{2, 0, 0}, [3]int{1, 99, 99}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := compareSemverParts(tt.a, tt.b); got != tt.want {
				t.Fatalf("compareSemverParts(%v, %v) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
			// Comparison is antisymmetric.
			if got := compareSemverParts(tt.b, tt.a); got != -tt.want {
				t.Fatalf("compareSemverParts(%v, %v) = %d, want %d", tt.b, tt.a, got, -tt.want)
			}
		})
	}
}

func TestDoctorProfileSummaryStatus(t *testing.T) {
	tests := []struct {
		name         string
		fails, nonOK int
		want         doctorStatus
	}{
		{"all ok", 0, 0, doctorOK},
		{"warnings only", 0, 3, doctorWarn},
		{"one fail", 1, 1, doctorFail},
		{"fail outranks warnings", 2, 5, doctorFail},
		{"fail count without non-ok still fails", 1, 0, doctorFail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := doctorProfileSummaryStatus(tt.fails, tt.nonOK); got != tt.want {
				t.Fatalf("doctorProfileSummaryStatus(%d, %d) = %q, want %q", tt.fails, tt.nonOK, got, tt.want)
			}
		})
	}
}

func TestDoctorCountFailsAndNonOK(t *testing.T) {
	tests := []struct {
		name      string
		checks    []doctorCheck
		wantFails int
		wantNonOK int
	}{
		{"nil", nil, 0, 0},
		{"empty", []doctorCheck{}, 0, 0},
		{"all ok", []doctorCheck{{Status: doctorOK}, {Status: doctorOK}}, 0, 0},
		{"only warns", []doctorCheck{{Status: doctorWarn}, {Status: doctorOK}, {Status: doctorWarn}}, 0, 2},
		{"only fails", []doctorCheck{{Status: doctorFail}, {Status: doctorFail}}, 2, 2},
		{
			"mixed",
			[]doctorCheck{{Status: doctorOK}, {Status: doctorWarn}, {Status: doctorFail}, {Status: doctorOK}, {Status: doctorFail}},
			2, 3,
		},
		{"unknown status counts as non-ok but not fail", []doctorCheck{{Status: doctorStatus("skipped")}}, 0, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := countFails(tt.checks); got != tt.wantFails {
				t.Fatalf("countFails = %d, want %d", got, tt.wantFails)
			}
			if got := countNonOK(tt.checks); got != tt.wantNonOK {
				t.Fatalf("countNonOK = %d, want %d", got, tt.wantNonOK)
			}
			// The summary status must agree with the counts it is fed.
			want := doctorOK
			if tt.wantFails > 0 {
				want = doctorFail
			} else if tt.wantNonOK > 0 {
				want = doctorWarn
			}
			if got := doctorProfileSummaryStatus(countFails(tt.checks), countNonOK(tt.checks)); got != want {
				t.Fatalf("summary status = %q, want %q", got, want)
			}
		})
	}
}
