import type { ClusterResponse, Snapshot } from './types';

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
