package fileserver

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/domain"
)

// Metrics holds a point-in-time snapshot of fileserver health and resource usage.
type Metrics struct {
	DiskTotalBytes    uint64            `json:"disk_total_bytes"`
	DiskUsedBytes     uint64            `json:"disk_used_bytes"`
	DiskFreeBytes     uint64            `json:"disk_free_bytes"`
	DiskUsagePercent  float64           `json:"disk_usage_percent"`
	PerUserStorage    map[string]uint64 `json:"per_user_storage"`
	PerUserQuota      map[string]uint64 `json:"per_user_quota"`
	ChunkCount        int               `json:"chunk_count"`
	CPUTempCelsius    float64           `json:"cpu_temp_celsius"`
	CPUUsagePercent   float64           `json:"cpu_usage_percent"`
	MemUsedBytes      uint64            `json:"mem_used_bytes"`
	MemTotalBytes     uint64            `json:"mem_total_bytes"`
	MemUsagePercent   float64           `json:"mem_usage_percent"`
	LoadAvg1m         float64           `json:"load_avg_1m"`
	LoadAvg5m         float64           `json:"load_avg_5m"`
	UptimeSeconds     float64           `json:"uptime_seconds"`
	LastRestartUnix   int64             `json:"last_restart_unix"`
	ActiveConnections int               `json:"active_connections"`
	UsersAssigned     int               `json:"users_assigned_count"`
}

// CollectMetrics gathers a full metrics snapshot from the fileserver.
func (fs *FileServer) CollectMetrics() Metrics {
	fs.mu.RLock()

	// Per-user storage and quota
	perUserStorage := make(map[string]uint64, len(fs.users))
	perUserQuota := make(map[string]uint64, len(fs.users))
	for username, fid := range fs.users {
		var size uint64
		if inode, ok := fs.inodes[fid.String()]; ok {
			size = inode.Size
		}
		perUserStorage[username] = size
		perUserQuota[username] = storageQuota
	}

	// Count file inodes (chunks)
	chunkCount := 0
	for _, inode := range fs.inodes {
		if inode.Type == domain.InodeTypeFile {
			chunkCount++
		}
	}

	activeConns := len(fs.sessions)
	usersAssigned := len(fs.users)
	rootDir := fs.rootDir
	startTime := fs.startTime

	fs.mu.RUnlock()

	// Disk stats
	diskTotal, diskUsed, diskFree, diskPct := readDiskStats(rootDir)

	// CPU temperature
	cpuTemp := readCPUTemp()

	// Memory
	memTotal, memAvail := readMemInfo()
	var memUsed uint64
	var memPct float64
	if memTotal > 0 {
		memUsed = memTotal - memAvail
		memPct = float64(memUsed) / float64(memTotal) * 100.0
	}

	// Load average
	load1m, load5m := readLoadAvg()

	// CPU usage
	cpuUsage := readCPUUsage()

	return Metrics{
		DiskTotalBytes:    diskTotal,
		DiskUsedBytes:     diskUsed,
		DiskFreeBytes:     diskFree,
		DiskUsagePercent:  diskPct,
		PerUserStorage:    perUserStorage,
		PerUserQuota:      perUserQuota,
		ChunkCount:        chunkCount,
		CPUTempCelsius:    cpuTemp,
		CPUUsagePercent:   cpuUsage,
		MemUsedBytes:      memUsed,
		MemTotalBytes:     memTotal,
		MemUsagePercent:   memPct,
		LoadAvg1m:         load1m,
		LoadAvg5m:         load5m,
		UptimeSeconds:     time.Since(startTime).Seconds(),
		LastRestartUnix:   startTime.Unix(),
		ActiveConnections: activeConns,
		UsersAssigned:     usersAssigned,
	}
}


// readCPUTemp reads the CPU temperature from the Linux thermal sysfs interface.
func readCPUTemp() float64 {
	data, err := os.ReadFile("/sys/class/thermal/thermal_zone0/temp")
	if err != nil {
		return 0.0
	}
	raw := strings.TrimSpace(string(data))
	milliDeg, err := strconv.Atoi(raw)
	if err != nil {
		return 0.0
	}
	return float64(milliDeg) / 1000.0
}

// readMemInfo parses /proc/meminfo and returns total and available memory in bytes.
func readMemInfo() (total, avail uint64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		val, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		// Values in /proc/meminfo are in kB
		switch fields[0] {
		case "MemTotal:":
			total = val * 1024
		case "MemAvailable:":
			avail = val * 1024
		}
	}
	return total, avail
}

// readLoadAvg parses /proc/loadavg and returns the 1-minute and 5-minute load averages.
func readLoadAvg() (load1m, load5m float64) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, 0
	}
	fields := strings.Fields(string(data))
	if len(fields) < 2 {
		return 0, 0
	}
	load1m, _ = strconv.ParseFloat(fields[0], 64)
	load5m, _ = strconv.ParseFloat(fields[1], 64)
	return load1m, load5m
}

// cpuStat holds parsed values from /proc/stat's first "cpu" line.
type cpuStat struct {
	user    uint64
	nice    uint64
	system  uint64
	idle    uint64
	iowait  uint64
	irq     uint64
	softirq uint64
	total   uint64
}

// parseProcStat reads the first "cpu" line from /proc/stat.
func parseProcStat() (cpuStat, error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return cpuStat{}, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)
		// fields[0] = "cpu", fields[1..] = user, nice, system, idle, iowait, irq, softirq, ...
		if len(fields) < 8 {
			break
		}
		parseU := func(s string) uint64 {
			v, _ := strconv.ParseUint(s, 10, 64)
			return v
		}
		s := cpuStat{
			user:    parseU(fields[1]),
			nice:    parseU(fields[2]),
			system:  parseU(fields[3]),
			idle:    parseU(fields[4]),
			iowait:  parseU(fields[5]),
			irq:     parseU(fields[6]),
			softirq: parseU(fields[7]),
		}
		s.total = s.user + s.nice + s.system + s.idle + s.iowait + s.irq + s.softirq
		return s, nil
	}
	return cpuStat{}, os.ErrNotExist
}

// readCPUUsage samples /proc/stat twice with a 100ms gap to compute CPU usage percent.
func readCPUUsage() float64 {
	s1, err := parseProcStat()
	if err != nil {
		return 0.0
	}
	time.Sleep(100 * time.Millisecond)
	s2, err := parseProcStat()
	if err != nil {
		return 0.0
	}

	totalDelta := s2.total - s1.total
	if totalDelta == 0 {
		return 0.0
	}
	idleDelta := s2.idle - s1.idle
	return (1.0 - float64(idleDelta)/float64(totalDelta)) * 100.0
}
