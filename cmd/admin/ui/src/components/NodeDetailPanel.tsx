import { useNavigate } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import {
  LineChart, Line, XAxis, YAxis, Tooltip, ResponsiveContainer,
  BarChart, Bar, Cell, LabelList,
} from 'recharts';
import type { NodeInfo, Snapshot } from '../types';
import { fetchHistory } from '../api';
import { formatBytes, formatUptime, getStatusBadgeClass, getStatusColor, getCpuTempColor, formatNodeDisplayName, formatMachineName } from '../utils';

interface Props {
  node: NodeInfo;
  show: boolean;
  onClose: () => void;
}

function bytesToGB(b: number) {
  return parseFloat((b / (1024 ** 3)).toFixed(2));
}

export default function NodeDetailPanel({ node, show, onClose }: Props) {
  const navigate = useNavigate();
  const m = node.metrics;

  const { data: history } = useQuery<Snapshot[]>({
    queryKey: ['history', node.fsID],
    queryFn: () => fetchHistory(node.fsID),
    refetchInterval: 10000,
    enabled: show,
  });

  // Per-user storage chart data
  const users = Object.keys(m.per_user_storage);
  const perUserData = users.map(u => {
    const used = m.per_user_storage[u] ?? 0;
    const quota = m.per_user_quota[u] ?? used;
    const pct = quota > 0 ? (used / quota) * 100 : 0;
    return { name: u, used: bytesToGB(used), quota: bytesToGB(quota), pct };
  });

  // History chart data (last 60 snapshots)
  const recentHistory = (history ?? []).slice(-60).map(s => ({
    time: new Date(s.timestamp * 1000).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }),
    diskPct: s.metrics.disk_usage_percent,
    cpuTemp: s.metrics.cpu_temp_celsius,
  }));

  const statusColor = getStatusColor(node.status);
  const tempColor = getCpuTempColor(m.cpu_temp_celsius);

  return (
    <>
      {/* Backdrop */}
      {show && (
        <div
          className="offcanvas-backdrop fade show"
          onClick={onClose}
          style={{ zIndex: 1040 }}
        />
      )}

      {/* Offcanvas */}
      <div
        className={`offcanvas offcanvas-end ${show ? 'show' : ''}`}
        tabIndex={-1}
        style={{ width: 550, zIndex: 1045, visibility: show ? 'visible' : 'hidden' }}
        aria-label={`Node ${formatNodeDisplayName(node)} details`}
      >
        {/* Header */}
        <div className="offcanvas-header border-bottom" style={{ borderLeft: `4px solid ${statusColor}` }}>
          <div>
            <h5 className="offcanvas-title fw-bold mb-0">
              <i className="bi bi-server me-2" style={{ color: statusColor }}></i>
              Node {formatNodeDisplayName(node)} <small className="text-muted fw-normal">({formatMachineName(node)})</small>
            </h5>
            <small className="text-muted">{node.address}</small>
          </div>
          <div className="d-flex align-items-center gap-2">
            <span className={`badge ${getStatusBadgeClass(node.status)}`}>{node.status}</span>
            <button type="button" className="btn-close" onClick={onClose} aria-label="Close" />
          </div>
        </div>

        <div className="offcanvas-body" style={{ overflowY: 'auto' }}>
          {/* Scoped Quick Actions */}
          <div className="d-flex gap-2 mb-4 p-2 rounded bg-light border">
            <button
              type="button"
              className="btn btn-outline-primary btn-sm flex-fill d-flex align-items-center justify-content-center gap-1"
              onClick={() => {
                onClose();
                navigate(`/actions?node=${node.fsID}&action=restart`);
              }}
              title={`Restart Node ${formatNodeDisplayName(node)}`}
            >
              <i className="bi bi-arrow-repeat"></i>Restart
            </button>
            <button
              type="button"
              className="btn btn-outline-danger btn-sm flex-fill d-flex align-items-center justify-content-center gap-1"
              onClick={() => {
                onClose();
                navigate(`/actions?node=${node.fsID}&action=reboot`);
              }}
              title={`Reboot Machine for ${formatNodeDisplayName(node)}`}
            >
              <i className="bi bi-power"></i>Reboot
            </button>
            <button
              type="button"
              className="btn btn-outline-secondary btn-sm flex-fill d-flex align-items-center justify-content-center gap-1"
              onClick={() => {
                onClose();
                navigate(`/actions?node=${node.fsID}&action=pull`);
              }}
              title={`Pull repo on Node ${formatNodeDisplayName(node)}`}
            >
              <i className="bi bi-git"></i>Git Pull
            </button>
            <button
              type="button"
              className="btn btn-outline-secondary btn-sm flex-fill d-flex align-items-center justify-content-center gap-1"
              onClick={() => {
                onClose();
                navigate(`/actions?node=${node.fsID}&action=logs`);
              }}
              title={`View logs for Node ${formatNodeDisplayName(node)}`}
            >
              <i className="bi bi-journal-text"></i>Logs
            </button>
          </div>

          {/* Quick Stats */}
          <div className="row g-2 mb-4">
            {[
              { label: 'CPU Usage', value: `${m.cpu_usage_percent.toFixed(1)}%`, icon: 'bi-cpu' },
              { label: 'RAM Usage', value: `${m.mem_usage_percent.toFixed(1)}%`, icon: 'bi-memory' },
              { label: 'Chunks', value: m.chunk_count, icon: 'bi-box' },
              {
                label: 'Connections',
                value: m.active_users && m.active_users.length > 0
                  ? `${m.active_connections} (${m.active_users.join(', ')})`
                  : m.active_connections,
                icon: 'bi-diagram-2'
              },
              { label: 'Load 1m', value: m.load_avg_1m.toFixed(2), icon: 'bi-graph-up' },
              { label: 'Uptime', value: formatUptime(m.uptime_seconds), icon: 'bi-clock' },
            ].map(s => (
              <div key={s.label} className="col-6 col-md-4">
                <div className="p-2 rounded" style={{ background: '#f8f9fa', border: '1px solid #e9ecef' }}>
                  <div className="d-flex align-items-center gap-2">
                    <i className={`bi ${s.icon} text-primary`}></i>
                    <div>
                      <div className="small text-muted" style={{ fontSize: '0.7rem' }}>{s.label}</div>
                      <div className="fw-semibold small">{s.value}</div>
                    </div>
                  </div>
                </div>
              </div>
            ))}
          </div>

          {/* CPU Temp */}
          <div className="d-flex align-items-center mb-4 p-3 rounded" style={{ background: '#f8f9fa' }}>
            <span style={{ fontSize: '1.5rem' }}>🌡</span>
            <div className="ms-3">
              <div className="small text-muted">CPU Temperature</div>
              <div className="fw-bold" style={{ fontSize: '1.5rem', color: tempColor }}>
                {m.cpu_temp_celsius.toFixed(1)}°C
              </div>
            </div>
            <div className="ms-auto text-end">
              <div className="small text-muted">Memory</div>
              <div className="small fw-semibold">
                {formatBytes(m.mem_used_bytes)} / {formatBytes(m.mem_total_bytes)}
              </div>
            </div>
          </div>

          {/* Storage */}
          <div className="mb-4">
            <h6 className="fw-semibold mb-2">
              <i className="bi bi-hdd me-1 text-primary"></i>Storage
            </h6>
            <div className="d-flex justify-content-between small text-muted mb-1">
              <span>{formatBytes(m.disk_used_bytes)} used</span>
              <span>{formatBytes(m.disk_total_bytes)} total</span>
            </div>
            <div className="progress mb-1" style={{ height: 12 }}>
              <div
                className="progress-bar"
                style={{ width: `${Math.min(m.disk_usage_percent, 100)}%`, backgroundColor: statusColor }}
              >
                <span className="small">{m.disk_usage_percent.toFixed(1)}%</span>
              </div>
            </div>
            <small className="text-muted">{m.chunk_count} chunks stored</small>
          </div>

          {/* Per-User Storage */}
          {perUserData.length > 0 && (
            <div className="mb-4">
              <h6 className="fw-semibold mb-2">
                <i className="bi bi-people me-1 text-primary"></i>Per-User Storage
              </h6>
              <ResponsiveContainer width="100%" height={Math.max(perUserData.length * 40, 80)}>
                <BarChart
                  data={perUserData}
                  layout="vertical"
                  margin={{ top: 0, right: 50, left: 40, bottom: 0 }}
                >
                  <XAxis type="number" hide />
                  <YAxis type="category" dataKey="name" tick={{ fontSize: 12 }} width={50} />
                  <Tooltip
                    formatter={(val: number, name: string) => [
                      `${val} GB`,
                      name === 'used' ? 'Used' : 'Quota',
                    ]}
                  />
                  <Bar dataKey="quota" name="Quota" fill="#dee2e6" radius={[4, 4, 4, 4]} />
                  <Bar dataKey="used" name="Used" fill="#0d6efd" radius={[4, 4, 4, 4]}>
                    {perUserData.map((entry, i) => (
                      <Cell
                        key={i}
                        fill={entry.pct >= 90 ? '#dc3545' : entry.pct >= 75 ? '#ffc107' : '#0d6efd'}
                      />
                    ))}
                    <LabelList
                      dataKey="pct"
                      position="right"
                      formatter={(v: number) => `${v.toFixed(0)}%`}
                      style={{ fontSize: 11, fill: '#6c757d' }}
                    />
                  </Bar>
                </BarChart>
              </ResponsiveContainer>
            </div>
          )}

          {/* History Charts */}
          <div className="mb-2">
            <h6 className="fw-semibold mb-3">
              <i className="bi bi-graph-up me-1 text-primary"></i>Historical Metrics
            </h6>

            {recentHistory.length === 0 ? (
              <p className="text-muted small">
                <i className="bi bi-hourglass-split me-1"></i>
                No history data available yet.
              </p>
            ) : (
              <>
                {/* Disk Usage % */}
                <p className="small text-muted mb-1 fw-semibold">Disk Usage % (last 60 snapshots)</p>
                <ResponsiveContainer width="100%" height={150}>
                  <LineChart data={recentHistory} margin={{ top: 5, right: 10, left: -10, bottom: 0 }}>
                    <XAxis dataKey="time" tick={{ fontSize: 10 }} interval="preserveStartEnd" />
                    <YAxis domain={[0, 100]} tick={{ fontSize: 10 }} unit="%" />
                    <Tooltip formatter={(v: number) => `${v.toFixed(1)}%`} />
                    <Line
                      type="monotone"
                      dataKey="diskPct"
                      name="Disk %"
                      stroke="#6f42c1"
                      dot={false}
                      strokeWidth={2}
                    />
                  </LineChart>
                </ResponsiveContainer>

                {/* CPU Temp */}
                <p className="small text-muted mb-1 fw-semibold mt-3">CPU Temperature °C (last 60 snapshots)</p>
                <ResponsiveContainer width="100%" height={150}>
                  <LineChart data={recentHistory} margin={{ top: 5, right: 10, left: -10, bottom: 0 }}>
                    <XAxis dataKey="time" tick={{ fontSize: 10 }} interval="preserveStartEnd" />
                    <YAxis tick={{ fontSize: 10 }} unit="°C" />
                    <Tooltip formatter={(v: number) => `${v.toFixed(1)}°C`} />
                    <Line
                      type="monotone"
                      dataKey="cpuTemp"
                      name="CPU Temp"
                      stroke="#dc3545"
                      dot={false}
                      strokeWidth={2}
                    />
                  </LineChart>
                </ResponsiveContainer>
              </>
            )}
          </div>
        </div>
      </div>
    </>
  );
}
