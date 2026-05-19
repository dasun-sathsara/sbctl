//go:build !windows

package platform

// IsElevated on non-Windows platforms always returns true since elevation
// is handled via sudo in the daemon/activator layer.
func IsElevated() bool {
	return true
}

// RunElevated is a no-op on non-Windows platforms.
func RunElevated(args []string) (int, error) {
	return 0, nil
}
