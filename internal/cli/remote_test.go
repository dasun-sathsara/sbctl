package cli

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sbctl/internal/profile"
)

const remoteValidBody = `{"inbounds":[{"type":"tun","interface_name":"tun0"}],"outbounds":[{"type":"vless","server":"host.example","server_port":443}]}`

// serveBody starts a test server replying 200 with body.
func serveBody(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
}

func TestPullDownloadsValidatesAndRecordsSource(t *testing.T) {
	h := newHarness(t)
	srv := serveBody(t, remoteValidBody)
	defer srv.Close()

	h.assertExit(h.run("pull", "gcp", srv.URL+"/sb.json"), ExitOK)
	h.assertContains("pulled gcp")
	h.assertContains("sbctl use gcp")

	got, err := os.ReadFile(filepath.Join(h.dir, "gcp.json"))
	if err != nil || string(got) != remoteValidBody {
		t.Fatalf("stored profile = %q, %v; want served body", got, err)
	}
	url, err := profile.ReadSource(h.dir, "gcp")
	if err != nil || url != srv.URL+"/sb.json" {
		t.Fatalf("recorded source = %q, %v", url, err)
	}
	if len(h.checker.Checked) != 1 {
		t.Fatalf("checker runs = %d, want 1", len(h.checker.Checked))
	}
}

func TestPullRefusesExistingProfile(t *testing.T) {
	h := newHarness(t)
	h.validProfile("work")
	srv := serveBody(t, remoteValidBody)
	defer srv.Close()

	h.assertExit(h.run("pull", "work", srv.URL+"/sb.json"), ExitError)
	h.assertContains("already exists")
	h.assertContains("sbctl sync work")
}

func TestPullRejectsNonHTTPURL(t *testing.T) {
	h := newHarness(t)
	h.assertExit(h.run("pull", "work", "ftp://example.com/sb.json"), ExitError)
	h.assertContains("not a valid http(s) profile URL")
	h.assertExit(h.run("pull", "work", "just some words"), ExitError)
}

func TestPullReportsMissingServer(t *testing.T) {
	h := newHarness(t)
	// Nothing listens on this port; the fetch must fail, not hang or crash.
	h.assertExit(h.run("pull", "work", "http://127.0.0.1:1/sb.json"), ExitError)
	h.assertContains("could not download")
	if _, err := os.Stat(filepath.Join(h.dir, "work.json")); !os.IsNotExist(err) {
		t.Fatalf("failed pull left a profile behind: %v", err)
	}
}

func TestPullReportsValidationFailure(t *testing.T) {
	h := newHarness(t)
	h.checker.Err = errors.New("missing required field")
	srv := serveBody(t, remoteValidBody)
	defer srv.Close()

	h.assertExit(h.run("pull", "work", srv.URL+"/sb.json"), ExitValidation)
	h.assertContains("not a valid sing-box configuration")
	if _, err := os.Stat(filepath.Join(h.dir, "work.json")); !os.IsNotExist(err) {
		t.Fatalf("invalid pull left a profile behind: %v", err)
	}
}

func TestPullWarnsOnPlaceholders(t *testing.T) {
	h := newHarness(t)
	srv := serveBody(t, `{"outbounds":[{"server":"TODO_SERVER_IP_OR_HOST"}]}`)
	defer srv.Close()

	h.assertExit(h.run("pull", "work", srv.URL+"/sb.json"), ExitOK)
	h.assertContains("placeholders")
	h.assertContains("sbctl edit work")
}

func TestSyncRefreshesInactiveProfile(t *testing.T) {
	h := newHarness(t)
	h.validProfile("work")
	h.validProfile("other")
	h.activator.Active = "other"
	h.running(100, 0)
	srv := serveBody(t, remoteValidBody)
	defer srv.Close()
	mustWriteSource(t, h.dir, "work", srv.URL+"/sb.json")

	h.assertExit(h.run("sync", "work"), ExitOK)
	h.assertContains("synced work")

	got, _ := os.ReadFile(filepath.Join(h.dir, "work.json"))
	if string(got) != remoteValidBody {
		t.Fatalf("profile not refreshed")
	}
	// Inactive sync must not restart the service.
	if h.manager.Restarts != 0 {
		t.Fatalf("restarts = %d, want 0", h.manager.Restarts)
	}
}

