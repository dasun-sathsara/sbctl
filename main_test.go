package main

import "testing"

func TestAppleScriptSafe(t *testing.T) {
	got := appleScriptSafe("switched to \"work\" ✓")
	want := "switched to 'work'"
	if got != want {
		t.Fatalf("appleScriptSafe() = %q, want %q", got, want)
	}
}
