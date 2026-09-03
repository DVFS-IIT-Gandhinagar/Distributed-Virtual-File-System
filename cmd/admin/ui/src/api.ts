import type { ClusterResponse, Snapshot, UserSummary } from './types';

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
