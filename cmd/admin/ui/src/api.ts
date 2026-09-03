import type { ClusterResponse, Snapshot, UserSummary, NodeRestartParams, CommandRecord, ActionRequest } from './types';

export async function fetchCluster(): Promise<ClusterResponse> {
  const res = await fetch('/api/cluster');
  if (!res.ok) throw new Error(`fetchCluster: ${res.status} ${res.statusText}`);
  return res.json() as Promise<ClusterResponse>;
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

