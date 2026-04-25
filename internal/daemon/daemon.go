package daemon

type RunState string

const (
	StateRunning RunState = "running"
	StateStopped RunState = "stopped"
	StateError   RunState = "error"
)

type Manager interface {
	Restart() error
	Stop() error
	Status() (RunState, error)
}
