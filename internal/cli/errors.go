package cli

import (
	"errors"
	"fmt"

	"sbctl/internal/profile"
	"sbctl/internal/service"
)

// Exit codes. They are grouped by cause so that a wrapper script can react
// differently to "your config is wrong" than to "sbctl lacks permission",
// which a single catch-all code made impossible.
//
// Cancelling an interactive prompt is deliberately not a failure and exits 0:
// the user asked for nothing to happen, and nothing happened.
const (
	// ExitOK reports success, including user cancellation.
	ExitOK = 0
	// ExitError reports a generic failure: bad usage, missing profile, I/O or
	// network trouble.
	ExitError = 1
	// ExitValidation reports a configuration sbctl refuses to activate, either
	// because placeholders remain or because sing-box rejected it.
	ExitValidation = 2
	// ExitService reports that the service could not be controlled, or that it
	// failed to stay running after activation.
	ExitService = 3
	// ExitPermission reports missing privileges: sudo rules absent, or
	// elevation declined.
	ExitPermission = 4
)

// Error is a user-facing failure carrying its own exit code and, wherever
// possible, the next action to take.
type Error struct {
	// Code is the process exit code.
	Code int
	// Message states what went wrong, in plain language.
	Message string
	// Hints are concrete follow-up actions, rendered one per line.
	Hints []string
	// Err is the underlying cause, preserved for errors.Is and --verbose.
	Err error

	// Reported marks a failure whose details the command has already written,
	// which happens when a --json handler emits a structured result and then
	// signals the non-zero exit separately. Without it the failure would be
	// printed twice, producing two JSON objects on stdout and invalid output.
	Reported bool
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.Err }

// ExitCode reports the code this error should terminate with.
func (e *Error) ExitCode() int {
	if e.Code == 0 {
		return ExitError
	}
	return e.Code
}

// failf builds a generic failure.
func failf(format string, args ...any) *Error {
	return &Error{Code: ExitError, Message: fmt.Sprintf(format, args...)}
}

// withHints attaches follow-up actions.
func (e *Error) withHints(hints ...string) *Error {
	e.Hints = append(e.Hints, hints...)
	return e
}

// wrap attaches an underlying cause.
func (e *Error) wrap(err error) *Error {
	e.Err = err
	return e
}

// classify converts an arbitrary error into a user-facing Error, choosing the
// exit code and hint from what the error actually is.
//
// Centralising this is what keeps every command's failure output consistent;
// previously each call site invented its own wording, code and (usually
// missing) remedy.
func classify(err error) *Error {
	if err == nil {
		return nil
	}

	var already *Error
	if errors.As(err, &already) {
		return already
	}

	switch {
	case errors.Is(err, service.ErrSudoNotConfigured):
		return (&Error{
			Code:    ExitPermission,
			Message: "sbctl is not allowed to manage the sing-box service without a password",
			Err:     err,
		}).withHints(
			"repair the sudo rules with: sudo make install",
			"then confirm with: sbctl doctor",
		)

	case errors.Is(err, profile.ErrSingBoxMissing):
		return (&Error{
			Code:    ExitError,
			Message: "sing-box is not installed, or is not on PATH",
			Err:     err,
		}).withHints("install it with: sudo make install")

	case errors.Is(err, profile.ErrInvalidName):
		return (&Error{Code: ExitError, Message: err.Error(), Err: err}).
			withHints("use letters, digits, dot, dash or underscore")

	case isInvalidConfig(err):
		return (&Error{Code: ExitValidation, Message: err.Error(), Err: err}).
			withHints("fix the configuration, then run: sbctl check <name>")
	}

	return &Error{Code: ExitError, Message: err.Error(), Err: err}
}

func isInvalidConfig(err error) bool {
	var invalid *profile.InvalidConfigError
	return errors.As(err, &invalid)
}

// placeholderError reports a profile that still contains template markers.
func placeholderError(name string, markers []string) *Error {
	return (&Error{
		Code:    ExitValidation,
		Message: fmt.Sprintf("%s still contains placeholder values: %s", name, joinAnd(markers)),
	}).withHints(
		fmt.Sprintf("fill them in with: sbctl edit %s", name),
		"every TODO_ marker must be replaced before the profile can be used",
	)
}

// notFoundError reports an unknown profile and lists the real ones, so the user
// does not have to run a second command to find out what is available.
func notFoundError(name string, available []string) *Error {
	err := &Error{Code: ExitError, Message: fmt.Sprintf("there is no profile named %q", name)}
	if len(available) > 0 {
		err = err.withHints("available profiles: " + joinAnd(available))
	}
	return err.withHints(fmt.Sprintf("create it with: sbctl add %s", name))
}

func joinAnd(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	out := items[0]
	for _, item := range items[1 : len(items)-1] {
		out += ", " + item
	}
	return out + " and " + items[len(items)-1]
}
