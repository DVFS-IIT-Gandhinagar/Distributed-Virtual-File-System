export function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B';
  const gb = bytes / (1024 * 1024 * 1024);
  if (gb >= 1) return `${gb.toFixed(1)} GB`;
  const mb = bytes / (1024 * 1024);
  if (mb >= 1) return `${mb.toFixed(1)} MB`;
  const kb = bytes / 1024;
  if (kb >= 1) return `${kb.toFixed(1)} KB`;
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

export function getStorageBarClass(pct: number): string {
  if (pct >= 90) return 'bg-danger';
  if (pct >= 75) return 'bg-warning';
  return 'bg-success';
}

export function getCpuTempColor(temp: number): string {
  if (temp >= 80) return '#dc3545';
  if (temp >= 65) return '#fd7e14';
  if (temp >= 50) return '#ffc107';
  return '#198754';
}

export function timeAgo(unixSeconds: number): string {
  const diff = Math.floor(Date.now() / 1000 - unixSeconds);
  if (diff < 60) return `${diff}s ago`;
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
  return `${Math.floor(diff / 3600)}h ago`;
}
