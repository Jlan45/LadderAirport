package control

import "context"

type State string

const (
	StateStopped State = "stopped"
	StateRunning State = "running"
	StateError   State = "error"
)

type Status struct {
	State         State
	ConfigHash    string
	StartedAtUnix int64
	LastError     string
}

type Metrics struct {
	Connections    int64
	UplinkBytes    int64
	DownlinkBytes  int64
	CPUPercent     float64
	MemoryRSSBytes int64
}

type Runtime interface {
	Apply(ctx context.Context, configJSON string, hash string) error
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Status(ctx context.Context) Status
	Metrics(ctx context.Context) Metrics
}
