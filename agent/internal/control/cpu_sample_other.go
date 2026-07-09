//go:build !linux

package control

import (
	"os"
	"time"
)

func processCPUTimeLinux() (time.Duration, error) {
	return 0, os.ErrInvalid
}
