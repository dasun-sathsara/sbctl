package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"sbctl/internal/daemon"
	"sbctl/internal/platform"
	"sbctl/internal/profile"
	"sbctl/internal/singbox"
	"sbctl/internal/ui"
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
	rt, err := platform.Detect()
	if err == nil {
		err = newRootCmd(rt).Execute()
	}
	if err != nil {
		var coded exitCoder
		if errors.As(err, &coded) {
			fmt.Fprintln(os.Stderr, coded.Error())
			os.Exit(coded.ExitCode())
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCmd(rt platform.Runtime) *cobra.Command {
	root := &cobra.Command{
		Use:           "sbctl",
		Short:         "Manage sing-box profiles",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInteractive(rt)
		},
	}

	root.AddCommand(
		newListCmd(rt),
		newUseCmd(rt),
		newOffCmd(rt),
		newStatusCmd(rt),
		newLogsCmd(rt),
		newEditCmd(rt),
		newAddCmd(rt),
		newRmCmd(rt),
		newCheckCmd(rt),
		newIPCmd(),
		newVersionCmd(),
	)

	return root
}

func newListCmd(rt platform.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List profiles",
		RunE: func(cmd *cobra.Command, args []string) error {
			profiles, active, err := profile.List(rt.ProfilesDir, rt.ActiveConfigPath)
			if rt.ActiveNamePath != "" {
				active, _ = rt.Activator.ActiveName()
			}
			if err != nil {
				return err
			}
			state, stateErr := rt.Manager.Status()
			if stateErr != nil || state != daemon.StateRunning {
				active = ""
			}
			fmt.Print(ui.RenderProfileList(profiles, active))
			return nil
		},
	}
}

func newUseCmd(rt platform.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "use <name>",
		Short: "Activate a profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return useProfile(rt, args[0])
		},
	}
}

func newOffCmd(rt platform.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "off",
		Short: "Stop sing-box",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !platform.IsElevated() {
				exitCode, err := platform.RunElevated([]string{"off"})
				if err != nil {
					return err
				}
				if exitCode != 0 {
					return cliError{code: exitCode, msg: "elevated process failed"}
				}
				fmt.Println("✓ sing-box stopped")
				rt.Notifier.Notify("sing-box stopped")
				return nil
			}
			if err := rt.Manager.Stop(); err != nil {
				return err
			}
			fmt.Println("✓ sing-box stopped")
			rt.Notifier.Notify("sing-box stopped")
			return nil
		},
	}
}

func newStatusCmd(rt platform.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show sing-box status",
		RunE: func(cmd *cobra.Command, args []string) error {
			return printStatus(rt)
		},
	}
}

func newLogsCmd(rt platform.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "logs",
		Short: "Tail sing-box error logs",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			return rt.LogFollower.Follow(ctx)
		},
	}
}

