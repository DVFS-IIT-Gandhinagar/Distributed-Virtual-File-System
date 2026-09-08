export function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B';
  const tib = bytes / (1024 * 1024 * 1024 * 1024);
  if (tib >= 1) return `${tib.toFixed(1)} TiB`;
  const gib = bytes / (1024 * 1024 * 1024);
  if (gib >= 1) return `${gib.toFixed(1)} GiB`;
  const mib = bytes / (1024 * 1024);
  if (mib >= 1) return `${mib.toFixed(1)} MiB`;
  const kib = bytes / 1024;
  if (kib >= 1) return `${kib.toFixed(1)} KiB`;
  return `${bytes} B`;
}

export function formatUptime(seconds: number): string {
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  return `${d}d ${h}h ${m}m`;
}

export function getStatusColor(status: string): string {
  switch (status) {
    case 'online': return '#198754';
    case 'warning': return '#ffc107';
    case 'degraded': return '#fd7e14';
    case 'critical': return '#dc3545';
    default: return '#6c757d';
  }
}

export function getStatusBadgeClass(status: string): string {
  switch (status) {
    case 'online': return 'bg-success';
    case 'warning': return 'bg-warning text-dark';
    case 'degraded': return 'bg-warning text-dark';
    case 'critical': return 'bg-danger';
    default: return 'bg-secondary';
  }
}

export function getStorageBarColor(pct: number): string {
  if (pct > 95) return '#dc3545';
  if (pct > 90) return '#fd7e14';
  if (pct > 80) return '#ffc107';
  return '#198754';
}

export function getStorageBarClass(pct: number): string {
  if (pct > 95) return 'bg-danger';
  if (pct > 80) return 'bg-warning text-dark';
  return 'bg-success';
}

export function getCpuTempColor(temp: number): string {
  if (temp > 85) return '#dc3545';
  if (temp > 75) return '#fd7e14';
  if (temp > 65) return '#ffc107';
  return '#198754';
}

export function timeAgo(unixSeconds: number): string {
  const diff = Math.floor(Date.now() / 1000 - unixSeconds);
  if (diff < 60) return `${diff}s ago`;
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
  return `${Math.floor(diff / 3600)}h ago`;
}

export function getUserQuotaColor(pct: number): string {
  if (pct > 100) return '#dc3545';
  if (pct >= 95) return '#fd7e14';
  if (pct >= 80) return '#ffc107';
  return '#198754';
}

export function getUserQuotaBadge(pct: number): { label: string; badgeClass: string; color: string } | null {
  if (pct > 100) return { label: 'Exceeded', badgeClass: 'bg-danger text-white', color: '#dc3545' };
  if (pct >= 95) return { label: 'Near Limit', badgeClass: 'text-dark', color: '#fd7e14' };
  if (pct >= 80) return { label: 'Warning', badgeClass: 'bg-warning text-dark', color: '#ffc107' };
  return null;
}

export interface CachedNodeInfo {
  fsID: string;
  displayID?: number;
  displayName?: string;
  machineName?: string;
  address?: string;
}

const clusterNodesCache = new Map<string, CachedNodeInfo>();

export function clearClusterNodesCache(): void {
  clusterNodesCache.clear();
}

export function updateClusterNodesCache(nodes: any[], fullSync: boolean = false): void {
  if (!Array.isArray(nodes)) return;
  if (fullSync) {
    const activeIds = new Set<string>();
    for (const n of nodes) {
      if (!n) continue;
      const id = String(n.fsID ?? n.fs_id ?? '');
      if (id !== '') {
        activeIds.add(id);
        clusterNodesCache.set(id, {
          fsID: id,
          displayID: n.displayID ?? n.display_id,
          displayName: n.displayName ?? n.display_name,
          machineName: n.machineName ?? n.machine_name,
          address: n.address,
        });
      }
    }
    // Prune deleted/removed nodes that are no longer in the authoritative cluster response
    for (const key of Array.from(clusterNodesCache.keys())) {
      if (!activeIds.has(key)) {
        clusterNodesCache.delete(key);
      }
    }
  } else {
    for (const n of nodes) {
      if (!n) continue;
      const id = String(n.fsID ?? n.fs_id ?? '');
      if (id !== '') {
        clusterNodesCache.set(id, {
          fsID: id,
          displayID: n.displayID ?? n.display_id,
          displayName: n.displayName ?? n.display_name,
          machineName: n.machineName ?? n.machine_name,
          address: n.address,
        });
      }
    }
  }
}

export function formatNodeDisplayName(node: any): string {
  if (!node && node !== 0) return '';
  if (typeof node === 'string' || typeof node === 'number') {
    const key = String(node);
    const cached = clusterNodesCache.get(key);
    if (cached?.displayName) return cached.displayName;
    const n = parseInt(key, 10);
    return !isNaN(n) ? `FS-${n + 1}` : `FS-${key}`;
  }
  if (node.displayName) return node.displayName;
  if (node.display_name) return node.display_name;
  if (node.home_fs_display) return node.home_fs_display;
  const rawId = node.fsID ?? node.fs_id ?? node.home_fs_id;
  if (rawId !== undefined && rawId !== null) {
    const key = String(rawId);
    const cached = clusterNodesCache.get(key);
    if (cached?.displayName) return cached.displayName;
    const n = parseInt(key, 10);
    return !isNaN(n) ? `FS-${n + 1}` : `FS-${key}`;
  }
  return '';
}

export function formatMachineName(node: any): string {
  if (!node && node !== 0) return '';
  if (typeof node === 'string' || typeof node === 'number') {
    const key = String(node);
    const cached = clusterNodesCache.get(key);
    if (cached?.machineName) return cached.machineName;
    const n = parseInt(key, 10);
    return !isNaN(n) ? `dvfs${n + 1}` : `dvfs-${key}`;
  }
  if (node.machineName) return node.machineName;
  if (node.machine_name) return node.machine_name;
  if (node.home_fs_machine) return node.home_fs_machine;
  const rawId = node.fsID ?? node.fs_id ?? node.home_fs_id;
  if (rawId !== undefined && rawId !== null) {
    const key = String(rawId);
    const cached = clusterNodesCache.get(key);
    if (cached?.machineName) return cached.machineName;
    const n = parseInt(key, 10);
    return !isNaN(n) ? `dvfs${n + 1}` : `dvfs-${key}`;
  }
  return '';
}


