import { useMemo } from 'react';
import { useNavigate } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import {
  BarChart, Bar, XAxis, YAxis, Tooltip, Legend, ResponsiveContainer, Cell,
} from 'recharts';
import { fetchCluster } from '../api';
import StatCard from '../components/StatCard';
import { formatBytes, getStatusColor, getStatusBadgeClass, getStorageBarClass, getStorageBarColor, formatNodeDisplayName, formatMachineName } from '../utils';

export default function Overview() {
  const navigate = useNavigate();
  const { data: cluster, isLoading, isError } = useQuery({
    queryKey: ['cluster'],
    queryFn: fetchCluster,
    refetchInterval: 5000,
  });

  const sortedNodes = useMemo(() => {
    if (!cluster?.nodes) return [];
    return [...cluster.nodes].sort((a, b) => {
      const numA = parseInt(a.fsID, 10);
      const numB = parseInt(b.fsID, 10);
      if (!isNaN(numA) && !isNaN(numB)) return numA - numB;
      return a.fsID.localeCompare(b.fsID);
    });
  }, [cluster?.nodes]);

  if (isLoading) {
    return (
      <div className="container-fluid py-5 text-center">
        <div className="spinner-border text-primary" role="status">
          <span className="visually-hidden">Loading...</span>
        </div>
        <p className="mt-3 text-muted">Connecting to cluster...</p>
      </div>
    );
  }

  if (isError || !cluster) {
    return (
      <div className="container-fluid py-5 text-center">
        <i className="bi bi-exclamation-triangle text-danger" style={{ fontSize: '3rem' }}></i>
        <p className="mt-3 text-danger">Failed to fetch cluster data.</p>
      </div>
    );
  }

  const onlineNodes = sortedNodes.filter(n => n.metrics && n.status !== 'offline');
  const avgCpuTemp =
    onlineNodes.length > 0
      ? onlineNodes.reduce((s, n) => s + (n.metrics?.cpu_temp_celsius ?? 0), 0) / onlineNodes.length
      : 0;

  // Storage bar chart data
  const storageData = sortedNodes.map(n => ({
    name: `${formatNodeDisplayName(n)} (${formatMachineName(n)})`,
    used: n.metrics ? parseFloat((n.metrics.disk_used_bytes / (1024 ** 3)).toFixed(2)) : 0,
    free: n.metrics ? parseFloat((n.metrics.disk_free_bytes / (1024 ** 3)).toFixed(2)) : 0,
    color: getStatusColor(n.status),
  }));

  // Memory bar chart data
  const memData = sortedNodes.map(n => ({
    name: `${formatNodeDisplayName(n)} (${formatMachineName(n)})`,
    used: n.metrics ? parseFloat((n.metrics.mem_used_bytes / (1024 ** 3)).toFixed(2)) : 0,
    total: n.metrics ? parseFloat((n.metrics.mem_total_bytes / (1024 ** 3)).toFixed(2)) : 0,
    color: getStatusColor(n.status),
  }));

  const storagePct =
    cluster.total_storage_bytes > 0
      ? (cluster.used_storage_bytes / cluster.total_storage_bytes) * 100
      : 0;

  return (
    <div className="container-fluid py-4 px-4">
      {/* Stat Cards Row (Plan Section 4.2: 6 Cards) */}
      <div className="row g-3 mb-4">
        <div className="col-sm-6 col-md-4 col-xl-2">
          <StatCard
            title="Active Nodes"
            value={`${cluster.online_count} / ${cluster.node_count}`}
            subtitle="Online nodes"
            icon="bi-server"
            iconColor="#0d6efd"
            onClick={() => navigate('/nodes')}
          />
        </div>
        <div className="col-sm-6 col-md-4 col-xl-2">
          <StatCard
            title="Cluster Storage"
            value={`${formatBytes(cluster.used_storage_bytes)} / ${formatBytes(cluster.total_storage_bytes)}`}
            subtitle={`${storagePct.toFixed(1)}% used`}
            icon="bi-hdd-stack"
            iconColor="#6f42c1"
          >
            <div className="progress" style={{ height: 6 }}>
              <div
                className={`progress-bar ${getStorageBarClass(storagePct)}`}
                style={{
                  width: `${Math.min(storagePct, 100)}%`,
                  backgroundColor: getStorageBarColor(storagePct),
                }}
              />
            </div>
          </StatCard>
        </div>
        <div className="col-sm-6 col-md-4 col-xl-2">
          <StatCard
            title="Cluster Users"
            value={`${cluster.online_users ?? 0} / ${cluster.total_users}`}
            subtitle={`${cluster.online_users ?? 0} online / ${cluster.total_users} total`}
            icon="bi-people"
            iconColor="#198754"
            onClick={() => navigate('/users')}
          />
        </div>
        <div className="col-sm-6 col-md-4 col-xl-2">
          <StatCard
            title="Write Throughput"
            value="—"
            subtitle="Phase 4 instrumentation"
            icon="bi-lightning-charge"
            iconColor="#ffc107"
          />
        </div>
        <div className="col-sm-6 col-md-4 col-xl-2">
          <StatCard
            title="Read Throughput"
            value="—"
            subtitle="Phase 4 instrumentation"
            icon="bi-book"
            iconColor="#0dcaf0"
          />
        </div>
        <div className="col-sm-6 col-md-4 col-xl-2">
          <StatCard
            title="Error Rate"
            value="0.0%"
            subtitle="Phase 4 instrumentation"
            icon="bi-exclamation-octagon"
            iconColor="#dc3545"
          />
        </div>
      </div>

      {/* Node Health Mini-Map */}
      <div className="card shadow-sm border-0 mb-4">
        <div className="card-header bg-white border-bottom d-flex justify-content-between align-items-center">
          <h6 className="mb-0 fw-semibold">
            <i className="bi bi-grid-3x3-gap me-2 text-primary"></i>
            Node Health Map
          </h6>
          {onlineNodes.length > 0 && (
            <span
              className="badge bg-light border"
              style={{
                color: avgCpuTemp >= 70 ? '#dc3545' : avgCpuTemp >= 55 ? '#fd7e14' : '#198754',
              }}
            >
              <i className="bi bi-thermometer-half me-1"></i>
              Avg CPU Temp: {avgCpuTemp.toFixed(1)}°C
            </span>
          )}
        </div>
        <div className="card-body">
          <div className="d-flex flex-wrap gap-4 align-items-center">
            {sortedNodes.map(node => (
              <div key={node.fsID} className="text-center" style={{ cursor: 'pointer' }} onClick={() => navigate('/nodes')}>
                <div
                  className="rounded-circle d-flex align-items-center justify-content-center mx-auto mb-1"
                  style={{
                    width: 48,
                    height: 48,
                    backgroundColor: getStatusColor(node.status),
                    boxShadow: `0 0 0 4px ${getStatusColor(node.status)}33`,
                  }}
                  title={`${formatNodeDisplayName(node)} (${formatMachineName(node)}) — ${node.address} — ${node.status}`}
                >
                  <i className="bi bi-server text-white" style={{ fontSize: '1.1rem' }}></i>
                </div>
                <small className="fw-semibold d-block">{formatNodeDisplayName(node)}</small>
                <small className="text-muted d-block" style={{ fontSize: '0.65rem' }}>{formatMachineName(node)}</small>
                <span className={`badge ${getStatusBadgeClass(node.status)}`} style={{ fontSize: '0.65rem' }}>
                  {node.status}
                </span>
              </div>
            ))}
            {sortedNodes.length === 0 && (
              <p className="text-muted mb-0">No nodes registered.</p>
            )}
          </div>
        </div>
      </div>

      {/* Charts Row */}
      <div className="row g-4">
        {/* Storage Chart */}
        <div className="col-lg-6">
          <div className="card shadow-sm border-0 h-100">
            <div className="card-header bg-white border-bottom">
              <h6 className="mb-0 fw-semibold">
                <i className="bi bi-hdd me-2 text-purple" style={{ color: '#6f42c1' }}></i>
                Cluster Storage by Node (GB)
              </h6>
            </div>
            <div className="card-body">
              <ResponsiveContainer width="100%" height={260}>
                <BarChart data={storageData} margin={{ top: 5, right: 20, left: 0, bottom: 5 }}>
                  <XAxis dataKey="name" tick={{ fontSize: 12 }} />
                  <YAxis tick={{ fontSize: 12 }} unit=" GB" />
                  <Tooltip formatter={(val: number) => `${val} GB`} />
                  <Legend />
                  <Bar dataKey="used" name="Used" stackId="a" radius={[0, 0, 0, 0]}>
                    {storageData.map((entry, i) => (
                      <Cell key={i} fill={entry.color} />
                    ))}
                  </Bar>
                  <Bar dataKey="free" name="Free" stackId="a" fill="#dee2e6" radius={[4, 4, 0, 0]} />
                </BarChart>
              </ResponsiveContainer>
            </div>
          </div>
        </div>

        {/* Memory Chart */}
        <div className="col-lg-6">
          <div className="card shadow-sm border-0 h-100">
            <div className="card-header bg-white border-bottom">
              <h6 className="mb-0 fw-semibold">
                <i className="bi bi-memory me-2" style={{ color: '#0d6efd' }}></i>
                Cluster Memory Usage by Node (GB)
              </h6>
            </div>
            <div className="card-body">
              <ResponsiveContainer width="100%" height={260}>
                <BarChart data={memData} margin={{ top: 5, right: 20, left: 0, bottom: 5 }}>
                  <XAxis dataKey="name" tick={{ fontSize: 12 }} />
                  <YAxis tick={{ fontSize: 12 }} unit=" GB" />
                  <Tooltip formatter={(val: number) => `${val} GB`} />
                  <Legend />
                  <Bar dataKey="used" name="Used" radius={[0, 0, 0, 0]}>
                    {memData.map((entry, i) => (
                      <Cell key={i} fill={entry.color} />
                    ))}
                  </Bar>
                  <Bar dataKey="total" name="Total" fill="#dee2e6" radius={[4, 4, 0, 0]} />
                </BarChart>
              </ResponsiveContainer>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
