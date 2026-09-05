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

export function formatNodeDisplayName(node: { displayName?: string; fsID?: string; fs_id?: string } | string): string {
  if (typeof node === 'string') {
    const n = parseInt(node, 10);
    return !isNaN(n) ? `FS-${n + 1}` : `FS-${node}`;
  }
  if (node.displayName) return node.displayName;
  const rawId = node.fsID || node.fs_id || '';
  const n = parseInt(rawId, 10);
  return !isNaN(n) ? `FS-${n + 1}` : `FS-${rawId}`;
}

export function formatMachineName(node: { machineName?: string; machine_name?: string; fsID?: string; fs_id?: string } | string): string {
  if (typeof node === 'string') {
    const n = parseInt(node, 10);
    return !isNaN(n) ? `dvfs${n + 1}` : `dvfs-${node}`;
  }
  if (node.machineName) return node.machineName;
  if (node.machine_name) return node.machine_name;
  const rawId = node.fsID || node.fs_id || '';
  const n = parseInt(rawId, 10);
  return !isNaN(n) ? `dvfs${n + 1}` : `dvfs-${rawId}`;
}


