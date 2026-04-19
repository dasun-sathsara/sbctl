package ui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	"sbctl/internal/daemon"
	"sbctl/internal/profile"
)

var (
	ErrCancelled = errors.New("cancelled")

	runningStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	stoppedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	titleStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("13")).Bold(true)
	panelStyle   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
)

const TurnOffChoice = "__turn_off__"

const (
	EditChoiceReedit     = "reedit"
	EditChoiceRevert     = "revert"
	EditChoiceKeepBroken = "keep_broken"
)

func RenderProfileList(profiles []profile.Profile, active string) string {
	var b strings.Builder
	for _, p := range profiles {
		marker := "○"
		nameStyle := lipgloss.NewStyle()
		if p.Name == active {
			marker = runningStyle.Render("●")
			nameStyle = runningStyle
		}
		fmt.Fprintf(&b, "%s %s\n", marker, nameStyle.Render(p.Name))
	}
	return b.String()
}

func RenderStatus(state daemon.RunState, activeProfile, tunName string) string {
	stateText := string(state)
	stateStyle := stoppedStyle
	switch state {
	case daemon.StateRunning:
		stateStyle = runningStyle
	case daemon.StateError:
		stateStyle = errorStyle
	}

	lines := []string{
		titleStyle.Render("sing-box"),
		fmt.Sprintf("State: %s", stateStyle.Render(stateText)),
	}
	if activeProfile == "" {
		lines = append(lines, fmt.Sprintf("Profile: %s", dimStyle.Render("none")))
	} else {
		lines = append(lines, fmt.Sprintf("Profile: %s", activeProfile))
	}
	if state == daemon.StateRunning && tunName != "" {
		lines = append(lines, fmt.Sprintf("TUN: %s", tunName))
	}
	return panelStyle.Render(strings.Join(lines, "\n"))
}

func PickProfile(profiles []profile.Profile, active string) (string, error) {
	options := make([]huh.Option[string], 0, len(profiles)+2)
	for _, p := range profiles {
		label := p.Name
		if p.Name == active {
			label = "● " + p.Name + " (active)"
		}
		options = append(options, huh.NewOption(label, p.Name))
	}
	options = append(options, huh.NewOption("──────────", ""))
	options = append(options, huh.NewOption("Turn off", TurnOffChoice))

	var selected string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Choose sing-box profile").
				Options(options...).
				Value(&selected),
		),
	).WithTheme(huh.ThemeCatppuccin())

	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return "", ErrCancelled
		}
		return "", err
	}
	return selected, nil
}

func ConfirmDelete(name string) (bool, error) {
	var ok bool
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(fmt.Sprintf("Delete profile %s?", name)).
				Affirmative("Delete").
				Negative("Cancel").
				Value(&ok),
		),
	)
	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return false, ErrCancelled
		}
		return false, err
	}
	return ok, nil
}

func ResolveValidationFailure() (string, error) {
	var choice string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Validation failed. What should happen next?").
				Options(
					huh.NewOption("Re-edit", EditChoiceReedit),
					huh.NewOption("Revert", EditChoiceRevert),
					huh.NewOption("Keep broken file", EditChoiceKeepBroken),
				).
				Value(&choice),
		),
	)
	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return "", ErrCancelled
		}
		return "", err
	}
	return choice, nil
}
