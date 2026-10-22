package cli

import (
	"reflect"
	"strings"
	"testing"
)

func TestAgentArgsParseAgentArgs(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    []string
		wantErr string
	}{
		{name: "empty", raw: "", want: nil},
		{name: "whitespace only", raw: "  \t\n ", want: nil},
		{name: "single token", raw: "--yolo", want: []string{"--yolo"}},
		{name: "multiple tokens", raw: "--model opus --verbose", want: []string{"--model", "opus", "--verbose"}},
		{name: "extra whitespace", raw: "  a \t b\n c  ", want: []string{"a", "b", "c"}},
		{name: "double quotes group", raw: `--prompt "hello world"`, want: []string{"--prompt", "hello world"}},
		{name: "single quotes group", raw: `--prompt 'hello world'`, want: []string{"--prompt", "hello world"}},
		{name: "quotes join adjacent text", raw: `a"b c"d`, want: []string{"ab cd"}},
		{name: "empty quoted token kept", raw: `a "" b`, want: []string{"a", "", "b"}},
		{name: "single quotes keep backslash", raw: `'a\b'`, want: []string{`a\b`}},
		{name: "single quotes keep double quote", raw: `'say "hi"'`, want: []string{`say "hi"`}},
		{name: "double quotes keep single quote", raw: `"it's"`, want: []string{"it's"}},
		{name: "backslash escapes space", raw: `a\ b c`, want: []string{"a b", "c"}},
		{name: "backslash escapes quote", raw: `\"x`, want: []string{`"x`}},
		{name: "escape inside double quotes", raw: `"a\"b"`, want: []string{`a"b`}},
		{name: "trailing escape", raw: `abc\`, wantErr: "trailing escape"},
		{name: "unterminated double quote", raw: `"abc`, wantErr: "unterminated quote"},
		{name: "unterminated single quote", raw: `'abc`, wantErr: "unterminated quote"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAgentArgs(tt.raw)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parseAgentArgs(%q) error = %v, want containing %q", tt.raw, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseAgentArgs(%q) unexpected error: %v", tt.raw, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("parseAgentArgs(%q) = %#v, want %#v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestAgentArgsParseBinaryArgFlags(t *testing.T) {
	tests := []struct {
		name    string
		codex   string
		claude  string
		want    map[string][]string
		wantErr string
	}{
		{name: "both empty", want: map[string][]string{}},
		{name: "blank values ignored", codex: "  ", claude: "\t", want: map[string][]string{}},
		{name: "codex only", codex: "--a b", want: map[string][]string{"codex": {"--a", "b"}}},
		{name: "claude only", claude: "--c", want: map[string][]string{"claude": {"--c"}}},
		{
			name:   "both",
			codex:  `--a "b c"`,
			claude: "--d",
			want:   map[string][]string{"codex": {"--a", "b c"}, "claude": {"--d"}},
		},
		{name: "codex parse error names flag", codex: `"oops`, wantErr: "parse --codex-args"},
		{name: "claude parse error names flag", claude: `oops\`, wantErr: "parse --claude-args"},
		{name: "codex error wins before claude", codex: `"oops`, claude: `'x`, wantErr: "parse --codex-args"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseBinaryArgFlags(tt.codex, tt.claude)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
				}
				if got != nil {
					t.Fatalf("got = %#v on error, want nil", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestAgentArgsMergeBinaryArgs(t *testing.T) {
	tests := []struct {
		name  string
		base  map[string][]string
		extra map[string][]string
		want  map[string][]string
	}{
		{name: "both nil", want: nil},
		{name: "both empty", base: map[string][]string{}, extra: map[string][]string{}, want: nil},
		{name: "base only", base: map[string][]string{"codex": {"a"}}, want: map[string][]string{"codex": {"a"}}},
		{name: "extra only", extra: map[string][]string{"claude": {"b"}}, want: map[string][]string{"claude": {"b"}}},
		{
			name:  "same key appends extra after base",
			base:  map[string][]string{"codex": {"a", "b"}},
			extra: map[string][]string{"codex": {"c"}},
			want:  map[string][]string{"codex": {"a", "b", "c"}},
		},
		{
			name:  "keys are trimmed and lowercased",
			base:  map[string][]string{" Codex ": {"a"}},
			extra: map[string][]string{"CODEX": {"b"}, "Claude": {"c"}},
			want:  map[string][]string{"codex": {"a", "b"}, "claude": {"c"}},
		},
		{
			name:  "blank keys dropped",
			base:  map[string][]string{"": {"a"}, "  ": {"b"}},
			extra: map[string][]string{"codex": {"c"}, " ": {"d"}},
			want:  map[string][]string{"codex": {"c"}},
		},
		{
			name: "only blank keys yields empty non-nil map",
			base: map[string][]string{"": {"a"}},
			want: map[string][]string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeBinaryArgs(tt.base, tt.extra)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("mergeBinaryArgs = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestAgentArgsMergeBinaryArgsDoesNotAliasInputs(t *testing.T) {
	base := map[string][]string{"codex": make([]string, 1, 4)}
	base["codex"][0] = "a"
	extra := map[string][]string{"codex": {"b"}}

	got := mergeBinaryArgs(base, extra)
	got["codex"][0] = "changed"

	if base["codex"][0] != "a" {
		t.Fatalf("mutating merged result changed base: %#v", base)
	}
	if len(base["codex"]) != 1 || len(extra["codex"]) != 1 {
		t.Fatalf("inputs resized: base=%#v extra=%#v", base, extra)
	}
}

func TestAgentArgsFormatBinaryArgs(t *testing.T) {
	tests := []struct {
		name string
		in   map[string][]string
		want string
	}{
		{name: "nil", in: nil, want: ""},
		{name: "empty", in: map[string][]string{}, want: ""},
		{name: "only empty arg lists", in: map[string][]string{"codex": {}, "claude": nil}, want: ""},
		{name: "single binary", in: map[string][]string{"codex": {"--a", "b"}}, want: "codex: --a b"},
		{
			name: "binaries sorted by name",
			in:   map[string][]string{"codex": {"x"}, "claude": {"y"}},
			want: "claude: y, codex: x",
		},
		{name: "empty list skipped between others", in: map[string][]string{"claude": {"y"}, "codex": nil, "zed": {"z"}}, want: "claude: y, zed: z"},
		{name: "args shell quoted", in: map[string][]string{"claude": {"--prompt", "hello world", ""}}, want: "claude: --prompt 'hello world' ''"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatBinaryArgs(tt.in); got != tt.want {
				t.Fatalf("formatBinaryArgs = %q, want %q", got, tt.want)
			}
		})
	}
}
