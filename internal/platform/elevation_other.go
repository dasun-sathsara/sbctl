//go:build !windows

package platform

import "fmt"

// UnixElevator represents the Unix privilege model, where escalation happens
// per-operation through sudo(8) inside the service and activator layers rather
// than by re-launching the whole process.
type UnixElevator struct{}

// NewElevator returns the elevation strategy for this host.
func NewElevator() Elevator { return UnixElevator{} }

// IsElevated always reports true: sbctl itself runs unprivileged and defers to
// sudo for the few operations that need root.
func (UnixElevator) IsElevated() bool { return true }

// RunElevated is never reached on Unix because IsElevated always reports true.
// It returns an explicit error rather than silently succeeding so that a future
// caller cannot mistake a no-op for a completed privileged operation.
func (UnixElevator) RunElevated([]string) (int, error) {
	return 1, fmt.Errorf("process-wide elevation is not used on this platform; privileged steps run through sudo")
}
