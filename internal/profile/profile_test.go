package profile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestValidateNameRejectsTraversal is the regression guard for unvalidated
// profile names. A name was previously joined straight onto the profiles
// directory, so a traversal sequence let `add` write and `rm` delete files
// outside the intended tree.
func TestValidateNameRejectsTraversal(t *testing.T) {
	rejected := []string{
		"",
		".",
		"..",
		"../../etc/passwd",
		"..",
		"sub/dir",
		`windows\path`,
		"/absolute",
		"has space",
		"emoji✨",
		"semi;colon",
		"$(whoami)",
		"-leading-dash",
		".hidden",
		"pipe|name",
		"new\nline",
		// Windows resolves these as devices in any directory: NUL.json discards
		// writes and CON.json blocks on the console.
		"nul",
		"NUL",
		"con",
		"Con.backup",
		"com1",
		"lpt9",
		// Windows strips trailing dots, which would collide with "work".
		"work.",
		strings.Repeat("x", 65),
	}
	for _, name := range rejected {
		t.Run("reject "+name, func(t *testing.T) {
			err := ValidateName(name)
			if err == nil {
				t.Fatalf("ValidateName(%q) = nil, want an error", name)
			}
			if !errors.Is(err, ErrInvalidName) {
				t.Fatalf("ValidateName(%q) error is not ErrInvalidName: %v", name, err)
			}
		})
	}

	accepted := []string{"work", "work-vpn", "work_vpn", "sg-cloudflare", "v2.profile", "a", strings.Repeat("x", 64)}
	for _, name := range accepted {
		t.Run("accept "+name, func(t *testing.T) {
			if err := ValidateName(name); err != nil {
				t.Fatalf("ValidateName(%q) = %v, want nil", name, err)
			}
		})
	}
}

// TestValidatedNameStaysInsideProfilesDir proves the property that matters:
// every accepted name resolves to a direct child of the profiles directory.
func TestValidatedNameStaysInsideProfilesDir(t *testing.T) {
	dir := "/etc/sing-box/profiles"
	for _, name := range []string{"work", "a.b.c", "x_y-z", "v2.profile"} {
		if err := ValidateName(name); err != nil {
			t.Fatalf("ValidateName(%q) = %v", name, err)
		}
		path := PathFor(dir, name)
		if filepath.Dir(path) != dir {
			t.Fatalf("PathFor(%q) = %q, which escapes %q", name, path, dir)
		}
	}
}

