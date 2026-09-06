import { useState, useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import {
  BarChart, Bar, LineChart, Line, AreaChart, Area, XAxis, YAxis, Tooltip, Legend, ResponsiveContainer, CartesianGrid,
} from 'recharts';
import { fetchPerformance, fetchHistory, fetchClusterHistory, getPerformanceExportUrl } from '../api';
import StatCard from '../components/StatCard';
import { getStatusBadgeClass, getStatusColor } from '../utils';
import type { NodePerformance, Snapshot, ClusterHistorySnapshot } from '../types';

export default function Performance() {
  const [selectedNodeId, setSelectedNodeId] = useState<string>('all');

  const { data: perf, isLoading, isError } = useQuery({
    queryKey: ['performance'],
    queryFn: fetchPerformance,
    refetchInterval: 5000,
  });

  const { data: selectedHistory } = useQuery<Snapshot[]>({
    queryKey: ['history', selectedNodeId],
    queryFn: () => fetchHistory(selectedNodeId),
    refetchInterval: 5000,
    enabled: selectedNodeId !== 'all',
  });

  const { data: clusterHistory } = useQuery<ClusterHistorySnapshot[]>({
    queryKey: ['clusterHistory'],
    queryFn: fetchClusterHistory,
    refetchInterval: 5000,
    enabled: selectedNodeId === 'all',
  });

  const nodes = useMemo(() => perf?.nodes ?? [], [perf]);

  // Chart data for Node Throughput (MiB/s)
  const throughputData = useMemo(() => {
    return nodes.map(n => ({
      name: `${n.display_name} (${n.machine_name})`,
      write_mbps: parseFloat(n.write_mbps.toFixed(2)),
      read_mbps: parseFloat(n.read_mbps.toFixed(2)),
      status: n.status,
    }));
  }, [nodes]);

  // Chart data for Node IOPS
  const iopsData = useMemo(() => {
    return nodes.map(n => ({
      name: `${n.display_name} (${n.machine_name})`,
      write_iops: parseFloat(n.write_iops.toFixed(1)),
      read_iops: parseFloat(n.read_iops.toFixed(1)),
    }));
  }, [nodes]);

  // Chart data for Latency (p50, p95, p99) - Phase 4 Issue 3 Resolved
  const latencyData = useMemo(() => {
    return nodes.map(n => ({
      name: `${n.display_name} (${n.machine_name})`,
      write_p50: parseFloat(n.latency_write_p50.toFixed(2)),
      read_p50: parseFloat(n.latency_read_p50.toFixed(2)),
      write_p95: parseFloat(n.latency_write_p95.toFixed(2)),
      read_p95: parseFloat(n.latency_read_p95.toFixed(2)),
      write_p99: parseFloat(n.latency_write_p99.toFixed(2)),
      read_p99: parseFloat(n.latency_read_p99.toFixed(2)),
    }));
  }, [nodes]);

  // History timeline data when a specific node is selected
  const timelineData = useMemo(() => {
    if (!selectedHistory || selectedHistory.length === 0) return [];
    return selectedHistory.slice(-60).map(s => {
      const timeStr = new Date(s.timestamp * 1000).toLocaleTimeString([], {
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
      });
      return {
        time: timeStr,
        write_mbps: parseFloat((s.write_mbps ?? 0).toFixed(2)),
        read_mbps: parseFloat((s.read_mbps ?? 0).toFixed(2)),
        write_iops: parseFloat((s.write_iops ?? 0).toFixed(1)),
        read_iops: parseFloat((s.read_iops ?? 0).toFixed(1)),
        active_connections: s.metrics?.active_connections ?? 0,
        error_rate: parseFloat((s.error_rate_pct ?? 0).toFixed(2)),
      };
    });
  }, [selectedHistory]);

  // Cluster timeline data when All Nodes is selected (Phase 4 Issue 1, 2, 4)
  const clusterTimelineData = useMemo(() => {
    if (!clusterHistory || clusterHistory.length === 0) return [];
    return clusterHistory.slice(-60).map(s => {
      const timeStr = new Date(s.timestamp * 1000).toLocaleTimeString([], {
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
      });
      return {
        time: timeStr,
        write_mbps: parseFloat(s.write_mbps.toFixed(2)),
        read_mbps: parseFloat(s.read_mbps.toFixed(2)),
        write_iops: parseFloat(s.write_iops.toFixed(1)),
        read_iops: parseFloat(s.read_iops.toFixed(1)),
        active_connections: s.active_connections ?? 0,
        error_rate: parseFloat((s.error_rate_pct ?? 0).toFixed(2)),
      };
    });
  }, [clusterHistory]);

  const activeTimeline = selectedNodeId === 'all' ? clusterTimelineData : timelineData;

  if (isLoading) {
    return (
      <div className="container-fluid py-5 text-center">
        <div className="spinner-border text-primary" role="status">
          <span className="visually-hidden">Loading...</span>
        </div>
        <p className="mt-3 text-muted">Collecting cluster performance telemetry...</p>
      </div>
    );
  }

  if (isError || !perf) {
    return (
      <div className="container-fluid py-5 text-center">
        <i className="bi bi-exclamation-triangle text-danger" style={{ fontSize: '3rem' }}></i>
        <p className="mt-3 text-danger">Failed to fetch cluster performance telemetry.</p>
      </div>
    );
  }

  const exportUrl = selectedNodeId === 'all'
    ? getPerformanceExportUrl()
    : getPerformanceExportUrl(selectedNodeId);

  return (
    <div className="container-fluid py-4 px-4">
      {/* Top Banner Header */}
      <div className="d-flex flex-wrap justify-content-between align-items-center mb-4 gap-3">
        <div>
          <h4 className="mb-1 fw-bold">
            <i className="bi bi-speedometer2 text-primary me-2"></i>
            Cluster Performance &amp; Telemetry
          </h4>
          <p className="text-muted small mb-0">
            Real-time throughput, IOPS, and latency percentiles computed across fileserver nodes.
          </p>
        </div>
        <div className="d-flex align-items-center gap-2">
          <div className="input-group input-group-sm" style={{ width: 220 }}>
            <span className="input-group-text bg-white border-end-0">
              <i className="bi bi-funnel text-secondary"></i>
            </span>
            <select
              className="form-select border-start-0"
              value={selectedNodeId}
              onChange={e => setSelectedNodeId(e.target.value)}
            >
              <option value="all">All Nodes (Aggregate)</option>
              {nodes.map(n => (
                <option key={n.fsID} value={n.fsID}>
                  {n.display_name} ({n.machine_name})
                </option>
              ))}
            </select>
          </div>
          <a
            href={exportUrl}
            download
            className="btn btn-sm btn-outline-success d-flex align-items-center gap-1 shadow-sm"
          >
            <i className="bi bi-file-earmark-spreadsheet"></i>
            Export CSV
          </a>
        </div>
      </div>

      {/* Cluster KPI Stat Cards */}
      <div className="row g-3 mb-4">
        <div className="col-sm-6 col-md-4 col-xl">
          <StatCard
            title="Write Throughput"
            value={`${perf.cluster_write_mbps.toFixed(2)} MiB/s`}
            subtitle="Cluster total writes"
            icon="bi-lightning-charge"
            iconColor="#ffc107"
          />
        </div>
        <div className="col-sm-6 col-md-4 col-xl">
          <StatCard
            title="Read Throughput"
            value={`${perf.cluster_read_mbps.toFixed(2)} MiB/s`}
            subtitle="Cluster total reads"
            icon="bi-book"
            iconColor="#0dcaf0"
          />
        </div>
        <div className="col-sm-6 col-md-4 col-xl">
          <StatCard
            title="Write IOPS"
            value={`${perf.cluster_write_iops.toFixed(1)} ops/s`}
            subtitle="Write frequency"
            icon="bi-arrow-up-right-circle"
            iconColor="#6610f2"
          />
        </div>
        <div className="col-sm-6 col-md-4 col-xl">
          <StatCard
            title="Read IOPS"
            value={`${perf.cluster_read_iops.toFixed(1)} ops/s`}
            subtitle="Read frequency"
            icon="bi-arrow-down-left-circle"
            iconColor="#20c997"
          />
        </div>
        <div className="col-sm-6 col-md-4 col-xl">
          <StatCard
            title="Error Rate"
            value={`${perf.cluster_error_rate_pct.toFixed(2)}%`}
            subtitle={perf.cluster_error_rate_pct > 0 ? "Non-zero errors observed" : "All nominal"}
            icon="bi-exclamation-octagon"
            iconColor="#dc3545"
          />
        </div>
      </div>

      {/* Telemetry Timelines (Cluster when 'all', Node when specific node selected - Phase 4 Issues 1, 2, 4) */}
      <div className="card shadow-sm border-0 mb-4">
        <div className="card-header bg-white border-bottom d-flex justify-content-between align-items-center">
          <h6 className="mb-0 fw-semibold">
            <i className="bi bi-clock-history text-primary me-2"></i>
            {selectedNodeId === 'all'
              ? 'Cluster Telemetry Timelines (Last 60 Points)'
              : `Node Telemetry Timeline — ${nodes.find(n => n.fsID === selectedNodeId)?.display_name} (Last 60 Points)`}
          </h6>
          {selectedNodeId !== 'all' && (
            <button
              className="btn btn-sm btn-link text-decoration-none text-muted p-0"
              onClick={() => setSelectedNodeId('all')}
            >
              Reset to Cluster View
            </button>
          )}
        </div>
        <div className="card-body">
          {activeTimeline.length === 0 ? (
            <p className="text-muted text-center py-4">Waiting for telemetry history...</p>
          ) : (
            <div className="row g-4">
              {/* Throughput Timeline (Phase 4 Issue 1) */}
              <div className="col-lg-4">
                <h6 className="text-secondary small fw-semibold text-uppercase mb-2">Throughput History (MiB/s)</h6>
                <ResponsiveContainer width="100%" height={220}>
                  <LineChart data={activeTimeline}>
                    <CartesianGrid strokeDasharray="3 3" stroke="#f0f0f0" />
                    <XAxis dataKey="time" tick={{ fontSize: 10 }} />
                    <YAxis tick={{ fontSize: 10 }} unit=" MiB/s" />
                    <Tooltip />
                    <Legend />
                    <Line type="monotone" dataKey="write_mbps" name="Write MiB/s" stroke="#ffc107" strokeWidth={2} dot={false} />
                    <Line type="monotone" dataKey="read_mbps" name="Read MiB/s" stroke="#0dcaf0" strokeWidth={2} dot={false} />
                  </LineChart>
                </ResponsiveContainer>
              </div>
              {/* IOPS Timeline (Phase 4 Issue 4) */}
              <div className="col-lg-4">
                <h6 className="text-secondary small fw-semibold text-uppercase mb-2">IOPS History (ops/s)</h6>
                <ResponsiveContainer width="100%" height={220}>
                  <LineChart data={activeTimeline}>
                    <CartesianGrid strokeDasharray="3 3" stroke="#f0f0f0" />
                    <XAxis dataKey="time" tick={{ fontSize: 10 }} />
                    <YAxis tick={{ fontSize: 10 }} unit=" ops" />
                    <Tooltip />
                    <Legend />
                    <Line type="monotone" dataKey="write_iops" name="Write IOPS" stroke="#6610f2" strokeWidth={2} dot={false} />
                    <Line type="monotone" dataKey="read_iops" name="Read IOPS" stroke="#20c997" strokeWidth={2} dot={false} />
                  </LineChart>
                </ResponsiveContainer>
              </div>
              {/* Active Connections Timeline AreaChart (Phase 4 Issue 2) */}
              <div className="col-lg-4">
                <h6 className="text-secondary small fw-semibold text-uppercase mb-2">Active Connections</h6>
                <ResponsiveContainer width="100%" height={220}>
                  <AreaChart data={activeTimeline}>
                    <CartesianGrid strokeDasharray="3 3" stroke="#f0f0f0" />
                    <XAxis dataKey="time" tick={{ fontSize: 10 }} />
                    <YAxis tick={{ fontSize: 10 }} allowDecimals={false} />
                    <Tooltip />
                    <Legend />
                    <Area type="monotone" dataKey="active_connections" name="Connections" stroke="#0d6efd" fill="#cfe2ff" strokeWidth={2} />
                  </AreaChart>
                </ResponsiveContainer>
              </div>
            </div>
          )}
        </div>
      </div>

      {/* Cluster Overview Charts (Grouped Bar Comparisons) */}
      <div className="row g-4 mb-4">
        {/* Throughput by Node */}
        <div className="col-lg-6">
          <div className="card shadow-sm border-0 h-100">
            <div className="card-header bg-white border-bottom">
              <h6 className="mb-0 fw-semibold">
                <i className="bi bi-bar-chart-line text-warning me-2"></i>
                Node Throughput (MiB/s)
              </h6>
            </div>
            <div className="card-body">
              <ResponsiveContainer width="100%" height={260}>
                <BarChart data={throughputData}>
                  <CartesianGrid strokeDasharray="3 3" stroke="#f0f0f0" />
                  <XAxis dataKey="name" tick={{ fontSize: 11 }} />
                  <YAxis tick={{ fontSize: 11 }} unit=" MiB/s" />
                  <Tooltip />
                  <Legend />
                  <Bar dataKey="write_mbps" name="Write Throughput (MiB/s)" fill="#ffc107" radius={[4, 4, 0, 0]} />
                  <Bar dataKey="read_mbps" name="Read Throughput (MiB/s)" fill="#0dcaf0" radius={[4, 4, 0, 0]} />
                </BarChart>
              </ResponsiveContainer>
            </div>
          </div>
        </div>

        {/* IOPS by Node */}
        <div className="col-lg-6">
          <div className="card shadow-sm border-0 h-100">
            <div className="card-header bg-white border-bottom">
              <h6 className="mb-0 fw-semibold">
                <i className="bi bi-activity text-success me-2"></i>
                Node IOPS (ops/s)
              </h6>
            </div>
            <div className="card-body">
              <ResponsiveContainer width="100%" height={260}>
                <BarChart data={iopsData}>
                  <CartesianGrid strokeDasharray="3 3" stroke="#f0f0f0" />
                  <XAxis dataKey="name" tick={{ fontSize: 11 }} />
                  <YAxis tick={{ fontSize: 11 }} unit=" ops" />
                  <Tooltip />
                  <Legend />
                  <Bar dataKey="write_iops" name="Write IOPS" fill="#6610f2" radius={[4, 4, 0, 0]} />
                  <Bar dataKey="read_iops" name="Read IOPS" fill="#20c997" radius={[4, 4, 0, 0]} />
                </BarChart>
              </ResponsiveContainer>
            </div>
          </div>
        </div>
      </div>

      {/* Latency Percentiles Comparison (Phase 4 Issue 3 Resolved with p50) */}
      <div className="card shadow-sm border-0 mb-4">
        <div className="card-header bg-white border-bottom d-flex justify-content-between align-items-center">
          <h6 className="mb-0 fw-semibold">
            <i className="bi bi-stopwatch text-danger me-2"></i>
            Latency Percentiles (p50, p95 &amp; p99 in ms)
          </h6>
          <span className="text-muted small">Lower is better</span>
        </div>
        <div className="card-body">
          <ResponsiveContainer width="100%" height={260}>
            <BarChart data={latencyData}>
              <CartesianGrid strokeDasharray="3 3" stroke="#f0f0f0" />
              <XAxis dataKey="name" tick={{ fontSize: 11 }} />
              <YAxis tick={{ fontSize: 11 }} unit=" ms" />
              <Tooltip />
              <Legend />
              <Bar dataKey="write_p50" name="Write p50 (ms)" fill="#ffc107" radius={[4, 4, 0, 0]} />
              <Bar dataKey="read_p50" name="Read p50 (ms)" fill="#20c997" radius={[4, 4, 0, 0]} />
              <Bar dataKey="write_p95" name="Write p95 (ms)" fill="#fd7e14" radius={[4, 4, 0, 0]} />
              <Bar dataKey="read_p95" name="Read p95 (ms)" fill="#0dcaf0" radius={[4, 4, 0, 0]} />
              <Bar dataKey="write_p99" name="Write p99 (ms)" fill="#dc3545" radius={[4, 4, 0, 0]} />
              <Bar dataKey="read_p99" name="Read p99 (ms)" fill="#0d6efd" radius={[4, 4, 0, 0]} />
            </BarChart>
          </ResponsiveContainer>
        </div>
      </div>

      {/* Performance Matrix Table */}
      <div className="card shadow-sm border-0">
        <div className="card-header bg-white border-bottom d-flex justify-content-between align-items-center">
          <h6 className="mb-0 fw-semibold">
            <i className="bi bi-table text-primary me-2"></i>
            Node Performance &amp; Latency Matrix
          </h6>
          <span className="badge bg-light text-dark border">
            {nodes.length} Nodes Monitored
          </span>
        </div>
        <div className="table-responsive">
          <table className="table table-hover align-middle mb-0" style={{ fontSize: '0.85rem' }}>
            <thead className="table-light">
              <tr>
                <th>Node</th>
                <th>Status</th>
                <th>Write Throughput</th>
                <th>Read Throughput</th>
                <th>Write IOPS</th>
                <th>Read IOPS</th>
                <th>Error Rate</th>
                <th>Write Latency (p50 / p95 / p99)</th>
                <th>Read Latency (p50 / p95 / p99)</th>
                <th>Conns</th>
                <th className="text-end">Export</th>
              </tr>
            </thead>
            <tbody>
              {nodes.map(n => (
                <tr
                  key={n.fsID}
                  className={selectedNodeId === n.fsID ? 'table-primary' : ''}
                  style={{ cursor: 'pointer' }}
                  onClick={() => setSelectedNodeId(n.fsID === selectedNodeId ? 'all' : n.fsID)}
                >
                  <td>
                    <div className="fw-semibold">{n.display_name}</div>
                    <div className="text-muted small">{n.machine_name} &bull; {n.address}</div>
                  </td>
                  <td>
                    <span className={`badge ${getStatusBadgeClass(n.status)}`}>
                      {n.status.toUpperCase()}
                    </span>
                  </td>
                  <td>
                    <span className="fw-semibold text-warning">
                      {n.write_mbps.toFixed(2)} MiB/s
                    </span>
                  </td>
                  <td>
                    <span className="fw-semibold text-info">
                      {n.read_mbps.toFixed(2)} MiB/s
                    </span>
                  </td>
                  <td>{n.write_iops.toFixed(1)} ops/s</td>
                  <td>{n.read_iops.toFixed(1)} ops/s</td>
                  <td>
                    <span className={n.error_rate_pct > 0 ? 'text-danger fw-semibold' : 'text-muted'}>
                      {n.error_rate_pct.toFixed(2)}%
                    </span>
                  </td>
                  <td>
                    <code>{n.latency_write_p50.toFixed(1)}ms</code> /{' '}
                    <code>{n.latency_write_p95.toFixed(1)}ms</code> /{' '}
                    <code>{n.latency_write_p99.toFixed(1)}ms</code>
                  </td>
                  <td>
                    <code>{n.latency_read_p50.toFixed(1)}ms</code> /{' '}
                    <code>{n.latency_read_p95.toFixed(1)}ms</code> /{' '}
                    <code>{n.latency_read_p99.toFixed(1)}ms</code>
                  </td>
                  <td>
                    <span className="badge bg-secondary-subtle text-dark border">
                      {n.active_connections}
                    </span>
                  </td>
                  <td className="text-end" onClick={e => e.stopPropagation()}>
                    <a
                      href={getPerformanceExportUrl(n.fsID)}
                      download
                      className="btn btn-sm btn-outline-secondary py-0 px-2"
                      title="Export CSV for this node"
                    >
                      <i className="bi bi-download"></i>
                    </a>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}
