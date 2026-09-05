import type { NodeInfo } from '../types';
import { formatBytes, formatUptime, getStatusBadgeClass, getStatusColor, getStorageBarClass, getStorageBarColor, getCpuTempColor, formatNodeDisplayName, formatMachineName } from '../utils';

interface Props {
  node: NodeInfo;
  onClick: () => void;
}

export default function NodeCard({ node, onClick }: Props) {
  const m = node.metrics;
  const statusColor = getStatusColor(node.status);

  if (!m) {
    return (
      <div
        className="card h-100 shadow-sm border-0"
        style={{ borderLeft: `4px solid ${statusColor}`, cursor: 'pointer', opacity: 0.85 }}
        onClick={onClick}
      >
        <div className="card-body">
          <div className="d-flex justify-content-between align-items-center mb-1">
            <div>
              <span className="fw-bold fs-6">{formatNodeDisplayName(node)}</span>
              <small className="text-muted ms-2">({formatMachineName(node)})</small>
            </div>
            <span className={`badge ${getStatusBadgeClass(node.status)}`}>
              {node.status}
            </span>
          </div>

          <p className="text-muted small mb-3 text-truncate" title={node.address}>
            {node.address}
          </p>

          <div className="text-center py-4 my-2 text-muted bg-light rounded">
            <i className="bi bi-hdd-network-fill d-block mb-1" style={{ fontSize: '1.8rem', opacity: 0.5 }}></i>
            <span className="small">Node Offline &mdash; No telemetry</span>
          </div>

          <div className="d-flex justify-content-between text-muted small pt-2 border-top">
            <span>⏱ Offline</span>
            <span>🔗 0 connections</span>
          </div>
        </div>
      </div>
    );
  }

  const storageLabel = `${formatBytes(m.disk_used_bytes)} / ${formatBytes(m.disk_total_bytes)} (${m.disk_usage_percent.toFixed(1)}%)`;
  const tempColor = getCpuTempColor(m.cpu_temp_celsius);

  return (
    <div
      className="card h-100 shadow-sm border-0"
      style={{ borderLeft: `4px solid ${statusColor}`, cursor: 'pointer' }}
      onClick={onClick}
    >
      <div className="card-body">
        {/* Header: 1-indexed FS-ID + machine name + status badge */}
        <div className="d-flex justify-content-between align-items-center mb-1">
          <div>
            <span className="fw-bold fs-6">{formatNodeDisplayName(node)}</span>
            <small className="text-muted ms-2">({formatMachineName(node)})</small>
          </div>
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
          <span
            title={m.active_users && m.active_users.length > 0 ? `Active: ${m.active_users.join(', ')}` : 'Active connections'}
            className={m.active_connections > 0 ? 'text-success fw-semibold' : ''}
          >
            <i className={`bi bi-link-45deg me-1 ${m.active_connections > 0 ? 'text-success' : ''}`}></i>
            {m.active_connections} {m.active_connections === 1 ? 'conn' : 'conns'}
          </span>
          <span title="Uptime">
            <i className="bi bi-clock me-1"></i>{formatUptime(m.uptime_seconds)}
          </span>
        </div>
      </div>
    </div>
  );
}
