package main

import (
	"strings"
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
	if _, _, err := cmd.Find([]string{"ip"}); err != nil {
		t.Fatalf("ip command missing: %v", err)
	}
}

func TestRenderIPInfo(t *testing.T) {
	got := renderIPInfo(ipInfo{
		IP:       "104.28.254.47",
		City:     "Singapore",
		Region:   "Singapore",
		Country:  "SG",
		Location: "1.2897,103.8501",
		Org:      "AS13335 Cloudflare, Inc.",
		Postal:   "018989",
		Timezone: "Asia/Singapore",
	})
	for _, want := range []string{
		"🌐 Public IP",
		"📍 IP:",
		"104.28.254.47",
		"🏢 Network:",
		"AS13335 Cloudflare, Inc.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("renderIPInfo() missing %q in:\n%s", want, got)
		}
	}
}
