package control

import (
	"context"
	"sync"
	"time"
)

// MockRuntime is a thread-safe in-memory Runtime used for lab and tests.
type MockRuntime struct {
	mu            sync.Mutex
	state         State
	configJSON    string
	configHash    string
	startedAtUnix int64
	lastError     string
	metrics       Metrics
}

func NewMockRuntime() *MockRuntime {
	return &MockRuntime{
		state: StateStopped,
	}
}

func (m *MockRuntime) Apply(_ context.Context, configJSON string, hash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.configJSON = configJSON
	m.configHash = hash
	m.state = StateRunning
	m.startedAtUnix = time.Now().Unix()
	m.lastError = ""
	return nil
}

func (m *MockRuntime) Start(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = StateRunning
	if m.startedAtUnix == 0 {
		m.startedAtUnix = time.Now().Unix()
	}
	m.lastError = ""
	return nil
}

func (m *MockRuntime) Stop(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = StateStopped
	return nil
}

func (m *MockRuntime) Status(_ context.Context) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return Status{
		State:         m.state,
		ConfigHash:    m.configHash,
		StartedAtUnix: m.startedAtUnix,
		LastError:     m.lastError,
	}
}

func (m *MockRuntime) Metrics(_ context.Context) Metrics {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.metrics
}

// SetMetrics allows tests/fixtures to inject metric values.
func (m *MockRuntime) SetMetrics(metrics Metrics) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.metrics = metrics
}

// ConfigJSON returns the last applied config (for tests).
func (m *MockRuntime) ConfigJSON() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.configJSON
}
