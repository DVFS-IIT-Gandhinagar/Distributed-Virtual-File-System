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
  active_users?: string[];
  users_assigned_count: number;
}

export interface NodeInfo {
  fsID: string;
  displayID?: number;
  displayName?: string;
  machineName?: string;
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
  online_users?: number;
}

export interface Snapshot {
  timestamp: number;
  metrics: NodeMetrics;
}

export interface NodeUserStorage {
  fs_id: string;
  display_id?: number;
  display_name?: string;
  machine_name?: string;
  address: string;
  used_bytes: number;
  quota_bytes: number;
}

export interface UserSummary {
  username: string;
  home_fs_id: string;
  home_fs_display?: string;
  home_fs_machine?: string;
  home_fs_address: string;
  quota_limit: number;
  quota_used: number;
  usage_percent: number;
  active_sessions: number;
  is_online?: boolean;
  nodes: NodeUserStorage[];
}

export type ActionType = 'pull' | 'build' | 'restart' | 'reboot' | 'logs' | 'custom';

export interface NodeRestartParams {
  fs_id: string;
  address: string;
  host: string;
  port: number;
  ssh_port?: number;
  meta_addr: string;
  own_ip: string;
  data_dir: string;
}

export interface ActionRequest {
  action_type: ActionType;
  target_node_ids: string[];
  custom_command?: string;
  repo_path?: string;
  git_branch?: string;
  make_target?: string;
  timeout_seconds?: number;
  ssh_port?: number;
  log_lines?: number;
  log_path?: string;
  restart_mode?: 'systemctl' | 'binary';
  log_mode?: 'journalctl' | 'tail';
  restart_params?: Record<string, NodeRestartParams>;
  ssh_user?: string;
  ssh_key_path?: string;
}

export interface NodeResult {
  node_id: string;
  address: string;
  exit_code: number;
  output: string;
  error?: string;
  duration_ms: number;
}

export interface CommandRecord {
  id: string;
  timestamp: number;
  action_type: ActionType;
  command: string;
  target_nodes: string[];
  status: 'running' | 'success' | 'failed';
  duration_ms: number;
  node_results: Record<string, NodeResult>;
}

export interface ActionEvent {
  type: 'action_started' | 'node_started' | 'node_output' | 'node_finished' | 'action_finished' | 'error';
  action_id: string;
  command?: string;
  node_id?: string;
  address?: string;
  stream?: 'stdout' | 'stderr';
  chunk?: string;
  exit_code?: number;
  duration_ms?: number;
  status?: string;
  error?: string;
}


