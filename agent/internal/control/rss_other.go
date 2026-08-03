//go:build !linux

package control

import "runtime"

// processRSSBytes falls back to MemStats.Sys on platforms without /proc.
// It overcounts true RSS (it is total heap+system memory obtained from the OS),
// but keeps the metric non-zero off Linux.
func processRSSBytes() int64 {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return int64(ms.Sys)
}
