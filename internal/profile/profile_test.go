package profile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHasPlaceholders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, []byte(`{"server":"TODO_SERVER_IP_OR_HOST","uuid":"real"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	has, markers, err := HasPlaceholders(path)
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Fatal("expected placeholder detection")
	}
	if len(markers) != 1 || markers[0] != "TODO_SERVER_IP_OR_HOST" {
		t.Fatalf("markers = %#v", markers)
	}
}

func TestCopyActivatorRollback(t *testing.T) {
	dir := t.TempDir()
	oldProfile := filepath.Join(dir, "old.json")
	newProfile := filepath.Join(dir, "new.json")
	active := filepath.Join(dir, "config.json")
	activeName := filepath.Join(dir, "active-profile")

	if err := os.WriteFile(oldProfile, []byte(`{"name":"old"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newProfile, []byte(`{"name":"new"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(active, []byte(`{"name":"old"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(activeName, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rollback, err := (CopyActivator{ActiveConfigPath: active, ActiveNamePath: activeName}).Activate(newProfile)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(active)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"name":"new"}` {
		t.Fatalf("active config = %s", data)
	}
	if err := rollback.Rollback(); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(active)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"name":"old"}` {
		t.Fatalf("rolled back config = %s", data)
	}
	name, err := os.ReadFile(activeName)
	if err != nil {
		t.Fatal(err)
	}
	if string(name) != "old\n" {
		t.Fatalf("rolled back active name = %q", name)
	}
}
