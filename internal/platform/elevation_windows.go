package platform

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// WindowsElevator re-launches sbctl through ShellExecuteExW with the "runas"
// verb, which raises a UAC consent prompt.
type WindowsElevator struct{}

// NewElevator returns the elevation strategy for this host.
func NewElevator() Elevator { return WindowsElevator{} }

// IsElevated reports whether the process token is elevated.
func (WindowsElevator) IsElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

const (
	seeMaskNoCloseProcess = 0x00000040
	seeMaskFlagNoUI       = 0x00000400
	swShowNormal          = 1
)

type shellExecuteInfo struct {
	cbSize       uint32
	fMask        uint32
	hwnd         uintptr
	lpVerb       *uint16
	lpFile       *uint16
	lpParameters *uint16
	lpDirectory  *uint16
	nShow        int32
	hInstApp     uintptr
	lpIDList     uintptr
	lpClass      *uint16
	hkeyClass    uintptr
	dwHotKey     uint32
	hIcon        uintptr
	hProcess     uintptr
}

// RunElevated launches an elevated copy of sbctl with args and waits for it.
//
// The child window is shown rather than hidden. Hiding it previously made every
// error the elevated process printed invisible, while the parent unconditionally
// reported success; a brief console window is a much smaller cost than silent
// failure. Because the child prints its own output, the caller must not emit a
// duplicate success message.
func (WindowsElevator) RunElevated(args []string) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 1, fmt.Errorf("cannot determine executable path: %w", err)
	}

	verb, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return 1, err
	}
	exePtr, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return 1, err
	}
	// Arguments must be quoted individually; joining with spaces corrupts any
	// value containing whitespace and allows argument injection.
	argPtr, err := windows.UTF16PtrFromString(QuoteArgs(args))
	if err != nil {
		return 1, err
	}

	shell32 := windows.NewLazySystemDLL("shell32.dll")
	shellExecuteEx := shell32.NewProc("ShellExecuteExW")

	sei := shellExecuteInfo{
		fMask:        seeMaskNoCloseProcess | seeMaskFlagNoUI,
		lpVerb:       verb,
		lpFile:       exePtr,
		lpParameters: argPtr,
		nShow:        swShowNormal,
	}
	sei.cbSize = uint32(unsafe.Sizeof(sei))

	ret, _, callErr := shellExecuteEx.Call(uintptr(unsafe.Pointer(&sei)))
	if ret == 0 {
		// Compare the numeric error rather than matching on message text,
		// which is locale-dependent.
		if errno, ok := callErr.(windows.Errno); ok {
			switch errno {
			case windows.ERROR_CANCELLED:
				return 1, fmt.Errorf("elevation was declined")
			case windows.ERROR_ACCESS_DENIED:
				return 1, fmt.Errorf("elevation was denied by policy")
			}
		}
		return 1, fmt.Errorf("could not request elevation: %w", callErr)
	}

	handle := windows.Handle(sei.hProcess)
	if handle == 0 {
		return 1, fmt.Errorf("elevated process started but returned no handle, so its result cannot be confirmed")
	}
	defer windows.CloseHandle(handle)

	if _, err := windows.WaitForSingleObject(handle, windows.INFINITE); err != nil {
		return 1, fmt.Errorf("waiting for the elevated process failed: %w", err)
	}
	var exitCode uint32
	if err := windows.GetExitCodeProcess(handle, &exitCode); err != nil {
		return 1, fmt.Errorf("could not read the elevated process exit code: %w", err)
	}
	return int(exitCode), nil
}
