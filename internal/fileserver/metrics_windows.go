//go:build windows

package fileserver

import (
	"golang.org/x/sys/windows"
)

// readDiskStats uses GetDiskFreeSpaceEx to get disk usage for the given path on Windows.
func readDiskStats(path string) (total, used, free uint64, pct float64) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, 0, 0.0
	}
	var freeBytes, totalBytes, totalFreeBytes uint64
	if err := windows.GetDiskFreeSpaceEx(p, &freeBytes, &totalBytes, &totalFreeBytes); err != nil {
		return 0, 0, 0, 0.0
	}
	if totalBytes >= totalFreeBytes {
		used = totalBytes - totalFreeBytes
	}
	if totalBytes > 0 {
		pct = float64(used) / float64(totalBytes) * 100.0
	}
	return totalBytes, used, totalFreeBytes, pct
}
