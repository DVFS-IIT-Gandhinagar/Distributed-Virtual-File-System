package admin

// FileserverMetrics mirrors the Metrics struct in the fileserver package
// and is used for JSON decoding of HTTP responses from fileserver nodes.
type FileserverMetrics struct {
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
