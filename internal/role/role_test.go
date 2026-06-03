package role

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureStubWritesAllSections(t *testing.T) {
	dir := t.TempDir()
	stub := Stub{
		Label:       "Product Designer",
		RoleID:      "designer",
		Description: "Designs the product surface.",
		Skills:      []string{"/frontend-design", "/canvas-design"},
		Peers:       []string{"cpo", "fullstack"},
	}
	wrote, err := EnsureStub(dir, stub)
	if err != nil {
		t.Fatalf("EnsureStub: %v", err)
	}
	if !wrote {
		t.Fatal("EnsureStub returned false on first call")
	}
	if _, err := os.Stat(Path(dir)); err != nil {
		t.Fatalf("EnsureStub did not create extension role file: %v", err)
	}
	if _, err := os.Stat(LegacyPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("EnsureStub created legacy role file, err=%v", err)
	}

	b, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)
	for _, want := range []string{
		"# Role: Product Designer",
		"## Description",
		"Designs the product surface.",
		"## Peers",
		"- cpo",
		"- fullstack",
		"## Skills",
		"/frontend-design",
		"/canvas-design",
		"amq-squad for team setup",
		"amq-cli only for raw AMQ debugging",
		"## System Prompt",
		"Use the binary default system behavior",
		"start by identifying your role and handle",
		"## Priming Template",
		"At launch, amq-squad injects identity",
		"start your first response by stating your role and handle",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("role.md missing %q", want)
		}
	}
	if strings.Contains(body, "TODO") {
		t.Fatalf("role.md should not include placeholder TODOs:\n%s", body)
	}
}

func TestEnsureStubNoClobber(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(Path(dir)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(dir), []byte("USER EDIT"), 0o600); err != nil {
		t.Fatal(err)
	}
	wrote, err := EnsureStub(dir, Stub{RoleID: "cpo", Label: "CPO"})
	if err != nil {
		t.Fatalf("EnsureStub: %v", err)
	}
	if wrote {
		t.Error("EnsureStub wrote over existing role.md")
	}
	b, _ := os.ReadFile(Path(dir))
	if string(b) != "USER EDIT" {
		t.Error("existing content clobbered")
	}
}

func TestEnsureStubFallsBackToRoleID(t *testing.T) {
	dir := t.TempDir()
	if _, err := EnsureStub(dir, Stub{RoleID: "unknown"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(Path(dir))
	if !strings.Contains(string(b), "# Role: unknown") {
		t.Error("stub should fall back to RoleID when Label is empty")
	}
	body := string(b)
	if !strings.Contains(body, "No catalog description is configured for this custom role") {
		t.Error("missing description should emit default guidance")
	}
	if !strings.Contains(body, "Use `amq-squad` for team setup") {
		t.Error("missing skills should emit amq-squad guidance")
	}
	if strings.Contains(body, "TODO") {
		t.Fatalf("fallback stub should not include TODO markers:\n%s", body)
	}
}

func TestExistingPathFallsBackToLegacyRole(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(LegacyPath(dir), []byte("USER EDIT"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := ExistingPath(dir); got != LegacyPath(dir) {
		t.Fatalf("ExistingPath = %q, want %q", got, LegacyPath(dir))
	}
	if !Exists(dir) {
		t.Fatal("Exists returned false for legacy role file")
	}
	wrote, err := EnsureStub(dir, Stub{RoleID: "qa", Label: "QA"})
	if err != nil {
		t.Fatalf("EnsureStub: %v", err)
	}
	if wrote {
		t.Fatal("EnsureStub wrote over legacy role file")
	}
	if _, err := os.Stat(Path(dir)); !os.IsNotExist(err) {
		t.Fatalf("EnsureStub created extension role despite legacy role, err=%v", err)
	}
}

func TestEnsureStubUpgradesUntouchedLegacyPlaceholder(t *testing.T) {
	dir := t.TempDir()
	stub := Stub{
		Label:       "QA Manager",
		RoleID:      "qa",
		Description: "Owns test strategy.",
		Peers:       []string{"cto", "fullstack"},
	}
	if err := os.MkdirAll(filepath.Dir(Path(dir)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(dir), []byte(renderLegacyPlaceholder(stub)), 0o600); err != nil {
		t.Fatal(err)
	}

	wrote, err := EnsureStub(dir, stub)
	if err != nil {
		t.Fatalf("EnsureStub: %v", err)
	}
	if !wrote {
		t.Fatal("legacy generated placeholder should be upgraded")
	}
	b, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)
	if strings.Contains(body, "TODO") {
		t.Fatalf("upgraded stub should not include TODO markers:\n%s", body)
	}
	if !strings.Contains(body, "No role-specific slash skills are configured") {
		t.Fatalf("upgraded stub missing default skills guidance:\n%s", body)
	}
	if !strings.Contains(body, "Use `amq-squad` for team setup") {
		t.Fatalf("upgraded stub missing amq-squad guidance:\n%s", body)
	}
}