func TestPlaceholdersIn(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "detects markers generically",
			input: `{"server":"TODO_SERVER_IP_OR_HOST","uuid":"TODO_UUID"}`,
			want:  []string{"TODO_SERVER_IP_OR_HOST", "TODO_UUID"},
		},
		{
			// The scan is by prefix rather than a fixed list, so a marker added
			// to the template later is still caught instead of silently
			// bypassing the guard.
			name:  "detects markers the code has never seen before",
			input: `{"x":"TODO_SOMETHING_ENTIRELY_NEW"}`,
			want:  []string{"TODO_SOMETHING_ENTIRELY_NEW"},
		},
		{
			name:  "deduplicates repeats",
			input: `{"a":"TODO_UUID","b":"TODO_UUID"}`,
			want:  []string{"TODO_UUID"},
		},
		{
			name:  "clean config has none",
			input: `{"server":"example.com","uuid":"3f2a"}`,
			want:  nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := PlaceholdersIn([]byte(tc.input))
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("PlaceholdersIn() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestInterfaceNameAndServer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p.json")
	content := `{
	  "inbounds":[{"type":"tun","interface_name":"tun0"}],
	  "outbounds":[
	    {"type":"vless","server":"vpn.example.com","server_port":443},
	    {"type":"direct"}
	  ]
	}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tun, err := InterfaceName(path)
	if err != nil || tun != "tun0" {
		t.Fatalf("InterfaceName() = %q, %v", tun, err)
	}
	server, err := Server(path)
	if err != nil || server != "vpn.example.com:443" {
		t.Fatalf("Server() = %q, %v", server, err)
	}
}

// TestInterfaceNameEmptyIsNotAnError covers the template's deliberate empty
// interface_name: sing-box picks a name itself, so the absence is normal.
func TestInterfaceNameEmptyIsNotAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.json")
	if err := os.WriteFile(path, []byte(`{"inbounds":[{"type":"tun","interface_name":""}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	tun, err := InterfaceName(path)
	if err != nil {
		t.Fatalf("InterfaceName() = %v", err)
	}
	if tun != "" {
		t.Fatalf("InterfaceName() = %q, want empty", tun)
	}
}

func TestListSortsAndFlagsPlaceholders(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("zeta.json", `{"ok":true}`)
	write("alpha.json", `{"uuid":"TODO_UUID"}`)
	write("notes.txt", "ignored")
	if err := os.Mkdir(filepath.Join(dir, "nested.json"), 0o755); err != nil {
		t.Fatal(err)
	}

	profiles, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := Names(profiles); strings.Join(got, ",") != "alpha,zeta" {
		t.Fatalf("Names() = %v, want [alpha zeta]", got)
	}
	if profiles[0].Ready() {
		t.Error("alpha has a placeholder and must not be reported as ready")
	}
	if !profiles[1].Ready() {
		t.Error("zeta has no placeholders and should be ready")
	}
}

// TestListMissingDirectoryIsEmptyNotAnError covers a fresh install, where the
// profiles directory has not been created yet.
func TestListMissingDirectoryIsEmptyNotAnError(t *testing.T) {
	profiles, err := List(filepath.Join(t.TempDir(), "absent"))
	if err != nil {
		t.Fatalf("List() = %v, want nil", err)
	}
	if len(profiles) != 0 {
		t.Fatalf("List() = %v, want empty", profiles)
	}
}

// TestActivatorsAgreeWhenNothingIsActive pins the shared contract. The two
// implementations previously disagreed: one returned an empty string on a fresh
// install and the other returned an error, so callers needed platform-specific
// special cases.
func TestActivatorsAgreeWhenNothingIsActive(t *testing.T) {
	dir := t.TempDir()

	activators := map[string]Activator{
		"symlink": SymlinkActivator{ActiveConfigPath: filepath.Join(dir, "absent-config.json")},
		"copy": CopyActivator{
			ActiveConfigPath: filepath.Join(dir, "config.json"),
			ActiveNamePath:   filepath.Join(dir, "active-profile"),
		},
	}

	for name, activator := range activators {
		t.Run(name, func(t *testing.T) {
			gotName, err := activator.ActiveName()
			if err != nil {
				t.Fatalf("ActiveName() = %v, want nil error", err)
			}
			if gotName != "" {
				t.Fatalf("ActiveName() = %q, want empty", gotName)
			}
			gotPath, err := activator.ActivePath()
			if err != nil {
				t.Fatalf("ActivePath() = %v, want nil error", err)
			}
			if gotPath != "" {
				t.Fatalf("ActivePath() = %q, want empty", gotPath)
			}
		})
	}
}

// TestSymlinkActivatorTreatsRegularFileAsUnmanaged covers a host where the
// config was hand-written rather than created by sbctl.
func TestSymlinkActivatorTreatsRegularFileAsUnmanaged(t *testing.T) {
	active := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(active, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	activator := SymlinkActivator{ActiveConfigPath: active}

	name, err := activator.ActiveName()
	if err != nil {
		t.Fatalf("ActiveName() = %v", err)
	}
	if name != "" {
		t.Fatalf("ActiveName() = %q, want empty for an unmanaged config", name)
	}
}

func TestSymlinkActivateAndRollback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink activation is not used on Windows")
	}
	dir := t.TempDir()
	old := filepath.Join(dir, "old.json")
	next := filepath.Join(dir, "next.json")
	active := filepath.Join(dir, "config.json")
	for _, p := range []string{old, next} {
		if err := os.WriteFile(p, []byte(`{}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(old, active); err != nil {
		t.Fatal(err)
	}

	activator := SymlinkActivator{ActiveConfigPath: active}
	rollback, err := activator.Activate(next)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := activator.ActiveName(); got != "next" {
		t.Fatalf("ActiveName() = %q, want next", got)
	}
	if !rollback.Known() || rollback.Description() != "old" {
		t.Fatalf("rollback describes %q, known=%v", rollback.Description(), rollback.Known())
	}
	if err := rollback.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got, _ := activator.ActiveName(); got != "old" {
		t.Fatalf("after rollback ActiveName() = %q, want old", got)
	}
}

// TestSymlinkRollbackUnknownOnFreshInstall makes sure sbctl does not claim it
// can undo an activation when there was no prior state to return to.
func TestSymlinkRollbackUnknownOnFreshInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink activation is not used on Windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "first.json")
	if err := os.WriteFile(target, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	activator := SymlinkActivator{ActiveConfigPath: filepath.Join(dir, "config.json")}
	rollback, err := activator.Activate(target)
	if err != nil {
		t.Fatal(err)
	}
	if rollback.Known() {
		t.Fatal("there was no previous profile, so rollback must report itself unknown")
	}
	if err := rollback.Rollback(); err == nil {
		t.Fatal("rolling back to nothing should fail explicitly")
	}
}

func TestCopyActivateAndRollback(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.json")
	next := filepath.Join(dir, "next.json")
	active := filepath.Join(dir, "config.json")
	activeName := filepath.Join(dir, "active-profile")

	if err := os.WriteFile(old, []byte(`{"n":"old"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(next, []byte(`{"n":"next"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	activator := CopyActivator{ActiveConfigPath: active, ActiveNamePath: activeName}

	// Establish a prior state so the rollback has something to restore.
	if _, err := activator.Activate(old); err != nil {
		t.Fatal(err)
	}
	rollback, err := activator.Activate(next)
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(active)
	if err != nil || string(data) != `{"n":"next"}` {
		t.Fatalf("active config = %q, %v", data, err)
	}
	if got, _ := activator.ActiveName(); got != "next" {
		t.Fatalf("ActiveName() = %q, want next", got)
	}

	if err := rollback.Rollback(); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(active)
	if err != nil || string(data) != `{"n":"old"}` {
		t.Fatalf("after rollback active config = %q, %v", data, err)
	}
	if got, _ := activator.ActiveName(); got != "old" {
		t.Fatalf("after rollback ActiveName() = %q, want old", got)
	}
}

// TestCopyRollbackRestoresConfigWithNoNameRecorded covers a config that sbctl
// did not place: installed by hand, so no name file exists beside it. The
// content is still the user's working configuration and must be restorable,
// because refusing on the grounds that the label is missing would lose it.
func TestCopyRollbackRestoresConfigWithNoNameRecorded(t *testing.T) {
	dir := t.TempDir()
	next := filepath.Join(dir, "next.json")
	active := filepath.Join(dir, "config.json")
	activeName := filepath.Join(dir, "active-profile")

	if err := os.WriteFile(next, []byte(`{"n":"next"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// A config with no accompanying name file.
	if err := os.WriteFile(active, []byte(`{"n":"handmade"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	activator := CopyActivator{ActiveConfigPath: active, ActiveNamePath: activeName}
	rollback, err := activator.Activate(next)
	if err != nil {
		t.Fatal(err)
	}
	if !rollback.Known() {
		t.Fatal("Known() = false, but the previous config was captured and can be restored")
	}
	if got := rollback.Description(); got == "" {
		t.Error("Description() must never be empty, or the recovery message reads as truncated")
	}

	if err := rollback.Rollback(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(active)
	if err != nil || string(data) != `{"n":"handmade"}` {
		t.Fatalf("after rollback active config = %q, %v", data, err)
	}
	// Nothing was recorded before, so nothing must be reported as active now.
	if got, err := activator.ActiveName(); err != nil || got != "" {
		t.Fatalf("ActiveName() = %q, %v; want empty", got, err)
	}
}

// TestCopyActivateRestoresConfigWhenNameWriteFails pins the all-or-nothing
// contract: a caller that receives an error must be able to trust that the
// service is still reading what it was reading before.
func TestCopyActivateRestoresConfigWhenNameWriteFails(t *testing.T) {
	dir := t.TempDir()
	next := filepath.Join(dir, "next.json")
	active := filepath.Join(dir, "config.json")

	if err := os.WriteFile(next, []byte(`{"n":"next"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(active, []byte(`{"n":"old"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Put a regular file where the name file's parent directory would go, so
	// writing the name cannot succeed.
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}

	activator := CopyActivator{
		ActiveConfigPath: active,
		ActiveNamePath:   filepath.Join(blocker, "active-profile"),
	}
	if _, err := activator.Activate(next); err == nil {
		t.Fatal("Activate() = nil, want an error when the name file cannot be written")
	}

	data, err := os.ReadFile(active)
	if err != nil || string(data) != `{"n":"old"}` {
		t.Fatalf("active config = %q, %v; a failed activation must leave the old config in place", data, err)
	}
}

// TestCopyActivatorKeepsConfigPrivate checks the active config is not left
// world-readable. It carries credentials: VLESS UUIDs and Reality keys.
func TestCopyActivatorKeepsConfigPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits are not meaningful on Windows")
	}
	dir := t.TempDir()
	next := filepath.Join(dir, "next.json")
	if err := os.WriteFile(next, []byte(`{"n":"next"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	active := filepath.Join(dir, "config.json")
	activator := CopyActivator{
		ActiveConfigPath: active,
		ActiveNamePath:   filepath.Join(dir, "active-profile"),
	}
	if _, err := activator.Activate(next); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(active)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("active config mode = %#o, want no group or other access", perm)
	}
}

func TestCopyActivatorClear(t *testing.T) {
	dir := t.TempDir()
	activator := CopyActivator{
		ActiveConfigPath: filepath.Join(dir, "config.json"),
		ActiveNamePath:   filepath.Join(dir, "active-profile"),
	}
	target := filepath.Join(dir, "p.json")
	if err := os.WriteFile(target, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := activator.Activate(target); err != nil {
		t.Fatal(err)
	}
	if err := activator.Clear(); err != nil {
		t.Fatal(err)
	}
	if got, _ := activator.ActiveName(); got != "" {
		t.Fatalf("ActiveName() = %q after Clear, want empty", got)
	}
	// Clearing twice must be safe, so cleanup paths can run unconditionally.
	if err := activator.Clear(); err != nil {
		t.Fatalf("Clear() on already-cleared state = %v", err)
	}
}

// TestSymlinkActivatorUsesInjectedLink proves the privileged swap is routed
// through the injected hook, which is what lets the real implementation issue
// exactly the one command its sudo rule permits.
func TestSymlinkActivatorUsesInjectedLink(t *testing.T) {
	var got string
	activator := SymlinkActivator{
		ActiveConfigPath: "/etc/sing-box/config.json",
		Link: func(_ context.Context, target string) error {
			got = target
			return nil
		},
	}
	if _, err := activator.Activate("/etc/sing-box/profiles/work.json"); err != nil {
		t.Fatal(err)
	}
	if got != "/etc/sing-box/profiles/work.json" {
		t.Fatalf("link target = %q", got)
	}
}

func TestFakeCheckerRecordsAndFails(t *testing.T) {
	boom := errors.New("bad config")
	checker := &FakeChecker{Invalid: map[string]error{"/p/bad.json": boom}}

	if err := checker.Check(context.Background(), "/p/good.json"); err != nil {
		t.Fatalf("good config = %v", err)
	}
	if err := checker.Check(context.Background(), "/p/bad.json"); !errors.Is(err, boom) {
		t.Fatalf("bad config = %v", err)
	}
	if len(checker.Checked) != 2 {
		t.Fatalf("Checked = %v", checker.Checked)
	}
}
