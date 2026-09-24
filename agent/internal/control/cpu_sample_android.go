//go:build android

package control

import (
	"os"
	"time"
)

// AndroidCPUProvider lets the Android host supply process CPU time.
var AndroidCPUProvider func() (time.Duration, error)

func processCPUTimeLinux() (time.Duration, error) {
	if AndroidCPUProvider != nil {
		return AndroidCPUProvider()
	}
	return 0, os.ErrInvalid
}
