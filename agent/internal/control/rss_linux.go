//go:build linux && !android

package control

import (
	"os"
	"strconv"
	"strings"
)

// processRSSBytes reads the process resident set size from /proc/self/statm
// (second field, in pages) multiplied by the page size.
func processRSSBytes() int64 {
	b, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(b))
	if len(fields) < 2 {
		return 0
	}
	pages, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil || pages < 0 {
		return 0
	}
	return pages * int64(os.Getpagesize())
}
