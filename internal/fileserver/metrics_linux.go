//go:build linux

package fileserver

import "syscall"

// readDiskStats uses syscall.Statfs to get disk usage for the given path on Linux.
func readDiskStats(path string) (total, used, free uint64, pct float64) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, 0, 0.0
	}
	total = stat.Blocks * uint64(stat.Bsize)
	free = stat.Bfree * uint64(stat.Bsize)
	if total >= free {
		used = total - free
	}
	if total > 0 {
		pct = float64(used) / float64(total) * 100.0
	}
	return total, used, free, pct
}
