package platform

import (
	"os"
	"strings"
	"testing"
)

func TestRuntimePaths(t *testing.T) {
	darwin := Darwin()
	if darwin.ProfilesDir != "/usr/local/etc/sing-box/profiles" {
		t.Fatalf("darwin profiles dir = %s", darwin.ProfilesDir)
	}
	linux := Linux()
	if linux.ProfilesDir != "/etc/sing-box/profiles" {
		t.Fatalf("linux profiles dir = %s", linux.ProfilesDir)
	}
	windows := Windows()
	if windows.ActiveNamePath == "" {
		t.Fatal("windows active name path is empty")
	}
	if windows.ErrorLogPath == "" || !strings.Contains(windows.ErrorLogPath, `sing-box`) {
		t.Fatalf("windows error log path = %s", windows.ErrorLogPath)
	}
	if _, ok := windows.LogFollower.(StreamingFileFollower); !ok {
		t.Fatalf("windows log follower = %T, want StreamingFileFollower", windows.LogFollower)
	}
}

func TestLastLinesOffset(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "log")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	if _, err := file.WriteString("one\ntwo\nthree\nfour\n"); err != nil {
		t.Fatal(err)
	}

	offset, err := LastLinesOffset(file, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(offset, 0); err != nil {
		t.Fatal(err)
	}
	tail, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	got := string(tail[offset:])
	if got != "three\nfour\n" {
		t.Fatalf("tail = %q", got)
	}
}
