package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLayoutsUseTheContractedPaths(t *testing.T) {
	// These paths are a compatibility contract with every existing install:
	// changing one orphans a user's profiles, service registration or sudo
	// rules. The test exists to make such a change a deliberate act.
	darwin := Darwin()
	if darwin.ProfilesDir != "/usr/local/etc/sing-box/profiles" {
		t.Errorf("darwin profiles dir = %q", darwin.ProfilesDir)
	}
	if darwin.ActiveConfigPath != "/usr/local/etc/sing-box/config.json" {
		t.Errorf("darwin active config = %q", darwin.ActiveConfigPath)
	}
	if darwin.LaunchdLabel != "system/app.lexiflix.singbox" {
		t.Errorf("darwin launchd label = %q", darwin.LaunchdLabel)
	}
	if darwin.PlistPath != "/Library/LaunchDaemons/app.lexiflix.singbox.plist" {
		t.Errorf("darwin plist = %q", darwin.PlistPath)
	}

	linux := Linux()
	if linux.ProfilesDir != "/etc/sing-box/profiles" {
		t.Errorf("linux profiles dir = %q", linux.ProfilesDir)
	}
	if linux.ActiveConfigPath != "/etc/sing-box/config.json" {
		t.Errorf("linux active config = %q", linux.ActiveConfigPath)
	}
	if linux.ServiceName != "sing-box" {
		t.Errorf("linux service = %q", linux.ServiceName)
	}

	windows := Windows()
	if windows.ActiveNamePath == "" {
		t.Error("windows must record the active profile name, since a copy carries no back-reference")
	}
	if !strings.Contains(windows.ProfilesDir, "sing-box") {
		t.Errorf("windows profiles dir = %q", windows.ProfilesDir)
	}
}

func TestWindowsLayoutHonoursProgramData(t *testing.T) {
	t.Setenv("ProgramData", filepath.Join("D:", "Data"))
	layout := Windows()
	if !strings.HasPrefix(layout.ProfilesDir, filepath.Join("D:", "Data")) {
		t.Fatalf("ProfilesDir = %q, want it under the ProgramData override", layout.ProfilesDir)
	}
}

func TestUsesSudo(t *testing.T) {
	if !Darwin().UsesSudo() || !Linux().UsesSudo() {
		t.Error("Unix platforms escalate per-operation through sudo")
	}
	if Windows().UsesSudo() {
		t.Error("Windows elevates the whole process instead of using sudo")
	}
}

func TestFollowerSelection(t *testing.T) {
	// journalctl needs no file. Gating it on one is what previously broke
	// `sbctl logs` on Linux entirely.
	linux := Linux().Follower()
	if _, required := linux.NeedsFile(); required {
		t.Error("the journald follower must not require a log file")
	}
	if _, ok := linux.(CommandFollower); !ok {
		t.Errorf("linux follower = %T, want CommandFollower", linux)
	}

	darwin := Darwin().Follower()
	path, required := darwin.NeedsFile()
	if !required || path == "" {
		t.Error("the file follower must declare the file it needs")
	}

	windows := Windows().Follower()
	if _, ok := windows.(PollingFollower); !ok {
		t.Errorf("windows follower = %T, want PollingFollower", windows)
	}
}

