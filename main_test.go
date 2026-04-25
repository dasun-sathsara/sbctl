package main

import (
	"testing"

	"sbctl/internal/platform"
)

func TestAppleScriptSafe(t *testing.T) {
	got := appleScriptSafe("switched to \"work\" ✓")
	want := "switched to 'work'"
	if got != want {
		t.Fatalf("appleScriptSafe() = %q, want %q", got, want)
	}
}

func TestRootCommandUsesInjectedRuntime(t *testing.T) {
	cmd := newRootCmd(platform.Linux())
	if cmd.Short != "Manage sing-box profiles" {
		t.Fatalf("short = %q", cmd.Short)
	}
	if _, _, err := cmd.Find([]string{"logs"}); err != nil {
		t.Fatalf("logs command missing: %v", err)
	}
}
