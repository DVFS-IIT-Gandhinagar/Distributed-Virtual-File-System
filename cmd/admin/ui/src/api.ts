import type {
  ClusterResponse,
  Snapshot,
  UserSummary,
  NodeRestartParams,
  CommandRecord,
  ActionRequest,
  PerformanceResponse,
  ClusterHistorySnapshot,
  Alert,
  AlertSummary,
  AlertFilters,
  LogTailResponse,
} from './types';

export async function fetchCluster(): Promise<ClusterResponse> {
  const res = await fetch('/api/cluster');
  if (!res.ok) throw new Error(`fetchCluster: ${res.status} ${res.statusText}`);
  return res.json() as Promise<ClusterResponse>;
}

export async function fetchPerformance(): Promise<PerformanceResponse> {
  const res = await fetch('/api/performance');
  if (!res.ok) throw new Error(`fetchPerformance: ${res.status} ${res.statusText}`);
  return res.json() as Promise<PerformanceResponse>;
}

export function getPerformanceExportUrl(nodeId?: string): string {
  if (nodeId) {
    return `/api/performance/export?node_id=${encodeURIComponent(nodeId)}`;
  }
  return '/api/performance/export';
}

export async function fetchHistory(fsID: string): Promise<Snapshot[]> {
  const res = await fetch(`/api/history/${fsID}`);
  if (!res.ok) throw new Error(`fetchHistory: ${res.status} ${res.statusText}`);
  return res.json() as Promise<Snapshot[]>;
}

export async function fetchUsers(): Promise<UserSummary[]> {
  const res = await fetch('/api/users');
  if (!res.ok) throw new Error(`fetchUsers: ${res.status} ${res.statusText}`);
  return res.json() as Promise<UserSummary[]>;
}

export async function updateUserQuota(username: string, quotaBytes: number): Promise<{ success: boolean; quota_bytes: number }> {
  const res = await fetch(`/api/users/${encodeURIComponent(username)}/quota`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ quota_bytes: quotaBytes }),
  });
  if (!res.ok) {
    const errData = await res.json().catch(() => ({ error: res.statusText }));
    throw new Error(errData.error || `updateUserQuota: ${res.status} ${res.statusText}`);
  }
  return res.json();
}

export async function fetchActionPresets(): Promise<Record<string, NodeRestartParams>> {
  const res = await fetch('/api/actions/presets');
  if (!res.ok) throw new Error(`fetchActionPresets: ${res.status} ${res.statusText}`);
  return res.json() as Promise<Record<string, NodeRestartParams>>;
}

export async function fetchCommandHistory(): Promise<CommandRecord[]> {
  const res = await fetch('/api/actions/history');
  if (!res.ok) throw new Error(`fetchCommandHistory: ${res.status} ${res.statusText}`);
  return res.json() as Promise<CommandRecord[]>;
}

export async function executeActionRest(req: ActionRequest): Promise<CommandRecord> {
  const res = await fetch('/api/actions/execute', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  });
  if (!res.ok) {
    const errData = await res.json().catch(() => ({ error: res.statusText }));
    throw new Error(errData.error || `executeActionRest: ${res.status} ${res.statusText}`);
  }
  return res.json() as Promise<CommandRecord>;
}

export async function fetchClusterHistory(): Promise<ClusterHistorySnapshot[]> {
  const res = await fetch('/api/history/cluster');
  if (!res.ok) throw new Error(`fetchClusterHistory: ${res.status} ${res.statusText}`);
  return res.json() as Promise<ClusterHistorySnapshot[]>;
}

export async function fetchAlerts(filters?: AlertFilters): Promise<Alert[]> {
  const params = new URLSearchParams();
  if (filters?.severity) params.set('severity', filters.severity);
  if (filters?.node_id) params.set('node_id', filters.node_id);
  if (filters?.unresolved) params.set('unresolved', 'true');
  if (filters?.limit) params.set('limit', filters.limit.toString());

  const url = `/api/alerts${params.toString() ? `?${params.toString()}` : ''}`;
  const res = await fetch(url);
  if (!res.ok) throw new Error(`fetchAlerts: ${res.status} ${res.statusText}`);
  return res.json() as Promise<Alert[]>;
}

export async function fetchAlertSummary(): Promise<AlertSummary> {
  const res = await fetch('/api/alerts/summary');
  if (!res.ok) throw new Error(`fetchAlertSummary: ${res.status} ${res.statusText}`);
  return res.json() as Promise<AlertSummary>;
}

export async function resolveAlert(id: string): Promise<boolean> {
  const res = await fetch('/api/alerts/resolve', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ id }),
  });
  if (!res.ok) throw new Error(`resolveAlert: ${res.status} ${res.statusText}`);
  const data = await res.json();
  return data.success;
}

export async function resolveAllAlerts(): Promise<number> {
  const res = await fetch('/api/alerts/resolve-all', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
  });
  if (!res.ok) throw new Error(`resolveAllAlerts: ${res.status} ${res.statusText}`);
  const data = await res.json();
  return data.resolved_count;
}

export async function fetchLogTail(nodeId: string, lines = 100, service = 'fileserver', mode = 'journalctl'): Promise<LogTailResponse> {
  const params = new URLSearchParams({
    node: nodeId,
    lines: lines.toString(),
    service,
    mode,
  });
  const res = await fetch(`/api/logs/tail?${params.toString()}`);
  if (!res.ok) throw new Error(`fetchLogTail: ${res.status} ${res.statusText}`);
  return res.json() as Promise<LogTailResponse>;
}

