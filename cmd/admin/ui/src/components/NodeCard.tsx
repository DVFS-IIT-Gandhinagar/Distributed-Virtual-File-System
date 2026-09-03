import type { NodeInfo } from '../types';
import { formatBytes, formatUptime, getStatusBadgeClass, getStatusColor, getStorageBarClass, getStorageBarColor, getCpuTempColor } from '../utils';

interface Props {
  node: NodeInfo;
  onClick: () => void;
}

export default function NodeCard({ node, onClick }: Props) {
  const m = node.metrics;
  const storageLabel = `${formatBytes(m.disk_used_bytes)} / ${formatBytes(m.disk_total_bytes)} (${m.disk_usage_percent.toFixed(1)}%)`;
  const statusColor = getStatusColor(node.status);
  const tempColor = getCpuTempColor(m.cpu_temp_celsius);

  return (
    <div
      className="card h-100 shadow-sm border-0"
      style={{ borderLeft: `4px solid ${statusColor}`, cursor: 'pointer' }}
      onClick={onClick}
    >
      <div className="card-body">
        {/* Header: FS-ID + status badge */}
        <div className="d-flex justify-content-between align-items-center mb-1">
          <span className="fw-bold fs-6">FS-{node.fsID}</span>
          <span className={`badge ${getStatusBadgeClass(node.status)}`}>
            {node.status}
          </span>
        </div>

        {/* IP Address */}
        <p className="text-muted small mb-3 text-truncate" title={node.address}>
          {node.address}
        </p>

        {/* Storage */}
        <div className="mb-3">
          <div className="d-flex justify-content-between align-items-center mb-1">
            <small className="text-muted fw-semibold">Storage</small>
            <small className="text-muted">{storageLabel}</small>
          </div>
          <div className="progress" style={{ height: 8 }}>
            <div
              className={`progress-bar ${getStorageBarClass(m.disk_usage_percent)}`}
              style={{
                width: `${Math.min(m.disk_usage_percent, 100)}%`,
                backgroundColor: getStorageBarColor(m.disk_usage_percent),
              }}
            />
          </div>
        </div>

        {/* CPU Temp */}
        <div className="d-flex align-items-center mb-3">
          <span className="me-2" style={{ fontSize: '1.1rem' }}>🌡</span>
          <span className="small">CPU Temp: </span>
          <span className="fw-semibold ms-1" style={{ color: tempColor }}>
            {m.cpu_temp_celsius.toFixed(1)}°C
          </span>
        </div>

        {/* CPU Usage */}
        <div className="mb-2">
          <div className="d-flex justify-content-between mb-1">
            <small className="text-muted">CPU Usage</small>
            <small className="fw-semibold">{m.cpu_usage_percent.toFixed(1)}%</small>
          </div>
          <div className="progress" style={{ height: 6 }}>
            <div
              className={`progress-bar ${m.cpu_usage_percent >= 85 ? 'bg-danger' : m.cpu_usage_percent >= 70 ? 'bg-warning' : 'bg-info'}`}
              style={{ width: `${Math.min(m.cpu_usage_percent, 100)}%` }}
            />
          </div>
        </div>

        {/* RAM Usage */}
        <div className="mb-0">
          <div className="d-flex justify-content-between mb-1">
            <small className="text-muted">RAM Usage</small>
            <small className="fw-semibold">{m.mem_usage_percent.toFixed(1)}%</small>
          </div>
          <div className="progress" style={{ height: 6 }}>
            <div
              className={`progress-bar ${m.mem_usage_percent >= 85 ? 'bg-danger' : m.mem_usage_percent >= 70 ? 'bg-warning' : 'bg-primary'}`}
              style={{ width: `${Math.min(m.mem_usage_percent, 100)}%` }}
            />
          </div>
        </div>
      </div>

      <div className="card-footer bg-white border-top py-2">
        <div className="d-flex justify-content-between align-items-center small text-muted">
          <span title="Users assigned">
            <i className="bi bi-people me-1"></i>{m.users_assigned_count} users
          </span>
          <span title="Active connections">
            <i className="bi bi-diagram-2 me-1"></i>{m.active_connections} conn
          </span>
          <span title="Uptime">
            <i className="bi bi-clock me-1"></i>{formatUptime(m.uptime_seconds)}
          </span>
        </div>
      </div>
    </div>
  );
}
