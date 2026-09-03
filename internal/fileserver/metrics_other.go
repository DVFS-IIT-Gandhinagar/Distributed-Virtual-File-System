//go:build !linux && !windows

package fileserver

// readDiskStats fallback for other operating systems.
func readDiskStats(path string) (total, used, free uint64, pct float64) {
	return 0, 0, 0, 0.0
}
