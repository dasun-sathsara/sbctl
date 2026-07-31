package platform

import (
	"fmt"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

// lookup resolves a helper binary on PATH, falling back to a known absolute
// path when PATH lookup fails (for example when sbctl runs from a service
// context with a minimal environment).
func lookup(name, fallback string) string {
	if resolved, err := exec.LookPath(name); err == nil {
		return resolved
	}
	return fallback
}

// SudoCommand is one privileged operation sbctl is permitted to perform.
//
// Argv is expressed as a sudoers pattern: every element is literal except
// where a glob is required to cover the varying profile filename. Because the
// installed sudoers file and the runtime argv are both derived from this single
// list, the two cannot drift apart — a mismatch would otherwise leave every
// operation failing with "a password is required".
type SudoCommand struct {
	// Name is a stable identifier used by `sbctl doctor` when reporting which
	// permission is missing.
	Name string

	// Argv is the full command, including the binary path at index 0.
	Argv []string
}

// String renders the command as it appears in a sudoers file.
func (c SudoCommand) String() string { return strings.Join(c.Argv, " ") }

// Matches reports whether a concrete argv would be authorised by this rule.
//
// It uses path.Match, whose "* does not cross /" semantics are exactly those
// of fnmatch(3) with FNM_PATHNAME, which is how sudo evaluates sudoers globs.
// That is what stops profiles/../../../etc/shadow.json from matching
// profiles/*.json.
func (c SudoCommand) Matches(argv []string) bool {
	if len(argv) != len(c.Argv) {
		return false
	}
	for i, pattern := range c.Argv {
		ok, err := path.Match(pattern, argv[i])
		if err != nil || !ok {
			return false
		}
	}
	return true
}

// SudoCommands returns every privileged operation for this layout, in the
// order they appear in the generated sudoers file. It returns nil on platforms
// that do not use sudo.
func (l Layout) SudoCommands() []SudoCommand {
	if !l.UsesSudo() {
		return nil
	}
	cmds := []SudoCommand{{
		Name: "activate",
		// The glob covers the profile name only. ValidateName already rejects
		// separators, and FNM_PATHNAME prevents traversal regardless.
		Argv: []string{l.LnBin, "-sfn", filepath.ToSlash(filepath.Join(l.ProfilesDir, "*.json")), l.ActiveConfigPath},
	}}
	switch l.OS {
	case "darwin":
		cmds = append(cmds,
			SudoCommand{Name: "probe", Argv: []string{l.CtlBin, "print", l.LaunchdLabel}},
			SudoCommand{Name: "restart", Argv: []string{l.CtlBin, "kickstart", "-k", l.LaunchdLabel}},
			SudoCommand{Name: "stop", Argv: []string{l.CtlBin, "bootout", l.LaunchdLabel}},
			SudoCommand{Name: "bootstrap", Argv: []string{l.CtlBin, "bootstrap", "system", l.PlistPath}},
		)
	case "linux":
		cmds = append(cmds,
			// Only `show` is granted: it supersedes `is-active` by reporting the
			// same state plus the restart counter, in locale-independent
			// key=value form. Granting a command sbctl never issues would widen
			// the privilege for nothing.
			SudoCommand{Name: "probe", Argv: []string{l.CtlBin, "show", "-p", SystemdShowProperties, l.ServiceName}},
			SudoCommand{Name: "restart", Argv: []string{l.CtlBin, "restart", l.ServiceName}},
			SudoCommand{Name: "stop", Argv: []string{l.CtlBin, "stop", l.ServiceName}},
		)
	}
	return cmds
}

// SystemdShowProperties is the property list sbctl requests from systemctl.
// These are key=value and locale-independent, unlike parsing `is-active`
// prose, and they carry the restart counter needed for crash-loop detection.
const SystemdShowProperties = "ActiveState,SubState,NRestarts,ExecMainPID,ExecMainStatus"

// ActivateArgv is the concrete privileged command that points the active
// config at a profile. Callers pass it to a Runner prefixed with sudo.
func (l Layout) ActivateArgv(profilePath string) []string {
	return []string{l.LnBin, "-sfn", profilePath, l.ActiveConfigPath}
}

// Sudoers renders the complete /etc/sudoers.d/sbctl content granting user the
// commands from SudoCommands and nothing more.
//
// This is the single source of truth: the install scripts obtain the file by
// running `sbctl print-sudoers`, so a Go-side change cannot silently diverge
// from what is installed on disk.
func (l Layout) Sudoers(user string) (string, error) {
	if !l.UsesSudo() {
		return "", fmt.Errorf("sudoers is not used on %s", l.OS)
	}
	if err := validateSudoersUser(user); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("# Managed by sbctl. Do not edit by hand.\n")
	b.WriteString("# Regenerate with: sbctl print-sudoers --user " + user + "\n")
	b.WriteString("#\n")
	b.WriteString("# Each rule is scoped to one exact command so that a compromised\n")
	b.WriteString("# user account cannot use these grants to run arbitrary code as root.\n")
	for _, cmd := range l.SudoCommands() {
		fmt.Fprintf(&b, "%s ALL=(root) NOPASSWD: %s\n", user, cmd)
	}
	return b.String(), nil
}

// validateSudoersUser rejects names that could inject additional sudoers
// directives. The rendered file is installed with root privileges, so this
// input must never be trusted.
func validateSudoersUser(user string) error {
	if user == "" {
		return fmt.Errorf("sudoers user must not be empty")
	}
	if len(user) > 32 {
		return fmt.Errorf("sudoers user %q is too long", user)
	}
	for _, r := range user {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_', r == '-', r == '.':
		default:
			return fmt.Errorf("sudoers user %q contains an unsupported character %q", user, r)
		}
	}
	return nil
}