// TestQuoteArg pins the Windows command-line escaping. Arguments were previously
// joined with plain spaces, so a profile name containing whitespace was split
// into two arguments and a crafted one could inject more.
func TestQuoteArg(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"simple", "simple"},
		{"", `""`},
		{"has space", `"has space"`},
		{`quote"inside`, `"quote\"inside"`},
		// A trailing backslash is only special next to a quote, so an argument
		// that needs no quoting keeps it verbatim.
		{`trailing\`, `trailing\`},
		{`a\\b`, `a\\b`},
		// Here quoting is required, so the trailing run must be doubled to stop
		// it escaping the closing quote.
		{`with space\`, `"with space\\"`},
		{`c:\path\to\file`, `c:\path\to\file`},
		{`c:\program files\x`, `"c:\program files\x"`},
		{`--flag=value`, `--flag=value`},
		{"tab\there", "\"tab\there\""},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			if got := QuoteArg(tc.in); got != tc.want {
				t.Fatalf("QuoteArg(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestQuoteArgsKeepsArgumentsSeparate(t *testing.T) {
	got := QuoteArgs([]string{"use", "my profile"})
	if got != `use "my profile"` {
		t.Fatalf("QuoteArgs() = %q", got)
	}
}

// TestSudoersMatchesIssuedCommands is the correspondence proof.
//
// The sudo rules are narrow, so if the argv the binary issues ever stops
// matching a rule, sudo refuses and every privileged operation fails for want of
// a password — a total breakage, and a worse outcome than the permissive grant
// the narrowing replaced. Both the rules and the runtime argv derive from
// SudoCommands, and this test confirms the derivation holds using the same
// matching semantics sudo applies.
func TestSudoersMatchesIssuedCommands(t *testing.T) {
	for _, layout := range []Layout{Darwin(), Linux()} {
		t.Run(layout.OS, func(t *testing.T) {
			commands := layout.SudoCommands()
			if len(commands) < 4 {
				t.Fatalf("expected the full privileged command set, got %d", len(commands))
			}

			// The activation command is the one with a varying argument.
			argv := layout.ActivateArgv(filepath.Join(layout.ProfilesDir, "work-vpn.json"))
			if !matchesAny(commands, argv) {
				t.Fatalf("the activate command %v is not authorised by any rule:\n%s", argv, renderRules(commands))
			}

			// Any name accepted by profile validation must also match.
			for _, name := range []string{"a", "work-vpn", "v2.profile", "under_score"} {
				argv := layout.ActivateArgv(filepath.Join(layout.ProfilesDir, name+".json"))
				if !matchesAny(commands, argv) {
					t.Errorf("profile %q produces an unauthorised command %v", name, argv)
				}
			}
		})
	}
}

// TestSudoersGlobBlocksTraversal confirms the property the narrow rule relies
// on: sudo matches globs with fnmatch's FNM_PATHNAME semantics, so a wildcard
// cannot span a path separator and a traversal sequence cannot satisfy the rule.
func TestSudoersGlobBlocksTraversal(t *testing.T) {
	layout := Darwin()
	commands := layout.SudoCommands()

	forbidden := [][]string{
		layout.ActivateArgv("/usr/local/etc/sing-box/profiles/../../../../etc/shadow.json"),
		{layout.LnBin, "-sfn", "/etc/shadow", "/usr/local/etc/sing-box/config.json"},
		{layout.LnBin, "-sfn", "/usr/local/etc/sing-box/profiles/x.json", "/etc/crontab"},
		{layout.CtlBin, "bootout", "system/com.apple.something"},
		{layout.CtlBin, "unload", layout.LaunchdLabel},
		{"/bin/sh", "-c", "id"},
	}
	for _, argv := range forbidden {
		if matchesAny(commands, argv) {
			t.Errorf("%v must not be authorised by:\n%s", argv, renderRules(commands))
		}
	}
}

func TestSudoersRendersValidFile(t *testing.T) {
	content, err := Darwin().Sudoers("alice")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "alice ALL=(root) NOPASSWD:") {
		t.Fatalf("sudoers content lacks a rule:\n%s", content)
	}
	// A blanket grant is exactly what the narrowing removed.
	for _, line := range strings.Split(content, "\n") {
		if strings.HasSuffix(strings.TrimSpace(line), "NOPASSWD: /bin/ln") {
			t.Fatalf("unrestricted ln grant reintroduced:\n%s", line)
		}
	}
	if _, err := Windows().Sudoers("alice"); err == nil {
		t.Error("Windows does not use sudoers and should say so")
	}
}

// TestSudoersRejectsUnsafeUser guards the rendered file, which is installed with
// root privileges, against injected directives.
func TestSudoersRejectsUnsafeUser(t *testing.T) {
	unsafe := []string{
		"",
		"alice ALL=(root) NOPASSWD: ALL",
		"alice\nbob ALL=(root) NOPASSWD: ALL",
		"alice #comment",
		strings.Repeat("a", 33),
	}
	for _, user := range unsafe {
		if _, err := Darwin().Sudoers(user); err == nil {
			t.Errorf("Sudoers(%q) succeeded, want rejection", user)
		}
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
	content, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	if got := string(content[offset:]); got != "three\nfour\n" {
		t.Fatalf("tail = %q", got)
	}
}

func matchesAny(commands []SudoCommand, argv []string) bool {
	for _, command := range commands {
		if command.Matches(argv) {
			return true
		}
	}
	return false
}

func renderRules(commands []SudoCommand) string {
	var b strings.Builder
	for _, command := range commands {
		b.WriteString("  " + command.String() + "\n")
	}
	return b.String()
}
