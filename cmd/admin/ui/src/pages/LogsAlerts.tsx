import React, { useState, useMemo, useEffect, useRef } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
  fetchAlerts,
  fetchAlertSummary,
  resolveAlert,
  resolveAllAlerts,
  fetchCommandHistory,
  fetchLogTail,
  fetchCluster,
} from '../api';
import type { Alert, AlertSeverity, CommandRecord } from '../types';

export default function LogsAlerts() {
  const queryClient = useQueryClient();
  const [activeTab, setActiveTab] = useState<'alerts' | 'history' | 'tail'>('alerts');

  // === Tab 1: Alerts State ===
  const [severityFilter, setSeverityFilter] = useState<string>('all');
  const [unresolvedOnly, setUnresolvedOnly] = useState<boolean>(true);
  const [searchQuery, setSearchQuery] = useState<string>('');

  const { data: alerts = [], isLoading: alertsLoading } = useQuery<Alert[]>({
    queryKey: ['alerts', severityFilter, unresolvedOnly],
    queryFn: () =>
      fetchAlerts({
        severity: severityFilter === 'all' ? undefined : severityFilter,
        unresolved: unresolvedOnly,
      }),
    refetchInterval: 5000,
  });

  const { data: summary } = useQuery({
    queryKey: ['alertSummary'],
    queryFn: fetchAlertSummary,
    refetchInterval: 5000,
  });

  const resolveMutation = useMutation({
    mutationFn: resolveAlert,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['alerts'] });
      queryClient.invalidateQueries({ queryKey: ['alertSummary'] });
    },
  });

  const resolveAllMutation = useMutation({
    mutationFn: resolveAllAlerts,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['alerts'] });
      queryClient.invalidateQueries({ queryKey: ['alertSummary'] });
    },
  });

  // Filtered alerts
  const filteredAlerts = useMemo(() => {
    return alerts.filter(a => {
      if (!searchQuery) return true;
      const q = searchQuery.toLowerCase();
      return (
        a.title.toLowerCase().includes(q) ||
        a.message.toLowerCase().includes(q) ||
        (a.node_name && a.node_name.toLowerCase().includes(q)) ||
        (a.node_id && a.node_id.toLowerCase().includes(q)) ||
        (a.username && a.username.toLowerCase().includes(q))
      );
    });
  }, [alerts, searchQuery]);

  // === Tab 2: Command History State ===
  const [historySearch, setHistorySearch] = useState<string>('');
  const [expandedRecordId, setExpandedRecordId] = useState<string | null>(null);

  const { data: commandHistory = [], isLoading: historyLoading } = useQuery<CommandRecord[]>({
    queryKey: ['commandHistory'],
    queryFn: fetchCommandHistory,
    refetchInterval: 5000,
    enabled: activeTab === 'history',
  });

  const filteredHistory = useMemo(() => {
    return commandHistory.filter(r => {
      if (!historySearch) return true;
      const q = historySearch.toLowerCase();
      return (
        r.command.toLowerCase().includes(q) ||
        r.action_type.toLowerCase().includes(q) ||
        r.status.toLowerCase().includes(q) ||
        r.target_nodes.some(n => n.toLowerCase().includes(q))
      );
    });
  }, [commandHistory, historySearch]);

  // === Tab 3: Live Log Tail State ===
  const { data: cluster } = useQuery({
    queryKey: ['cluster'],
    queryFn: fetchCluster,
  });

  const nodes = useMemo(() => cluster?.nodes ?? [], [cluster]);
  const [selectedNodeId, setSelectedNodeId] = useState<string>('0');
  const [selectedService, setSelectedService] = useState<string>('fileserver');
  const [logLines, setLogLines] = useState<number>(100);
  const [isStreaming, setIsStreaming] = useState<boolean>(true);
  const [autoScroll, setAutoScroll] = useState<boolean>(true);
  const logTerminalRef = useRef<HTMLPreElement | null>(null);

  const { data: logTailData, refetch: refetchLogs, isFetching: logsFetching } = useQuery({
    queryKey: ['logTail', selectedNodeId, selectedService, logLines],
    queryFn: () => fetchLogTail(selectedNodeId, logLines, selectedService),
    refetchInterval: isStreaming ? 4000 : false,
    enabled: activeTab === 'tail' && !!selectedNodeId,
  });

  useEffect(() => {
    if (autoScroll && logTerminalRef.current) {
      logTerminalRef.current.scrollTop = logTerminalRef.current.scrollHeight;
    }
  }, [logTailData, autoScroll]);

  // Helper formatting functions
  const formatTime = (ts: number) => {
    return new Date(ts * 1000).toLocaleString();
  };

  const timeAgo = (ts: number) => {
    const diff = Math.floor(Date.now() / 1000 - ts);
    if (diff < 60) return `${diff}s ago`;
    if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
    if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
    return `${Math.floor(diff / 86400)}d ago`;
  };

  const getSeverityBadgeClass = (sev: AlertSeverity) => {
    switch (sev) {
      case 'critical':
        return 'bg-danger';
      case 'warning':
        return 'bg-warning text-dark';
      case 'info':
        return 'bg-info text-dark';
      default:
        return 'bg-secondary';
    }
  };

  return (
    <div className="container-fluid py-4 px-4">
      {/* Page Title Banner */}
      <div className="d-flex flex-wrap justify-content-between align-items-center mb-4 gap-3">
        <div>
          <h4 className="mb-1 fw-bold">
            <i className="bi bi-bell text-danger me-2"></i>
            Logs &amp; Alerts Console
          </h4>
          <p className="text-muted small mb-0">
            Real-time threshold alert feed, orchestration command audit history, and live remote log streaming.
          </p>
        </div>

        {/* Summary Badges */}
        <div className="d-flex gap-2">
          <span className="badge bg-danger p-2 px-3 d-flex align-items-center gap-1 shadow-sm">
            <i className="bi bi-exclamation-octagon-fill"></i>
            {summary?.critical_count ?? 0} Critical
          </span>
          <span className="badge bg-warning text-dark p-2 px-3 d-flex align-items-center gap-1 shadow-sm">
            <i className="bi bi-exclamation-triangle-fill"></i>
            {summary?.warning_count ?? 0} Warning
          </span>
          <span className="badge bg-info text-dark p-2 px-3 d-flex align-items-center gap-1 shadow-sm">
            <i className="bi bi-info-circle-fill"></i>
            {summary?.info_count ?? 0} Info
          </span>
        </div>
      </div>

      {/* Tabs Navigation */}
      <ul className="nav nav-pills mb-4 gap-2 border-bottom pb-3">
        <li className="nav-item">
          <button
            className={`nav-link d-flex align-items-center gap-2 ${activeTab === 'alerts' ? 'active' : ''}`}
            onClick={() => setActiveTab('alerts')}
          >
            <i className="bi bi-bell-fill"></i>
            Alert Feed
            {(summary?.total_unresolved ?? 0) > 0 && (
              <span className="badge bg-danger rounded-pill">{summary?.total_unresolved}</span>
            )}
          </button>
        </li>
        <li className="nav-item">
          <button
            className={`nav-link d-flex align-items-center gap-2 ${activeTab === 'history' ? 'active' : ''}`}
            onClick={() => setActiveTab('history')}
          >
            <i className="bi bi-clock-history"></i>
            Command History
          </button>
        </li>
        <li className="nav-item">
          <button
            className={`nav-link d-flex align-items-center gap-2 ${activeTab === 'tail' ? 'active' : ''}`}
            onClick={() => setActiveTab('tail')}
          >
            <i className="bi bi-terminal-fill"></i>
            Live Log Tail
          </button>
        </li>
      </ul>

      {/* TAB 1: ALERT FEED */}
      {activeTab === 'alerts' && (
        <div>
          {/* Controls Bar */}
          <div className="card shadow-sm border-0 mb-4">
            <div className="card-body py-3">
              <div className="row g-3 align-items-center justify-content-between">
                <div className="col-md-6 d-flex flex-wrap gap-2 align-items-center">
                  <div className="btn-group btn-group-sm" role="group">
                    <button
                      type="button"
                      className={`btn ${severityFilter === 'all' ? 'btn-primary' : 'btn-outline-secondary'}`}
                      onClick={() => setSeverityFilter('all')}
                    >
                      All Severities
                    </button>
                    <button
                      type="button"
                      className={`btn ${severityFilter === 'critical' ? 'btn-danger' : 'btn-outline-danger'}`}
                      onClick={() => setSeverityFilter('critical')}
                    >
                      Critical
                    </button>
                    <button
                      type="button"
                      className={`btn ${severityFilter === 'warning' ? 'btn-warning text-dark' : 'btn-outline-warning text-dark'}`}
                      onClick={() => setSeverityFilter('warning')}
                    >
                      Warning
                    </button>
                    <button
                      type="button"
                      className={`btn ${severityFilter === 'info' ? 'btn-info text-dark' : 'btn-outline-info text-dark'}`}
                      onClick={() => setSeverityFilter('info')}
                    >
                      Info
                    </button>
                  </div>

                  <div className="form-check form-switch ms-2 mb-0">
                    <input
                      className="form-check-input"
                      type="checkbox"
                      id="unresolvedSwitch"
                      checked={unresolvedOnly}
                      onChange={e => setUnresolvedOnly(e.target.checked)}
                    />
                    <label className="form-check-label small fw-semibold" htmlFor="unresolvedSwitch">
                      Unresolved Only
                    </label>
                  </div>
                </div>

                <div className="col-md-6 d-flex justify-content-md-end gap-2">
                  <div className="input-group input-group-sm" style={{ maxWidth: 280 }}>
                    <span className="input-group-text bg-white">
                      <i className="bi bi-search text-muted"></i>
                    </span>
                    <input
                      type="text"
                      className="form-control"
                      placeholder="Search alerts..."
                      value={searchQuery}
                      onChange={e => setSearchQuery(e.target.value)}
                    />
                    {searchQuery && (
                      <button className="btn btn-outline-secondary" onClick={() => setSearchQuery('')}>
                        &times;
                      </button>
                    )}
                  </div>

                  <button
                    className="btn btn-sm btn-outline-success d-flex align-items-center gap-1"
                    onClick={() => resolveAllMutation.mutate()}
                    disabled={resolveAllMutation.isPending || (summary?.total_unresolved ?? 0) === 0}
                  >
                    <i className="bi bi-check2-all"></i>
                    Resolve All
                  </button>
                </div>
              </div>
            </div>
          </div>

          {/* Alerts List */}
          {alertsLoading ? (
            <div className="text-center py-5">
              <div className="spinner-border text-primary" role="status"></div>
              <p className="mt-2 text-muted">Loading system alerts...</p>
            </div>
          ) : filteredAlerts.length === 0 ? (
            <div className="card shadow-sm border-0 text-center py-5">
              <div className="card-body">
                <i className="bi bi-shield-check text-success" style={{ fontSize: '3.5rem' }}></i>
                <h5 className="mt-3 fw-bold">No Alerts to Display</h5>
                <p className="text-muted small">
                  {unresolvedOnly
                    ? 'All system conditions are nominal. No active unresolved alerts.'
                    : 'No alerts match your current search filters.'}
                </p>
              </div>
            </div>
          ) : (
            <div className="d-flex flex-column gap-3">
              {filteredAlerts.map(alert => (
                <div
                  key={alert.id}
                  className={`card shadow-sm border-0 ${alert.resolved ? 'opacity-75 bg-light' : 'bg-white'}`}
                  style={{
                    borderLeft: `5px solid ${
                      alert.severity === 'critical' ? '#dc3545' : alert.severity === 'warning' ? '#ffc107' : '#0dcaf0'
                    }`,
                  }}
                >
                  <div className="card-body d-flex flex-wrap justify-content-between align-items-center gap-3">
                    <div className="d-flex align-items-start gap-3">
                      <span className={`badge ${getSeverityBadgeClass(alert.severity)} text-uppercase px-2 py-1 mt-1`}>
                        {alert.severity}
                      </span>
                      <div>
                        <div className="d-flex align-items-center gap-2 flex-wrap mb-1">
                          <h6 className="mb-0 fw-bold">{alert.title}</h6>
                          {alert.node_name && (
                            <span className="badge bg-light text-dark border">
                              <i className="bi bi-hdd-network me-1"></i>
                              {alert.node_name}
                            </span>
                          )}
                          {alert.username && (
                            <span className="badge bg-light text-dark border">
                              <i className="bi bi-person me-1"></i>
                              {alert.username}
                            </span>
                          )}
                          {alert.resolved && (
                            <span className="badge bg-success-subtle text-success border border-success-subtle">
                              <i className="bi bi-check-circle me-1"></i>
                              Resolved
                            </span>
                          )}
                        </div>
                        <p className="mb-1 text-secondary small">{alert.message}</p>
                        <div className="text-muted small">
                          <i className="bi bi-clock me-1"></i>
                          {formatTime(alert.timestamp)} ({timeAgo(alert.timestamp)})
                          {alert.resolved && alert.resolved_at && (
                            <span className="ms-2">
                              &bull; Resolved at {formatTime(alert.resolved_at)}
                            </span>
                          )}
                        </div>
                      </div>
                    </div>

                    {!alert.resolved && (
                      <button
                        className="btn btn-sm btn-outline-success d-flex align-items-center gap-1"
                        onClick={() => resolveMutation.mutate(alert.id)}
                        disabled={resolveMutation.isPending}
                      >
                        <i className="bi bi-check-lg"></i>
                        Acknowledge
                      </button>
                    )}
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      )}

      {/* TAB 2: COMMAND HISTORY */}
      {activeTab === 'history' && (
        <div className="card shadow-sm border-0">
          <div className="card-header bg-white border-bottom py-3 d-flex flex-wrap justify-content-between align-items-center gap-3">
            <h6 className="mb-0 fw-semibold">
              <i className="bi bi-journal-code text-primary me-2"></i>
              Cluster Orchestration Audit Log
            </h6>
            <div className="input-group input-group-sm" style={{ maxWidth: 280 }}>
              <span className="input-group-text bg-white">
                <i className="bi bi-search text-muted"></i>
              </span>
              <input
                type="text"
                className="form-control"
                placeholder="Search audit trail..."
                value={historySearch}
                onChange={e => setHistorySearch(e.target.value)}
              />
              {historySearch && (
                <button className="btn btn-outline-secondary" onClick={() => setHistorySearch('')}>
                  &times;
                </button>
              )}
            </div>
          </div>

          <div className="table-responsive">
            <table className="table table-hover align-middle mb-0" style={{ fontSize: '0.85rem' }}>
              <thead className="table-light">
                <tr>
                  <th scope="col">Timestamp</th>
                  <th scope="col">Action</th>
                  <th scope="col">Command Executed</th>
                  <th scope="col">Targets</th>
                  <th scope="col">Status</th>
                  <th scope="col">Duration</th>
                  <th scope="col" className="text-end">Details</th>
                </tr>
              </thead>
              <tbody>
                {historyLoading ? (
                  <tr>
                    <td colSpan={7} className="text-center py-4 text-muted">
                      <div className="spinner-border spinner-border-sm text-primary me-2"></div>
                      Loading command audit logs...
                    </td>
                  </tr>
                ) : filteredHistory.length === 0 ? (
                  <tr>
                    <td colSpan={7} className="text-center py-5 text-muted">
                      <i className="bi bi-inbox text-secondary" style={{ fontSize: '2rem' }}></i>
                      <p className="mt-2 mb-0">No command records found.</p>
                    </td>
                  </tr>
                ) : (
                  filteredHistory.map(r => {
                    const isExpanded = expandedRecordId === r.id;
                    const statusBadge =
                      r.status === 'success' ? 'bg-success' : r.status === 'failed' ? 'bg-danger' : 'bg-warning text-dark';

                    return (
                      <React.Fragment key={r.id}>
                        <tr>
                          <td>
                            <span className="fw-semibold">{formatTime(r.timestamp)}</span>
                            <div className="text-muted small">{timeAgo(r.timestamp)}</div>
                          </td>
                          <td>
                            <span className="badge bg-light text-dark border text-uppercase">
                              {r.action_type}
                            </span>
                          </td>
                          <td style={{ maxWidth: 320 }}>
                            <code className="text-dark text-break">{r.command || '(preset command)'}</code>
                          </td>
                          <td>
                            <span className="badge bg-secondary-subtle text-secondary border">
                              {r.target_nodes.join(', ')}
                            </span>
                          </td>
                          <td>
                            <span className={`badge ${statusBadge} text-uppercase`}>{r.status}</span>
                          </td>
                          <td>{r.duration_ms} ms</td>
                          <td className="text-end">
                            <button
                              className="btn btn-sm btn-outline-secondary"
                              onClick={() => setExpandedRecordId(isExpanded ? null : r.id)}
                            >
                              {isExpanded ? 'Hide' : 'View Outputs'}
                            </button>
                          </td>
                        </tr>
                        {isExpanded && (
                          <tr>
                            <td colSpan={7} className="bg-light p-3">
                              <h6 className="fw-bold mb-2 small text-uppercase">Node Results &amp; Outputs:</h6>
                              <div className="d-flex flex-column gap-2">
                                {Object.entries(r.node_results || {}).map(([nodeId, res]) => (
                                  <div key={nodeId} className="card shadow-sm border">
                                    <div className="card-header bg-white py-2 d-flex justify-content-between align-items-center">
                                      <span className="fw-semibold small">
                                        Node {nodeId} ({res.address}) &mdash; Exit Code {res.exit_code}
                                      </span>
                                      <span className="text-muted small">{res.duration_ms} ms</span>
                                    </div>
                                    <div className="card-body p-2 bg-dark">
                                      <pre className="text-light mb-0 small" style={{ maxHeight: 150, overflowY: 'auto' }}>
                                        {res.output || res.error || '(no output)'}
                                      </pre>
                                    </div>
                                  </div>
                                ))}
                              </div>
                            </td>
                          </tr>
                        )}
                      </React.Fragment>
                    );
                  })
                )}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* TAB 3: LIVE LOG TAIL */}
      {activeTab === 'tail' && (
        <div className="card shadow-sm border-0">
          <div className="card-header bg-white border-bottom py-3">
            <div className="row g-3 align-items-center justify-content-between">
              <div className="col-md-7 d-flex flex-wrap gap-2 align-items-center">
                {/* Node Selector */}
                <div className="input-group input-group-sm" style={{ width: 220 }}>
                  <span className="input-group-text bg-white">
                    <i className="bi bi-hdd-network"></i>
                  </span>
                  <select
                    className="form-select"
                    value={selectedNodeId}
                    onChange={e => setSelectedNodeId(e.target.value)}
                  >
                    {nodes.map(n => (
                      <option key={n.fsID} value={n.fsID}>
                        {n.displayName || `FS-${n.fsID}`} ({n.machineName || n.address})
                      </option>
                    ))}
                  </select>
                </div>

                {/* Service Selector */}
                <div className="input-group input-group-sm" style={{ width: 170 }}>
                  <span className="input-group-text bg-white">Service</span>
                  <select
                    className="form-select"
                    value={selectedService}
                    onChange={e => setSelectedService(e.target.value)}
                  >
                    <option value="fileserver">fileserver</option>
                    <option value="metaserver">metaserver</option>
                    <option value="admin">admin</option>
                  </select>
                </div>

                {/* Lines Selector */}
                <div className="input-group input-group-sm" style={{ width: 140 }}>
                  <span className="input-group-text bg-white">Lines</span>
                  <select
                    className="form-select"
                    value={logLines}
                    onChange={e => setLogLines(parseInt(e.target.value, 10))}
                  >
                    <option value={50}>50</option>
                    <option value={100}>100</option>
                    <option value={200}>200</option>
                    <option value={500}>500</option>
                  </select>
                </div>
              </div>

              {/* Controls */}
              <div className="col-md-5 d-flex justify-content-md-end gap-2 align-items-center">
                <div className="form-check form-switch mb-0 me-2">
                  <input
                    className="form-check-input"
                    type="checkbox"
                    id="autoScrollCheck"
                    checked={autoScroll}
                    onChange={e => setAutoScroll(e.target.checked)}
                  />
                  <label className="form-check-label small fw-semibold" htmlFor="autoScrollCheck">
                    Auto-scroll
                  </label>
                </div>

                <button
                  className={`btn btn-sm ${isStreaming ? 'btn-outline-danger' : 'btn-outline-success'} d-flex align-items-center gap-1`}
                  onClick={() => setIsStreaming(!isStreaming)}
                >
                  <i className={`bi ${isStreaming ? 'bi-pause-fill' : 'bi-play-fill'}`}></i>
                  {isStreaming ? 'Pause Stream' : 'Resume Stream'}
                </button>

                <button
                  className="btn btn-sm btn-outline-primary d-flex align-items-center gap-1"
                  onClick={() => refetchLogs()}
                  disabled={logsFetching}
                >
                  <i className={`bi bi-arrow-clockwise ${logsFetching ? 'spin' : ''}`}></i>
                  Refresh
                </button>
              </div>
            </div>
          </div>

          {/* Monospace Terminal Body */}
          <div className="card-body p-0 bg-dark position-relative">
            <pre
              ref={logTerminalRef}
              className="text-light p-3 mb-0 font-monospace"
              style={{
                height: 520,
                overflowY: 'auto',
                fontSize: '0.8rem',
                lineHeight: 1.5,
                whiteSpace: 'pre-wrap',
                wordBreak: 'break-all',
              }}
            >
              {logTailData?.content || 'Connecting to node log stream...'}
            </pre>
          </div>

          <div className="card-footer bg-light border-top py-2 d-flex justify-content-between align-items-center text-muted small">
            <span>
              Target: <code className="text-dark">{selectedService}</code> on{' '}
              <code className="text-dark">Node {selectedNodeId}</code>
            </span>
            <span>
              {logTailData?.timestamp
                ? `Last sampled: ${new Date(logTailData.timestamp * 1000).toLocaleTimeString()}`
                : 'Waiting for log data...'}
            </span>
          </div>
        </div>
      )}
    </div>
  );
}
