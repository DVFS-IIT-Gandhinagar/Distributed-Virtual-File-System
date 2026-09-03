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
	ActiveConnections int               `json:"active_connections"`
	UsersAssigned     int               `json:"users_assigned_count"`
}
