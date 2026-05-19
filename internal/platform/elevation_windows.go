package platform

import (
	"fmt"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// IsElevated checks if the current process is running with administrator privileges.
func IsElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// RunElevated re-launches the current executable with the given arguments
// using the "runas" verb to trigger UAC elevation. It waits for the elevated
// process to complete and returns its exit code.
func RunElevated(args []string) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 1, fmt.Errorf("cannot determine executable path: %w", err)
	}

	verb, _ := windows.UTF16PtrFromString("runas")
	exePtr, _ := windows.UTF16PtrFromString(exe)
	argStr, _ := windows.UTF16PtrFromString(strings.Join(args, " "))

	const (
		seeMaskNocloseprocess = 0x00000040
		swHide                = 0
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

	shell32 := windows.NewLazySystemDLL("shell32.dll")
	shellExecuteEx := shell32.NewProc("ShellExecuteExW")

	sei := shellExecuteInfo{
		fMask:        seeMaskNocloseprocess,
		lpVerb:       verb,
		lpFile:       exePtr,
		lpParameters: argStr,
		nShow:        swHide,
	}
	sei.cbSize = uint32(unsafe.Sizeof(sei))

	ret, _, callErr := shellExecuteEx.Call(uintptr(unsafe.Pointer(&sei)))
	if ret == 0 {
		// Error code 1223 means the user cancelled the UAC prompt
		if callErr != nil && strings.Contains(callErr.Error(), "1223") {
			return 1, fmt.Errorf("elevation cancelled by user")
		}
		return 1, fmt.Errorf("ShellExecuteEx failed: %v", callErr)
	}

	// Wait for the elevated process to finish and propagate its exit code.
	handle := windows.Handle(sei.hProcess)
	if handle != 0 {
		windows.WaitForSingleObject(handle, windows.INFINITE)
		var exitCode uint32
		windows.GetExitCodeProcess(handle, &exitCode)
		windows.CloseHandle(handle)
		return int(exitCode), nil
	}

	// No process handle returned — cannot verify outcome.
	return 1, fmt.Errorf("elevated process started but no handle returned; cannot verify result")
}
