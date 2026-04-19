package main

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"sbctl/internal/daemon"
	"sbctl/internal/profile"
	"sbctl/internal/singbox"
	"sbctl/internal/ui"
)

const (
	profilesDir = "/usr/local/etc/sing-box/profiles"
	activeLink  = "/usr/local/etc/sing-box/config.json"
	daemonLabel = "system/app.lexiflix.singbox"
	plistPath   = "/Library/LaunchDaemons/app.lexiflix.singbox.plist"
	errorLog    = "/var/log/sing-box/error.log"
	accessLog   = "/var/log/sing-box/access.log"
)

var version = "dev"

//go:embed assets/skeleton.json
var skeleton string

type exitCoder interface {
	error
	ExitCode() int
}

type cliError struct {
	code int
	msg  string
}

func (e cliError) Error() string { return e.msg }
func (e cliError) ExitCode() int { return e.code }

func main() {
	if err := newRootCmd().Execute(); err != nil {
		var coded exitCoder
		if errors.As(err, &coded) {
			fmt.Fprintln(os.Stderr, coded.Error())
			os.Exit(coded.ExitCode())
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "sbctl",
		Short:         "Manage sing-box profiles on macOS",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInteractive()
		},
	}

	root.AddCommand(
		newListCmd(),
		newUseCmd(),
		newOffCmd(),
		newStatusCmd(),
		newLogsCmd(),
		newEditCmd(),
		newAddCmd(),
		newRmCmd(),
		newCheckCmd(),
		newVersionCmd(),
	)

	return root
}

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List profiles",
		RunE: func(cmd *cobra.Command, args []string) error {
			profiles, active, err := profile.List(profilesDir, activeLink)
			if err != nil {
				return err
			}
			fmt.Print(ui.RenderProfileList(profiles, active))
			return nil
		},
	}
}

func newUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use <name>",
		Short: "Activate a profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return useProfile(args[0])
		},
	}
}

func newOffCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "off",
		Short: "Stop sing-box",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := daemon.Stop(daemonLabel); err != nil {
				return err
			}
			fmt.Println("✓ sing-box stopped")
			notify("sing-box stopped")
			return nil
		},
	}
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show sing-box status",
		RunE: func(cmd *cobra.Command, args []string) error {
			return printStatus()
		},
	}
}

func newLogsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logs",
		Short: "Tail sing-box error logs",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			tail := exec.CommandContext(ctx, "tail", "-f", errorLog)
			tail.Stdout = os.Stdout
			tail.Stderr = os.Stderr
			if err := tail.Run(); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				var exitErr *exec.ExitError
				if errors.As(err, &exitErr) && exitErr.ExitCode() == -1 {
					return nil
				}
				return err
			}
			return nil
		},
	}
}

func newEditCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "edit <name>",
		Short: "Edit and validate a profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := profile.PathFor(profilesDir, args[0])
			if _, err := os.Stat(path); err != nil {
				return wrapProfileStoreError(err, "access")
			}

			before, err := os.ReadFile(path)
			if err != nil {
				return wrapProfileStoreError(err, "read")
			}

			for {
				if err := openEditor(path); err != nil {
					return err
				}

				if err := singbox.Check(path); err == nil {
					fmt.Printf("✓ %s is valid\n", args[0])
					return nil
				} else {
					fmt.Fprintf(os.Stderr, "validation failed:\n%s\n", err)
					choice, promptErr := ui.ResolveValidationFailure()
					if promptErr != nil {
						return promptErr
					}
					switch choice {
					case ui.EditChoiceReedit:
						continue
					case ui.EditChoiceRevert:
						if writeErr := os.WriteFile(path, before, 0o644); writeErr != nil {
							return wrapProfileStoreError(writeErr, "restore")
						}
						fmt.Println("reverted changes")
					case ui.EditChoiceKeepBroken:
					default:
						return fmt.Errorf("unknown validation resolution %q", choice)
					}
					return cliError{code: 1, msg: fmt.Sprintf("profile %s failed validation", args[0])}
				}
			}
		},
	}
}

func newAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add <name>",
		Short: "Create a new profile from the skeleton",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := os.MkdirAll(profilesDir, 0o755); err != nil {
				return wrapProfileStoreError(err, "prepare")
			}
			path := profile.PathFor(profilesDir, args[0])
			file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
			if err != nil {
				if errors.Is(err, os.ErrExist) {
					return fmt.Errorf("profile %s already exists", args[0])
				}
				return wrapProfileStoreError(err, "create")
			}
			if _, err := file.WriteString(skeleton); err != nil {
				_ = file.Close()
				return wrapProfileStoreError(err, "write")
			}
			if err := file.Close(); err != nil {
				return wrapProfileStoreError(err, "finalize")
			}
			if err := openEditor(path); err != nil {
				return err
			}
			if err := singbox.Check(path); err != nil {
				return err
			}
			fmt.Printf("✓ created %s\n", args[0])
			return nil
		},
	}
}

func newRmCmd() *cobra.Command {
	allowForce := false
	cmd := &cobra.Command{
		Use:   "rm <name>",
		Short: "Delete a profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			active, err := profile.ActiveName(activeLink)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if name == active && !allowForce {
				return fmt.Errorf("refusing to delete active profile %s without --force", name)
			}
			ok, err := ui.ConfirmDelete(name)
			if err != nil {
				return err
			}
			if !ok {
				fmt.Println("cancelled")
				return nil
			}
			if err := os.Remove(profile.PathFor(profilesDir, name)); err != nil {
				return wrapProfileStoreError(err, "remove")
			}
			fmt.Printf("✓ removed %s\n", name)
			return nil
		},
	}
	cmd.Flags().BoolVar(&allowForce, "force", false, "allow deleting the active profile")
	return cmd
}

func newCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "check [name]",
		Short: "Validate a profile with sing-box check",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var path string
			var label string
			if len(args) == 0 {
				target, err := os.Readlink(activeLink)
				if err != nil {
					return err
				}
				active, err := profile.ActivePath(activeLink)
				if err != nil {
					return err
				}
				if _, err := os.Stat(active); err != nil {
					if errors.Is(err, os.ErrNotExist) {
						return cliError{
							code: 1,
							msg:  fmt.Sprintf("✗ active config symlink is broken: %s → %s", activeLink, target),
						}
					}
					return err
				}
				path = active
				label = strings.TrimSuffix(filepath.Base(active), filepath.Ext(active))
			} else {
				label = args[0]
				path = profile.PathFor(profilesDir, label)
			}
			if err := singbox.Check(path); err != nil {
				return err
			}
			fmt.Printf("✓ %s passed sing-box check\n", label)
			return nil
		},
	}
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(version)
		},
	}
}

func runInteractive() error {
	state, err := daemon.Status(daemonLabel)
	if err != nil {
		fmt.Println(ui.RenderStatus(daemon.StateError, "", ""))
		return err
	}
	activeName, _ := profile.ActiveName(activeLink)
	tunName := ""
	if activeName != "" {
		tunName, _ = profile.InterfaceName(profile.PathFor(profilesDir, activeName))
	}

	profiles, _, err := profile.List(profilesDir, activeLink)
	if err != nil {
		return err
	}

	if len(profiles) == 0 {
		fmt.Println(ui.RenderStatus(state, activeName, tunName))
		fmt.Printf("\nNo profiles found in %s.\nCreate one with:  sbctl add <name>\n", profilesDir)
		return nil
	}

	choice, err := ui.PickProfile(profiles, activeName, state, tunName)
	if err != nil {
		if errors.Is(err, ui.ErrCancelled) {
			return nil
		}
		return err
	}
	if choice == "" {
		return nil
	}
	if choice == ui.TurnOffChoice {
		return newOffCmd().RunE(&cobra.Command{}, nil)
	}
	if choice == activeName && state == daemon.StateRunning {
		fmt.Printf("✓ %s already active\n", choice)
		return nil
	}
	return useProfile(choice)
}

func useProfile(name string) error {
	path := profile.PathFor(profilesDir, name)
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("profile %s not found", name)
		}
		return err
	}
	if err := singbox.Check(path); err != nil {
		return err
	}
	oldTarget, oldTargetErr := os.Readlink(activeLink)
	oldTargetKnown := oldTargetErr == nil
	if oldTargetErr != nil && !errors.Is(oldTargetErr, os.ErrNotExist) {
		return oldTargetErr
	}
	if err := sudo("ln", "-sfn", path, activeLink); err != nil {
		return err
	}
	if err := daemon.Restart(daemonLabel, plistPath); err != nil {
		if oldTargetKnown {
			if revertErr := sudo("ln", "-sfn", oldTarget, activeLink); revertErr != nil {
				fmt.Fprintf(os.Stderr, "✗ failed to restart sing-box: %v\n✗ failed to restore previous active profile %s: %v\n", err, oldTarget, revertErr)
				return err
			}
			fmt.Fprintf(os.Stderr, "✗ failed to restart sing-box; reverted active profile to %s\n", oldTarget)
			return err
		}
		fmt.Fprintf(os.Stderr, "✗ failed to restart sing-box: %v\n✗ failed to restore previous active profile: no prior symlink target was set\n", err)
		return err
	}
	fmt.Printf("✓ switched to %s\n", name)
	notify(fmt.Sprintf("switched to %s", name))
	return nil
}

func printStatus() error {
	status, err := daemon.Status(daemonLabel)
	if err != nil {
		return err
	}
	activeName, _ := profile.ActiveName(activeLink)
	tunName := ""
	if activeName != "" {
		activePath := profile.PathFor(profilesDir, activeName)
		tunName, _ = profile.InterfaceName(activePath)
	}
	fmt.Println(ui.RenderStatus(status, activeName, tunName))
	return nil
}

func openEditor(path string) error {
	editor := os.Getenv("EDITOR")
	if strings.TrimSpace(editor) == "" {
		editor = "nvim"
	}
	parts := strings.Fields(editor)
	cmd := exec.Command(parts[0], append(parts[1:], path)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func notify(message string) {
	cmd := exec.Command("osascript", "-e", fmt.Sprintf(`display notification "%s" with title "sing-box"`, appleScriptSafe(message)))
	_ = cmd.Run()
}

var asciiNotificationFilter = regexp.MustCompile(`[^\x20-\x7E]+`)

func appleScriptSafe(message string) string {
	safe := asciiNotificationFilter.ReplaceAllString(message, " ")
	safe = strings.ReplaceAll(safe, `"`, `'`)
	safe = strings.Join(strings.Fields(safe), " ")
	return safe
}

func sudo(args ...string) error {
	cmd := exec.Command("sudo", append([]string{"-n"}, args...)...)
	var stderr bytes.Buffer
	cmd.Stdout = os.Stdout
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderr)
	if err := cmd.Run(); err != nil {
		if strings.Contains(stderr.String(), "a password is required") {
			return cliError{code: 2, msg: "sudo access is not configured; finish /etc/sudoers.d/sbctl setup and retry"}
		}
		return err
	}
	return nil
}

func wrapProfileStoreError(err error, action string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, os.ErrPermission) {
		return cliError{
			code: 1,
			msg:  fmt.Sprintf("failed to %s profile data: %v\nRun `make install` to repair ownership under %s.", action, err, profilesDir),
		}
	}
	return err
}
