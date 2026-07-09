//go:build linux

package control

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

func processCPUTimeLinux() (time.Duration, error) {
	b, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return 0, err
	}
	// Format: pid (comm) state ppid ... utime stime (fields 14,15 after comm)
	s := string(b)
	// comm may contain spaces/parentheses — find last ')'
	i := strings.LastIndex(s, ")")
	if i < 0 || i+2 >= len(s) {
		return 0, fmt.Errorf("parse /proc/self/stat")
	}
	fields := strings.Fields(s[i+2:])
	// fields[0] is state; utime=fields[11], stime=fields[12] (0-based after comm)
	if len(fields) < 13 {
		return 0, fmt.Errorf("short /proc/self/stat")
	}
	utime, err1 := strconv.ParseUint(fields[11], 10, 64)
	stime, err2 := strconv.ParseUint(fields[12], 10, 64)
	if err1 != nil || err2 != nil {
		return 0, fmt.Errorf("parse cpu ticks")
	}
	// USER_HZ is typically 100 on Linux
	const userHz = 100
	ticks := utime + stime
	return time.Duration(ticks) * time.Second / userHz, nil
}
