package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omriariav/amq-noc/internal/launch"
)

func TestListSortRestorableEntriesOrdersByRoleHandleSource(t *testing.T) {
	mk := func(role, handle, source string) launch.Entry {
		return launch.Entry{Record: launch.Record{Role: role, Handle: handle}, Source: source}
	}
	entries := []launch.Entry{
		mk("qa", "qa", "/b"),
		mk("dev", "zed", "/a"),
		mk("dev", "amy", "/z"),
		mk("dev", "amy", "/a"),
	}
	sortRestorableEntries(entries)

	want := [][3]string{
		{"dev", "amy", "/a"},
		{"dev", "amy", "/z"},
		{"dev", "zed", "/a"},
		{"qa", "qa", "/b"},
	}
	for i, w := range want {
		got := [3]string{entries[i].Record.Role, entries[i].Record.Handle, entries[i].Source}
		if got != w {
			t.Fatalf("entries[%d] = %v, want %v", i, got, w)
		}
	}
}

func TestListFormatListTime(t *testing.T) {
	if got := formatListTime(time.Time{}); got != "" {
		t.Fatalf("formatListTime(zero) = %q, want empty", got)
	}
	ts := time.Date(2026, 3, 7, 9, 5, 59, 0, time.UTC)
	if got, want := formatListTime(ts), "2026-03-07 09:05"; got != want {
		t.Fatalf("formatListTime = %q, want %q", got, want)
	}
}

func TestListPresencePath(t *testing.T) {
	dir := filepath.Join("root", "agents", "dev")
	if got, want := presencePath(dir), filepath.Join(dir, "presence.json"); got != want {
		t.Fatalf("presencePath = %q, want %q", got, want)
	}
}

func TestListLooksActive(t *testing.T) {
	now := time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC)
	probe := func(alive, match bool) duplicateLaunchProbe {
		return duplicateLaunchProbe{
			PIDAlive:     func(int) bool { return alive },
			ProcessMatch: func(int, func(string) bool) bool { return match },
			Now:          func() time.Time { return now },
		}
	}
	writePresence := func(t *testing.T, dir, body string) {
		t.Helper()
		if err := os.WriteFile(presencePath(dir), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fresh := now.Add(-10 * time.Second).Format(time.RFC3339)
	stale := now.Add(-presenceFreshness - time.Minute).Format(time.RFC3339)

	tests := []struct {
		name     string
		pid      int
		binary   string
		presence string
		probe    duplicateLaunchProbe
		want     bool
	}{
		{"live pid with matching binary", 42, "claude", "", probe(true, true), true},
		{"live pid without binary skips process match", 42, "", "", probe(true, false), true},
		{"live pid but process mismatch and no presence", 42, "claude", "", probe(true, false), false},
		{"dead pid and no presence", 42, "claude", "", probe(false, true), false},
		{"no pid and no presence file", 0, "claude", "", probe(true, true), false},
		{"fresh active presence", 0, "claude", `{"status":"active","last_seen":"` + fresh + `"}`, probe(false, false), true},
		{"fresh presence status is case-insensitive", 0, "claude", `{"status":"ACTIVE","last_seen":"` + fresh + `"}`, probe(false, false), true},
		{"fresh presence but not active", 0, "claude", `{"status":"idle","last_seen":"` + fresh + `"}`, probe(false, false), false},
		{"stale active presence", 0, "claude", `{"status":"active","last_seen":"` + stale + `"}`, probe(false, false), false},
		{"active presence without last_seen", 0, "claude", `{"status":"active"}`, probe(false, false), false},
		{"unparseable presence", 0, "claude", `{not json`, probe(false, false), false},
		{"dead pid falls back to fresh presence", 42, "claude", `{"status":"active","last_seen":"` + fresh + `"}`, probe(false, false), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.presence != "" {
				writePresence(t, dir, tt.presence)
			}
			e := launch.Entry{
				Record:   launch.Record{Binary: tt.binary, AgentPID: tt.pid},
				AgentDir: dir,
			}
			if got := looksActive(e, tt.probe); got != tt.want {
				t.Fatalf("looksActive = %v, want %v", got, tt.want)
			}
		})
	}
}
