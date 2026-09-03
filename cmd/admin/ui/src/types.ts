export interface NodeMetrics {
  disk_total_bytes: number;
  disk_used_bytes: number;
  disk_free_bytes: number;
  disk_usage_percent: number;
  per_user_storage: Record<string, number>;
  per_user_quota: Record<string, number>;
  chunk_count: number;
  cpu_temp_celsius: number;
  cpu_usage_percent: number;
  mem_used_bytes: number;
  mem_total_bytes: number;
  mem_usage_percent: number;
  load_avg_1m: number;
  load_avg_5m: number;
  uptime_seconds: number;
  last_restart_unix: number;
  active_connections: number;
  users_assigned_count: number;
}

export interface NodeInfo {
  fsID: string;
  address: string;
  metricsURL: string;
  status: string;
  lastSeen: number;
  metrics: NodeMetrics;
}

export interface ClusterResponse {
  nodes: NodeInfo[];
  users: Record<string, string>;
  node_count: number;
  online_count: number;
  total_storage_bytes: number;
  used_storage_bytes: number;
  total_users: number;
}

export interface Snapshot {
  timestamp: number;
  metrics: NodeMetrics;
}
