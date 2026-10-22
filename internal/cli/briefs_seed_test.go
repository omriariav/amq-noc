package cli

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// stubSeedGh swaps the gh seam for fn and returns the argument lists it saw.
func stubSeedGh(t *testing.T, fn func(args ...string) ([]byte, error)) *[][]string {
	t.Helper()
	var calls [][]string
	orig := seedGhRun
	seedGhRun = func(args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		return fn(args...)
	}
	t.Cleanup(func() { seedGhRun = orig })
	return &calls
}

func TestSeedRenderIssueBody(t *testing.T) {
	tests := []struct {
		name             string
		title, url, body string
		want             string
	}{
		{
			name:  "title url and body",
			title: "Fix the thing", url: "https://github.com/o/r/issues/7", body: "Details here.",
			want: "# Fix the thing\n\nURL: https://github.com/o/r/issues/7\n\nDetails here.\n",
		},
		{name: "no title", url: "https://x/1", body: "b", want: "URL: https://x/1\n\nb\n"},
		{name: "no url", title: "T", body: "b", want: "# T\n\nb\n"},
		{name: "body only", body: "just body", want: "just body\n"},
		{name: "all empty", want: "\n"},
		{name: "trailing newlines collapse to one", title: "T", body: "line\n\n\n", want: "# T\n\nline\n"},
		{name: "leading whitespace and inner newlines preserved", body: "\n  a\n\nb\n", want: "\n  a\n\nb\n"},
		{name: "empty body keeps headers", title: "T", url: "u", want: "# T\n\nURL: u\n\n\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := renderIssueBody(tt.title, tt.url, tt.body); got != tt.want {
				t.Fatalf("renderIssueBody = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSeedResolveSeedIssueLocalRepo(t *testing.T) {
	calls := stubSeedGh(t, func(args ...string) ([]byte, error) {
		return []byte(`{"title":"Add CI","body":"Run gofmt.\n","url":"https://github.com/o/r/issues/57"}`), nil
	})

	got, err := resolveSeedIssue("", " 57 ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "# Add CI\n\nURL: https://github.com/o/r/issues/57\n\nRun gofmt.\n"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
	wantArgs := []string{"issue", "view", "57", "--json", "title,body,url"}
	if len(*calls) != 1 || !reflect.DeepEqual((*calls)[0], wantArgs) {
		t.Fatalf("gh calls = %#v, want one call %#v", *calls, wantArgs)
	}
}

func TestSeedResolveSeedIssueExplicitRepo(t *testing.T) {
	calls := stubSeedGh(t, func(args ...string) ([]byte, error) {
		return []byte(`{"title":"T","body":"B","url":"U"}`), nil
	})

	if _, err := resolveSeedIssue("owner/repo", "9"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantArgs := []string{"issue", "view", "9", "--json", "title,body,url", "--repo", "owner/repo"}
	if len(*calls) != 1 || !reflect.DeepEqual((*calls)[0], wantArgs) {
		t.Fatalf("gh calls = %#v, want one call %#v", *calls, wantArgs)
	}
}

func TestSeedResolveSeedIssueRejectsBadNumberWithoutCallingGh(t *testing.T) {
	calls := stubSeedGh(t, func(args ...string) ([]byte, error) {
		t.Fatalf("gh must not be called, got %v", args)
		return nil, nil
	})

	tests := []struct {
		name, number, wantErr string
	}{
		{name: "empty", number: "", wantErr: "missing number"},
		{name: "blank", number: "   ", wantErr: "missing number"},
		{name: "not a number", number: "abc", wantErr: "not a number"},
		{name: "number with suffix", number: "12x", wantErr: "not a number"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := resolveSeedIssue("", tt.number)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
	if len(*calls) != 0 {
		t.Fatalf("gh called %d times, want 0", len(*calls))
	}
}

func TestSeedResolveSeedIssueGhFailure(t *testing.T) {
	boom := errors.New("exit status 1: gh: not logged in")
	stubSeedGh(t, func(args ...string) ([]byte, error) { return nil, boom })

	t.Run("local issue reference", func(t *testing.T) {
		_, err := resolveSeedIssue("", "5")
		if err == nil || !errors.Is(err, boom) {
			t.Fatalf("error = %v, want wrapping %v", err, boom)
		}
		if !strings.Contains(err.Error(), "--seed-from issue:5: gh:") {
			t.Fatalf("error = %q, want issue:5 reference", err)
		}
	})
	t.Run("repo qualified reference", func(t *testing.T) {
		_, err := resolveSeedIssue("o/r", "5")
		if err == nil || !errors.Is(err, boom) {
			t.Fatalf("error = %v, want wrapping %v", err, boom)
		}
		if !strings.Contains(err.Error(), "--seed-from gh:o/r#5: gh:") {
			t.Fatalf("error = %q, want gh:o/r#5 reference", err)
		}
	})
}

func TestSeedResolveSeedIssueBadGhJSON(t *testing.T) {
	stubSeedGh(t, func(args ...string) ([]byte, error) { return []byte("not json"), nil })

	_, err := resolveSeedIssue("", "3")
	if err == nil || !strings.Contains(err.Error(), "issue:3: parse gh output") {
		t.Fatalf("error = %v, want parse gh output error", err)
	}
}

func TestSeedResolveSeedRoutesIssueAndGhKinds(t *testing.T) {
	calls := stubSeedGh(t, func(args ...string) ([]byte, error) {
		return []byte(`{"title":"T","body":"B","url":"U"}`), nil
	})

	if got, err := resolveSeed("issue:12"); err != nil || got != "# T\n\nURL: U\n\nB\n" {
		t.Fatalf("resolveSeed(issue:12) = %q, %v", got, err)
	}
	if got, err := resolveSeed("gh:acme/widgets#34"); err != nil || got != "# T\n\nURL: U\n\nB\n" {
		t.Fatalf("resolveSeed(gh:acme/widgets#34) = %q, %v", got, err)
	}
	want := [][]string{
		{"issue", "view", "12", "--json", "title,body,url"},
		{"issue", "view", "34", "--json", "title,body,url", "--repo", "acme/widgets"},
	}
	if !reflect.DeepEqual(*calls, want) {
		t.Fatalf("gh calls = %#v, want %#v", *calls, want)
	}
}

func TestSeedResolveSeedGhShapeErrorsSkipGh(t *testing.T) {
	calls := stubSeedGh(t, func(args ...string) ([]byte, error) {
		t.Fatalf("gh must not be called, got %v", args)
		return nil, nil
	})

	tests := []struct{ ref, wantErr string }{
		{"gh:noissue", "want owner/repo#N"},
		{"gh:#5", "want owner/repo#N"},
		{"gh:o/r#", "want owner/repo#N"},
		{"gh:repo#5", "repo must look like owner/repo"},
		{"gh:o/r#x", "not a number"},
	}
	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			_, err := resolveSeed(tt.ref)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("resolveSeed(%q) error = %v, want containing %q", tt.ref, err, tt.wantErr)
			}
		})
	}
	if len(*calls) != 0 {
		t.Fatalf("gh called %d times, want 0", len(*calls))
	}
}
