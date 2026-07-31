package ui

import (
	"errors"
	"fmt"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// EditFailureAction is what the user chose after a profile failed validation.
type EditFailureAction string

const (
	// EditRetry reopens the editor.
	EditRetry EditFailureAction = "retry"
	// EditRevert restores the previous contents.
	EditRevert EditFailureAction = "revert"
	// EditKeep leaves the invalid file in place.
	EditKeep EditFailureAction = "keep"
)

// HuhTheme derives a huh theme from the shared tokens.
//
// The prompts previously used an unrelated preset palette, so a confirm dialog
// looked like it came from a different program than the picker two lines above
// it. Deriving from the same tokens is what makes the whole tool feel like one
// surface.
func (t Theme) HuhTheme() *huh.Theme {
	theme := huh.ThemeBase()

	theme.Focused.Base = theme.Focused.Base.BorderForeground(t.Accent)
	theme.Focused.Title = lipgloss.NewStyle().Foreground(t.Accent).Bold(true)
	theme.Focused.Description = lipgloss.NewStyle().Foreground(t.Muted)
	theme.Focused.SelectedOption = lipgloss.NewStyle().Foreground(t.Success).Bold(true)
	theme.Focused.UnselectedOption = lipgloss.NewStyle().Foreground(t.Fg)
	theme.Focused.SelectSelector = lipgloss.NewStyle().Foreground(t.Accent).SetString(t.Symbols.Cursor + " ")
	theme.Focused.FocusedButton = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(t.Accent).Padding(0, 2)
	theme.Focused.BlurredButton = lipgloss.NewStyle().Foreground(t.Muted).Padding(0, 2)
	theme.Focused.ErrorMessage = lipgloss.NewStyle().Foreground(t.Danger)

	theme.Blurred.Title = lipgloss.NewStyle().Foreground(t.Muted)
	theme.Blurred.Description = lipgloss.NewStyle().Foreground(t.Muted)

	return theme
}

// ConfirmDelete asks for confirmation before removing a profile.
//
// warning carries any additional consequence, such as the active config being
// left without a target, so the user sees the full cost before agreeing.
func (t Theme) ConfirmDelete(name, warning string) (bool, error) {
	description := "This cannot be undone."
	if warning != "" {
		description = warning + "\nThis cannot be undone."
	}

	var confirmed bool
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(fmt.Sprintf("Delete profile %q?", name)).
				Description(description).
				Affirmative("Delete").
				Negative("Keep").
				Value(&confirmed),
		),
	).WithTheme(t.HuhTheme())

	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return false, ErrCancelled
		}
		return false, err
	}
	return confirmed, nil
}

// ResolveEditFailure asks what to do about a profile that failed validation.
func (t Theme) ResolveEditFailure(detail string) (EditFailureAction, error) {
	var choice EditFailureAction
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[EditFailureAction]().
				Title("That configuration is not valid").
				Description(detail).
				Options(
					huh.NewOption("Edit it again", EditRetry),
					huh.NewOption("Discard my changes", EditRevert),
					huh.NewOption("Keep it anyway", EditKeep),
				).
				Value(&choice),
		),
	).WithTheme(t.HuhTheme())

	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return "", ErrCancelled
		}
		return "", err
	}
	return choice, nil
}
