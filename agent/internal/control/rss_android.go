//go:build android

package control

import "runtime"

// AndroidRSSProvider lets the Android host supply the process RSS.
var AndroidRSSProvider func() int64

func processRSSBytes() int64 {
	if AndroidRSSProvider != nil {
		if rss := AndroidRSSProvider(); rss > 0 {
			return rss
		}
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return int64(ms.Sys)
}
