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
	ActiveConnections   int               `json:"active_connections"`
	ActiveUsers         []string          `json:"active_users"`
	UsersAssigned       int               `json:"users_assigned_count"`
	BytesWrittenTotal   uint64            `json:"bytes_written_total"`
	BytesReadTotal      uint64            `json:"bytes_read_total"`
	WriteOpsTotal       uint64            `json:"write_ops_total"`
	ReadOpsTotal        uint64            `json:"read_ops_total"`
	ErrorsTotal         uint64            `json:"errors_total"`
	FailedWritesTotal   uint64            `json:"failed_writes_total"`
	FailedReadsTotal    uint64            `json:"failed_reads_total"`
	OpLatencyWriteMsP50 float64           `json:"op_latency_write_ms_p50"`
	OpLatencyWriteMsP95 float64           `json:"op_latency_write_ms_p95"`
	OpLatencyWriteMsP99 float64           `json:"op_latency_write_ms_p99"`
	OpLatencyReadMsP50  float64           `json:"op_latency_read_ms_p50"`
	OpLatencyReadMsP95  float64           `json:"op_latency_read_ms_p95"`
	OpLatencyReadMsP99  float64           `json:"op_latency_read_ms_p99"`
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
		perUserQuota[username] = fs.getUserQuotaLocked(username)
	}

	// Count file inodes (chunks)
	chunkCount := 0
	for _, inode := range fs.inodes {
		if inode.Type == domain.InodeTypeFile {
			chunkCount++
		}
	}

	now := time.Now()
	activeUsers := make([]string, 0)
	for username, sess := range fs.sessions {
		if fs.isSessionActiveLocked(sess, now) {
			activeUsers = append(activeUsers, username)
		}
	}
	activeConns := len(activeUsers)
	usersAssigned := len(fs.users)
	rootDir := fs.rootDir
	startTime := fs.startTime

	fs.mu.RUnlock()

	// Disk stats (max usable space is disk space - 20 GiB safety buffer)
	diskTotal, diskUsed, diskFree, diskPct := readFileserverDiskStats(rootDir)

	// CPU temperature
	cpuTemp := readCPUTemp()

	// Memory
	memTotal, memAvail := readMemInfo()
	var memUsed uint64
	var memPct float64
	if memTotal > 0 {
		if memTotal > memAvail {
			memUsed = memTotal - memAvail
		}
		memPct = float64(memUsed) / float64(memTotal) * 100.0
	}

	// Load average
	load1m, load5m := readLoadAvg()

	// CPU usage
	cpuUsage := readCPUUsage()

	// Operation metrics and latency percentiles
	opSnap := fs.OpMetricsSnapshot()

	return Metrics{
		DiskTotalBytes:      diskTotal,
		DiskUsedBytes:       diskUsed,
		DiskFreeBytes:       diskFree,
		DiskUsagePercent:    diskPct,
		PerUserStorage:      perUserStorage,
		PerUserQuota:        perUserQuota,
		ChunkCount:          chunkCount,
		CPUTempCelsius:      cpuTemp,
		CPUUsagePercent:     cpuUsage,
		MemUsedBytes:        memUsed,
		MemTotalBytes:       memTotal,
		MemUsagePercent:     memPct,
		LoadAvg1m:           load1m,
		LoadAvg5m:           load5m,
		UptimeSeconds:       time.Since(startTime).Seconds(),
		LastRestartUnix:     startTime.Unix(),
		ActiveConnections:   activeConns,
		ActiveUsers:         activeUsers,
		UsersAssigned:       usersAssigned,
		BytesWrittenTotal:   opSnap.BytesWrittenTotal,
		BytesReadTotal:      opSnap.BytesReadTotal,
		WriteOpsTotal:       opSnap.WriteOpsTotal,
		ReadOpsTotal:        opSnap.ReadOpsTotal,
		ErrorsTotal:         opSnap.ErrorsTotal,
		FailedWritesTotal:   opSnap.FailedWritesTotal,
		FailedReadsTotal:    opSnap.FailedReadsTotal,
		OpLatencyWriteMsP50: opSnap.OpLatencyWriteMsP50,
		OpLatencyWriteMsP95: opSnap.OpLatencyWriteMsP95,
		OpLatencyWriteMsP99: opSnap.OpLatencyWriteMsP99,
		OpLatencyReadMsP50:  opSnap.OpLatencyReadMsP50,
		OpLatencyReadMsP95:  opSnap.OpLatencyReadMsP95,
		OpLatencyReadMsP99:  opSnap.OpLatencyReadMsP99,
	}
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
	if s2.idle > s2.total || s1.idle > s1.total {
		return 0.0
	}
	idleDelta := int64(s2.idle) - int64(s1.idle)
	if idleDelta < 0 {
		idleDelta = 0
	}
	usage := (1.0 - float64(idleDelta)/float64(totalDelta)) * 100.0
	if usage < 0.0 {
		usage = 0.0
	} else if usage > 100.0 {
		usage = 100.0
	}
	return usage
}

// readFileserverDiskStats reads disk statistics and applies the 20 GiB safety buffer,
// ensuring the fileserver's max usable space is capped at (disk space - 20 GiB).
func readFileserverDiskStats(path string) (total, used, free uint64, pct float64) {
	rawTotal, rawUsed, rawFree, _ := readDiskStats(path)
	if rawTotal > DiskSafetyBuffer {
		total = rawTotal - DiskSafetyBuffer
	} else {
		total = rawTotal
	}
	if rawFree > DiskSafetyBuffer {
		free = rawFree - DiskSafetyBuffer
	} else {
		free = 0
	}
	used = rawUsed
	if total > 0 {
		pct = float64(used) / float64(total) * 100.0
		if pct > 100.0 {
			pct = 100.0
		}
	}
	return total, used, free, pct
}
