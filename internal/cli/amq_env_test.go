package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAMQEnvAbsoluteAMQRoot(t *testing.T) {
	cwd := t.TempDir()
	abs := filepath.Join(t.TempDir(), "abs", "root")

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		cwd  string
		root string
		want string
	}{
		{name: "empty root", cwd: cwd, root: "", want: ""},
		{name: "blank root", cwd: cwd, root: "  \t", want: ""},
		{name: "absolute root is cleaned", cwd: cwd, root: abs + "/../root/", want: abs},
		{name: "relative root joins cwd", cwd: cwd, root: ".agent-mail", want: filepath.Join(cwd, ".agent-mail")},
		{name: "relative root is cleaned", cwd: cwd, root: "a/./b/../.agent-mail", want: filepath.Join(cwd, "a", ".agent-mail")},
		{name: "cwd is trimmed", cwd: "  " + cwd + "  ", root: "x", want: filepath.Join(cwd, "x")},
		{name: "empty cwd uses process dir", cwd: "", root: ".agent-mail", want: filepath.Join(wd, ".agent-mail")},
		{name: "blank cwd uses process dir", cwd: "   ", root: "r", want: filepath.Join(wd, "r")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := absoluteAMQRoot(tt.cwd, tt.root); got != tt.want {
				t.Fatalf("absoluteAMQRoot(%q, %q) = %q, want %q", tt.cwd, tt.root, got, tt.want)
			}
		})
	}
}

func TestAMQEnvChooseProjectBaseRoot(t *testing.T) {
	mkdir := func(t *testing.T, parts ...string) string {
		t.Helper()
		p := filepath.Join(parts...)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}

	t.Run("prefers existing base_root named .agent-mail", func(t *testing.T) {
		proj := t.TempDir()
		base := mkdir(t, proj, "custom", ".agent-mail")
		mkdir(t, proj, ".agent-mail")
		got := chooseProjectBaseRoot(proj, amqEnv{BaseRoot: base, Root: filepath.Join(proj, ".agent-mail", "s")})
		if got != base {
			t.Fatalf("got %q, want %q", got, base)
		}
	})

	t.Run("relative base_root resolves against project dir", func(t *testing.T) {
		proj := t.TempDir()
		want := mkdir(t, proj, ".agent-mail")
		got := chooseProjectBaseRoot(proj, amqEnv{BaseRoot: ".agent-mail"})
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("in-session base_root pointing at parent falls through to root", func(t *testing.T) {
		proj := t.TempDir()
		container := mkdir(t, proj, ".agent-mail")
		got := chooseProjectBaseRoot(proj, amqEnv{BaseRoot: proj, Root: container})
		if got != container {
			t.Fatalf("got %q, want %q", got, container)
		}
	})

	t.Run("dot base_root falls through to conventional container", func(t *testing.T) {
		proj := t.TempDir()
		container := mkdir(t, proj, ".agent-mail")
		got := chooseProjectBaseRoot(proj, amqEnv{BaseRoot: ".", Root: "."})
		if got != container {
			t.Fatalf("got %q, want %q", got, container)
		}
	})

	t.Run("base_root named .agent-mail but missing on disk is skipped", func(t *testing.T) {
		proj := t.TempDir()
		container := mkdir(t, proj, ".agent-mail")
		missing := filepath.Join(proj, "gone", ".agent-mail")
		got := chooseProjectBaseRoot(proj, amqEnv{BaseRoot: missing})
		if got != container {
			t.Fatalf("got %q, want %q", got, container)
		}
	})

	t.Run("regular file named .agent-mail is not a container", func(t *testing.T) {
		proj := t.TempDir()
		file := filepath.Join(proj, ".agent-mail")
		if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		// Not a directory, so it is skipped and the base_root fallback applies.
		got := chooseProjectBaseRoot(proj, amqEnv{BaseRoot: file, Root: filepath.Join(proj, "other")})
		if got != file {
			t.Fatalf("got %q, want fallback %q", got, file)
		}
	})

	t.Run("no container falls back to cleaned base_root", func(t *testing.T) {
		proj := t.TempDir()
		custom := filepath.Join(proj, "custom-root")
		got := chooseProjectBaseRoot(proj, amqEnv{BaseRoot: custom + "/", Root: filepath.Join(proj, "other")})
		if got != custom {
			t.Fatalf("got %q, want %q", got, custom)
		}
	})

	t.Run("no container and empty base_root falls back to root", func(t *testing.T) {
		proj := t.TempDir()
		got := chooseProjectBaseRoot(proj, amqEnv{Root: "rel/root"})
		if want := filepath.Join(proj, "rel", "root"); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("nothing known yields empty", func(t *testing.T) {
		// No .agent-mail on disk and an empty env: only the conventional
		// candidate exists as a name match, and it is absent.
		proj := t.TempDir()
		if got := chooseProjectBaseRoot(proj, amqEnv{}); got != "" {
			t.Fatalf("got %q, want empty", got)
		}
	})
}

// stubAMQ puts a fake `amq` first on PATH. It prints stdout and exits with code.
func stubAMQ(t *testing.T, stdout string, code int) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell stub needs a POSIX shell")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\ncat <<'JSON'\n" + stdout + "\nJSON\nexit " + string(rune('0'+code)) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "amq"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestAMQEnvScanBaseRootForProject(t *testing.T) {
	t.Run("uses container reported by amq env", func(t *testing.T) {
		proj := t.TempDir()
		container := filepath.Join(proj, ".agent-mail")
		if err := os.MkdirAll(container, 0o755); err != nil {
			t.Fatal(err)
		}
		// In-session shape: base_root is the parent, root is the container.
		stubAMQ(t, `{"root":"`+container+`","base_root":"`+proj+`"}`, 0)
		got, err := scanBaseRootForProject(proj)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != container {
			t.Fatalf("got %q, want %q", got, container)
		}
	})

	t.Run("falls back to base_root when no container exists", func(t *testing.T) {
		proj := t.TempDir()
		custom := filepath.Join(proj, "custom-root")
		stubAMQ(t, `{"root":"`+custom+`"}`, 0)
		got, err := scanBaseRootForProject(proj)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// resolveAMQEnvInDir defaults an empty base_root to root.
		if got != custom {
			t.Fatalf("got %q, want %q", got, custom)
		}
	})

	t.Run("amq failure is returned", func(t *testing.T) {
		stubAMQ(t, "", 1)
		if _, err := scanBaseRootForProject(t.TempDir()); err == nil || !strings.Contains(err.Error(), "amq env") {
			t.Fatalf("error = %v, want containing %q", err, "amq env")
		}
	})

	t.Run("invalid json is returned", func(t *testing.T) {
		stubAMQ(t, "not json", 0)
		if _, err := scanBaseRootForProject(t.TempDir()); err == nil || !strings.Contains(err.Error(), "parse amq env output") {
			t.Fatalf("error = %v, want containing %q", err, "parse amq env output")
		}
	})

	t.Run("empty root is rejected", func(t *testing.T) {
		stubAMQ(t, `{"base_root":"/x"}`, 0)
		if _, err := scanBaseRootForProject(t.TempDir()); err == nil || !strings.Contains(err.Error(), "empty root") {
			t.Fatalf("error = %v, want containing %q", err, "empty root")
		}
	})
}
