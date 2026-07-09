package control

import (
	"os"
	"runtime"
	"sync"
	"time"
)

// Coarse process CPU% based on process times / wall clock between samples.
// First call returns 0 (no baseline yet).

var (
	cpuMu       sync.Mutex
	cpuLastWall time.Time
	cpuLastProc time.Duration
)

func sampleCPUPercent() float64 {
	proc, err := processCPUTime()
	if err != nil {
		return 0
	}
	now := time.Now()

	cpuMu.Lock()
	defer cpuMu.Unlock()

	if cpuLastWall.IsZero() {
		cpuLastWall = now
		cpuLastProc = proc
		return 0
	}
	wallDelta := now.Sub(cpuLastWall).Seconds()
	procDelta := (proc - cpuLastProc).Seconds()
	cpuLastWall = now
	cpuLastProc = proc
	if wallDelta <= 0 {
		return 0
	}
	// Normalize by GOMAXPROCS so 100% ≈ one full core busy on all procs.
	pct := (procDelta / wallDelta) * 100 / float64(runtime.GOMAXPROCS(0))
	if pct < 0 {
		return 0
	}
	if pct > 100 {
		return 100
	}
	return pct
}

func processCPUTime() (time.Duration, error) {
	// Portable fallback: use total GC CPU fraction is not process time.
	// On Linux read /proc/self/stat; elsewhere return error → 0%.
	if runtime.GOOS != "linux" {
		return 0, os.ErrInvalid
	}
	return processCPUTimeLinux()
}