func TestSyncReloadsActiveProfile(t *testing.T) {
	h := newHarness(t)
	h.validProfile("work")
	h.activator.Active = "work"
	h.running(100, 0)
	srv := serveBody(t, remoteValidBody)
	defer srv.Close()
	mustWriteSource(t, h.dir, "work", srv.URL+"/sb.json")

	h.assertExit(h.run("sync", "work"), ExitOK)
	h.assertContains("now using work")
	if h.manager.Restarts != 1 {
		t.Fatalf("restarts = %d, want 1", h.manager.Restarts)
	}
}

func TestSyncWithoutSourceExplains(t *testing.T) {
	h := newHarness(t)
	h.validProfile("work")

	h.assertExit(h.run("sync", "work"), ExitError)
	h.assertContains("no recorded source URL")
	h.assertContains("sbctl pull work <url>")
}

func TestSyncAllCoversOnlySourced(t *testing.T) {
	h := newHarness(t)
	h.validProfile("plain")
	h.validProfile("tracked")
	srv := serveBody(t, remoteValidBody)
	defer srv.Close()
	mustWriteSource(t, h.dir, "tracked", srv.URL+"/sb.json")

	h.assertExit(h.run("sync"), ExitOK)
	h.assertContains("synced tracked")
	if out := h.out.String() + h.err.String(); strings.Contains(out, "synced plain") {
		t.Fatalf("untracked profile was synced: %q", out)
	}
}

func TestSyncAllWithNothingExplains(t *testing.T) {
	h := newHarness(t)
	h.validProfile("plain")

	h.assertExit(h.run("sync"), ExitOK)
	h.assertContains("No profiles have a recorded source")
	h.assertContains("sbctl pull <name> <url>")
}

func TestSyncKeepsOldFileOnValidationFailure(t *testing.T) {
	h := newHarness(t)
	before := h.validProfile("work")
	srv := serveBody(t, remoteValidBody)
	defer srv.Close()
	mustWriteSource(t, h.dir, "work", srv.URL+"/sb.json")
	h.checker.Err = errors.New("missing required field")

	h.assertExit(h.run("sync", "work"), ExitValidation)
	got, _ := os.ReadFile(before)
	if !strings.Contains(string(got), "host.example") {
		t.Fatalf("stored profile was overwritten by invalid content")
	}
}

func TestRmRemovesSourceSidecar(t *testing.T) {
	h := newHarness(t)
	h.validProfile("work")
	mustWriteSource(t, h.dir, "work", "https://example.com/sb.json")

	h.assertExit(h.run("rm", "work", "--force"), ExitOK)
	if _, err := os.Stat(filepath.Join(h.dir, "work.url")); !os.IsNotExist(err) {
		t.Fatalf("sidecar survived rm: %v", err)
	}
}

func mustWriteSource(t *testing.T, dir, name, url string) {
	t.Helper()
	if err := profile.WriteSource(dir, name, url); err != nil {
		t.Fatal(err)
	}
}

func TestSyncActiveUsesHealthyRestart(t *testing.T) {
	h := newHarness(t)
	h.validProfile("work")
	h.activator.Active = "work"
	// Stable across probes: no crash loop, so activation succeeds.
	h.running(100, 0)
	srv := serveBody(t, remoteValidBody)
	defer srv.Close()
	mustWriteSource(t, h.dir, "work", srv.URL+"/sb.json")

	h.assertExit(h.run("sync", "work"), ExitOK)
	if h.activator.Active != "work" {
		t.Fatalf("active = %q, want work", h.activator.Active)
	}
}
