package profile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSourceRoundTrip(t *testing.T) {
	dir := t.TempDir()

	if url, err := ReadSource(dir, "work"); err != nil || url != "" {
		t.Fatalf("ReadSource on missing sidecar = %q, %v; want empty, nil", url, err)
	}

	if err := WriteSource(dir, "work", "https://example.com/sb.json"); err != nil {
		t.Fatalf("WriteSource: %v", err)
	}
	url, err := ReadSource(dir, "work")
	if err != nil || url != "https://example.com/sb.json" {
		t.Fatalf("ReadSource = %q, %v; want URL, nil", url, err)
	}

	names, err := Sourced(dir)
	if err != nil || len(names) != 1 || names[0] != "work" {
		t.Fatalf("Sourced = %v, %v; want [work], nil", names, err)
	}

	// A stray .url file that is not a valid profile name must not surface.
	// ".url" has an empty name, which ValidateName rejects.
	if err := os.WriteFile(filepath.Join(dir, ".url"), []byte("https://example.com"), 0o644); err != nil {
		t.Fatal(err)
	}
	names, err = Sourced(dir)
	if err != nil || len(names) != 1 {
		t.Fatalf("Sourced with stray file = %v, %v; want [work], nil", names, err)
	}

	if err := RemoveSource(dir, "work"); err != nil {
		t.Fatalf("RemoveSource: %v", err)
	}
	if err := RemoveSource(dir, "work"); err != nil {
		t.Fatalf("RemoveSource on missing sidecar: %v", err)
	}
	if url, _ := ReadSource(dir, "work"); url != "" {
		t.Fatalf("ReadSource after remove = %q; want empty", url)
	}
}