func newEditCmd(rt platform.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "edit <name>",
		Short: "Edit and validate a profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := profile.PathFor(rt.ProfilesDir, args[0])
			if _, err := os.Stat(path); err != nil {
				return wrapProfileStoreError(rt, err, "access")
			}

			before, err := os.ReadFile(path)
			if err != nil {
				return wrapProfileStoreError(rt, err, "read")
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
							return wrapProfileStoreError(rt, writeErr, "restore")
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

func newAddCmd(rt platform.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "add <name>",
		Short: "Create a new profile from the skeleton",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := os.MkdirAll(rt.ProfilesDir, 0o755); err != nil {
				return wrapProfileStoreError(rt, err, "prepare")
			}
			path := profile.PathFor(rt.ProfilesDir, args[0])
			file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
			if err != nil {
				if errors.Is(err, os.ErrExist) {
					return fmt.Errorf("profile %s already exists", args[0])
				}
				return wrapProfileStoreError(rt, err, "create")
			}
			if _, err := file.WriteString(skeleton); err != nil {
				_ = file.Close()
				return wrapProfileStoreError(rt, err, "write")
			}
			if err := file.Close(); err != nil {
				return wrapProfileStoreError(rt, err, "finalize")
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

func newRmCmd(rt platform.Runtime) *cobra.Command {
	allowForce := false
	cmd := &cobra.Command{
		Use:   "rm <name>",
		Short: "Delete a profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			active, err := rt.Activator.ActiveName()
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
			if err := os.Remove(profile.PathFor(rt.ProfilesDir, name)); err != nil {
				return wrapProfileStoreError(rt, err, "remove")
			}
			fmt.Printf("✓ removed %s\n", name)
			return nil
		},
	}
	cmd.Flags().BoolVar(&allowForce, "force", false, "allow deleting the active profile")
	return cmd
}

func newCheckCmd(rt platform.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "check [name]",
		Short: "Validate a profile with sing-box check",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var path string
			var label string
			if len(args) == 0 {
				active, err := rt.Activator.ActivePath()
				if err != nil {
					return err
				}
				if active == "" {
					return cliError{
						code: 1,
						msg:  fmt.Sprintf("no active profile is managed by sbctl; run `sbctl use <name>` to manage %s", rt.ActiveConfigPath),
					}
				}
				if _, err := os.Stat(active); err != nil {
					if errors.Is(err, os.ErrNotExist) {
						return cliError{
							code: 1,
							msg:  fmt.Sprintf("✗ active config is broken: %s", rt.ActiveConfigPath),
						}
					}
					return err
				}
				path = active
				label = strings.TrimSuffix(filepath.Base(active), filepath.Ext(active))
				if rt.ActiveNamePath != "" {
					if activeName, err := rt.Activator.ActiveName(); err == nil && activeName != "" {
						label = activeName
					}
				}
			} else {
				label = args[0]
				path = profile.PathFor(rt.ProfilesDir, label)
			}
			if hasPlaceholders, markers, err := profile.HasPlaceholders(path); err != nil {
				return err
			} else if hasPlaceholders {
				return placeholderError(label, markers)
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

func newIPCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ip",
		Short: "Show current public IP and network location",
		RunE: func(cmd *cobra.Command, args []string) error {
			info, err := fetchIPInfo(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Print(renderIPInfo(info))
			return nil
		},
	}
}

type ipInfo struct {
	IP       string `json:"ip"`
	City     string `json:"city"`
	Region   string `json:"region"`
	Country  string `json:"country"`
	Location string `json:"loc"`
	Org      string `json:"org"`
	Postal   string `json:"postal"`
	Timezone string `json:"timezone"`
}

func fetchIPInfo(ctx context.Context) (ipInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://ipinfo.io/json", nil)
	if err != nil {
		return ipInfo{}, err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return ipInfo{}, fmt.Errorf("failed to fetch ipinfo.io: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return ipInfo{}, fmt.Errorf("ipinfo.io returned %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return ipInfo{}, err
	}
	var info ipInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return ipInfo{}, fmt.Errorf("ipinfo.io returned invalid JSON: %w", err)
	}
	return info, nil
}

func renderIPInfo(info ipInfo) string {
	var b strings.Builder
	b.WriteString("🌐 Public IP\n")
	writeInfoLine(&b, "📍", "IP", info.IP)
	writeInfoLine(&b, "🏙️", "City", info.City)
	writeInfoLine(&b, "🗺️", "Region", info.Region)
	writeInfoLine(&b, "🇺🇳", "Country", info.Country)
	writeInfoLine(&b, "🧭", "Location", info.Location)
	writeInfoLine(&b, "🏢", "Network", info.Org)
	writeInfoLine(&b, "✉️", "Postal", info.Postal)
	writeInfoLine(&b, "⏱️", "Timezone", info.Timezone)
	return b.String()
}

func writeInfoLine(b *strings.Builder, icon, label, value string) {
	if strings.TrimSpace(value) == "" {
		value = "-"
	}
	fmt.Fprintf(b, "%s %-9s %s\n", icon, label+":", value)
}

func runInteractive(rt platform.Runtime) error {
	state, err := rt.Manager.Status()
	if err != nil {
		fmt.Println(ui.RenderStatus(daemon.StateError, "", ""))
		return err
	}
	activeName, _ := rt.Activator.ActiveName()
	if state != daemon.StateRunning {
		activeName = ""
	}
	tunName := ""
	if activeName != "" {
		tunName, _ = profile.InterfaceName(profile.PathFor(rt.ProfilesDir, activeName))
	}

	profiles, _, err := profile.List(rt.ProfilesDir, rt.ActiveConfigPath)
	if err != nil {
		return err
	}

	if len(profiles) == 0 {
		fmt.Println(ui.RenderStatus(state, activeName, tunName))
		fmt.Printf("\nNo profiles found in %s.\nCreate one with:  sbctl add <name>\n", rt.ProfilesDir)
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
		return newOffCmd(rt).RunE(&cobra.Command{}, nil)
	}
	if choice == activeName && state == daemon.StateRunning {
		fmt.Printf("✓ %s already active\n", choice)
		return nil
	}
	return useProfile(rt, choice)
}

func useProfile(rt platform.Runtime, name string) error {
	if !platform.IsElevated() {
		exitCode, err := platform.RunElevated([]string{"use", name})
		if err != nil {
			return err
		}
		if exitCode != 0 {
			return cliError{code: exitCode, msg: "elevated process failed"}
		}
		fmt.Printf("✓ switched to %s\n", name)
		rt.Notifier.Notify(fmt.Sprintf("switched to %s", name))
		return nil
	}
	path := profile.PathFor(rt.ProfilesDir, name)
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("profile %s not found", name)
		}
		return err
	}
	if hasPlaceholders, markers, err := profile.HasPlaceholders(path); err != nil {
		return err
	} else if hasPlaceholders {
		return placeholderError(name, markers)
	}
	if err := singbox.Check(path); err != nil {
		return err
	}
	rollback, err := rt.Activator.Activate(path)
	if err != nil {
		return err
	}
	if err := rt.Manager.Restart(); err != nil {
		if rollback.Known() {
			if revertErr := rollback.Rollback(); revertErr != nil {
				fmt.Fprintf(os.Stderr, "✗ failed to restart sing-box: %v\n✗ failed to restore previous active profile %s: %v\n", err, rollback.Description(), revertErr)
				return err
			}
			fmt.Fprintf(os.Stderr, "✗ failed to restart sing-box; reverted active profile to %s\n", rollback.Description())
			return err
		}
		fmt.Fprintf(os.Stderr, "✗ failed to restart sing-box: %v\n✗ failed to restore previous active profile: no prior active profile was set\n", err)
		return err
	}
	fmt.Printf("✓ switched to %s\n", name)
	rt.Notifier.Notify(fmt.Sprintf("switched to %s", name))
	return nil
}

func printStatus(rt platform.Runtime) error {
	status, err := rt.Manager.Status()
	if err != nil {
		return err
	}
	activeName, _ := rt.Activator.ActiveName()
	if status != daemon.StateRunning {
		activeName = ""
	}
	tunName := ""
	if activeName != "" {
		activePath := profile.PathFor(rt.ProfilesDir, activeName)
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

func appleScriptSafe(message string) string {
	return platform.AppleScriptSafe(message)
}

func wrapProfileStoreError(rt platform.Runtime, err error, action string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, os.ErrPermission) {
		return cliError{
			code: 1,
			msg:  fmt.Sprintf("failed to %s profile data: %v\nRun `make install` to repair ownership under %s.", action, err, rt.ProfilesDir),
		}
	}
	return err
}

func placeholderError(name string, markers []string) error {
	return cliError{
		code: 1,
		msg:  fmt.Sprintf("profile %s still contains placeholder values: %s\nEdit it before activating or starting sing-box.", name, strings.Join(markers, ", ")),
	}
}
