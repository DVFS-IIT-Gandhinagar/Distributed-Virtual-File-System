import React, { useState, useEffect, useRef, useCallback, useMemo } from 'react';
import { useSearchParams } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { fetchCluster, fetchActionPresets, fetchCommandHistory } from '../api';
import type { ActionType, ActionRequest, NodeRestartParams, CommandRecord, ActionEvent } from '../types';
import { getStatusBadgeClass, formatUptime, formatNodeDisplayName, formatMachineName } from '../utils';

interface NodeExecutionStatus {
  status: 'pending' | 'running' | 'success' | 'failed';
  durationMs?: number;
  exitCode?: number;
  error?: string;
}

export default function Actions() {
  const queryClient = useQueryClient();
  const [searchParams] = useSearchParams();

  // Queries
  const { data: cluster } = useQuery({
    queryKey: ['cluster'],
    queryFn: fetchCluster,
    refetchInterval: 5000,
  });

  const { data: presets = {} } = useQuery({
    queryKey: ['action_presets'],
    queryFn: fetchActionPresets,
    refetchInterval: 10000,
  });

  const { data: history = [], refetch: refetchHistory } = useQuery({
    queryKey: ['action_history'],
    queryFn: fetchCommandHistory,
    refetchInterval: 5000,
  });

  // Selected Nodes
  const [selectedNodeIDs, setSelectedNodeIDs] = useState<string[]>([]);
  const selectedNodeIDsRef = useRef<string[]>(selectedNodeIDs);
  useEffect(() => {
    selectedNodeIDsRef.current = selectedNodeIDs;
  }, [selectedNodeIDs]);

  // Action Tabs
  const [activeAction, setActiveAction] = useState<ActionType>('pull');

  // Form State
  const [repoPath, setRepoPath] = useState('~/Distributed-Virtual-File-System');
  const [gitBranch, setGitBranch] = useState('main');
  const [makeTarget, setMakeTarget] = useState('');
  const [restartMode, setRestartMode] = useState<'systemctl' | 'binary'>('systemctl');
  const [targetService, setTargetService] = useState<'fileserver' | 'metaserver' | 'admin' | 'all'>('fileserver');
  const [aptMode, setAptMode] = useState<'update_upgrade' | 'update_only'>('update_upgrade');
  const [logMode, setLogMode] = useState<'journalctl' | 'tail'>('journalctl');
  const [logLines, setLogLines] = useState<number>(50);
  const [customCommand, setCustomCommand] = useState('');
  const [sshUser, setSshUser] = useState('');
  const [sshKeyPath, setSshKeyPath] = useState('');
  const [sshPort, setSshPort] = useState<number>(22);

  // Reboot Confirmation Modal State
  const [showRebootModal, setShowRebootModal] = useState(false);

  // Per-Node Restart Overrides
  const [nodeParamsOverrides, setNodeParamsOverrides] = useState<Record<string, NodeRestartParams>>({});

  // Terminal & Live Stream State
  const [terminalLines, setTerminalLines] = useState<string[]>([]);
  const [executing, setExecuting] = useState(false);
  const [execStatus, setExecStatus] = useState<'idle' | 'running' | 'success' | 'failed'>('idle');
  const [nodeStatusMap, setNodeStatusMap] = useState<Record<string, NodeExecutionStatus>>({});
  const [autoScroll, setAutoScroll] = useState(true);
  const terminalContainerRef = useRef<HTMLDivElement>(null);
  const wsRef = useRef<WebSocket | null>(null);

  // WebSocket Connection Resilience
  const [wsStatus, setWsStatus] = useState<'connected' | 'connecting' | 'disconnected'>('connecting');
  const reconnectAttemptsRef = useRef(0);
  const reconnectTimeoutRef = useRef<number | null>(null);

  // Selected History Record for Details Modal
  const [viewingRecord, setViewingRecord] = useState<CommandRecord | null>(null);

  // Always scroll window to top when opening the Actions tab
  useEffect(() => {
    window.scrollTo(0, 0);
  }, []);

  // Synchronize initial node selection when cluster loads (only ONCE or when query params change)
  const initialSelectionDoneRef = useRef(false);
  useEffect(() => {
    if (initialSelectionDoneRef.current) return;
    if (!cluster || cluster.nodes.length === 0) return;

    const nodeParam = searchParams.get('node');
    const actionParam = searchParams.get('action') as ActionType | null;

    if (nodeParam) {
      setSelectedNodeIDs([nodeParam]);
    } else {
      setSelectedNodeIDs(cluster.nodes.map((n) => n.fsID));
    }

    if (actionParam && ['pull', 'build', 'restart', 'reboot', 'apt', 'logs', 'custom'].includes(actionParam)) {
      setActiveAction(actionParam);
    }

    initialSelectionDoneRef.current = true;
  }, [cluster, searchParams]);

  // Auto-scroll terminal container only (does NOT scroll the outer window/page)
  useEffect(() => {
    if (autoScroll && terminalContainerRef.current) {
      terminalContainerRef.current.scrollTop = terminalContainerRef.current.scrollHeight;
    }
  }, [terminalLines, autoScroll]);

  const appendTerminal = (text: string) => {
    setTerminalLines((prev) => [...prev, text]);
  };

  const handleStreamEvent = (ev: ActionEvent) => {
    switch (ev.type) {
      case 'action_started':
        setExecuting(true);
        setExecStatus('running');
        appendTerminal(`\n🚀 [ACTION STARTED] ID: ${ev.action_id} | Command: ${ev.command || 'custom'}`);
        // Initialize all target nodes to pending in the live status matrix
        {
          const map: Record<string, NodeExecutionStatus> = {};
          selectedNodeIDsRef.current.forEach((id) => {
            map[id] = { status: 'pending' };
          });
          setNodeStatusMap(map);
        }
        break;

      case 'node_started':
        appendTerminal(`⚡ [${formatNodeDisplayName(ev.node_id || '')} | ${ev.address}] Starting execution...`);
        setNodeStatusMap((prev) => ({
          ...prev,
          [ev.node_id || '']: { status: 'running' },
        }));
        break;

      case 'node_output':
        if (ev.chunk) {
          const lines = ev.chunk.split('\n');
          lines.forEach((line) => {
            if (line.trim().length > 0) {
              const prefix = `[${formatNodeDisplayName(ev.node_id || '')}] `;
              appendTerminal(`${prefix}${line}`);
            }
          });
        }
        break;

      case 'node_finished': {
        const exitCode = ev.exit_code ?? (ev.error ? -1 : 0);
        const isSuccess = exitCode === 0 && !ev.error;
        const nodeName = formatNodeDisplayName(ev.node_id || '');
        if (isSuccess) {
          appendTerminal(`✅ [${nodeName}] Succeeded in ${ev.duration_ms}ms (Exit 0)`);
        } else {
          appendTerminal(`❌ [${nodeName}] Failed with code ${exitCode} (${ev.error || 'error'}) in ${ev.duration_ms}ms`);
        }
        setNodeStatusMap((prev) => ({
          ...prev,
          [ev.node_id || '']: {
            status: isSuccess ? 'success' : 'failed',
            durationMs: ev.duration_ms,
            exitCode: exitCode,
            error: ev.error,
          },
        }));
        break;
      }

      case 'action_finished':
        setExecuting(false);
        setExecStatus(ev.status === 'success' ? 'success' : 'failed');
        appendTerminal(`\n🏁 [ACTION COMPLETED] Status: ${ev.status?.toUpperCase()} in ${ev.duration_ms}ms\n`);
        refetchHistory();
        queryClient.invalidateQueries({ queryKey: ['cluster'] });
        break;

      case 'error':
        setExecuting(false);
        setExecStatus('failed');
        appendTerminal(`⚠️ [ERROR] ${ev.error}`);
        break;
    }
  };

  const handleStreamEventRef = useRef(handleStreamEvent);
  useEffect(() => {
    handleStreamEventRef.current = handleStreamEvent;
  });

  const connectWebSocket = useCallback(() => {
    if (wsRef.current && (wsRef.current.readyState === WebSocket.OPEN || wsRef.current.readyState === WebSocket.CONNECTING)) {
      return;
    }
    setWsStatus('connecting');
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const host = window.location.host;
    const ws = new WebSocket(`${protocol}//${host}/ws/actions`);
    wsRef.current = ws;

    ws.onopen = () => {
      setWsStatus('connected');
      reconnectAttemptsRef.current = 0;
      appendTerminal('[SYSTEM] Connected to orchestration stream.');
    };

    ws.onmessage = (event) => {
      try {
        const ev: ActionEvent = JSON.parse(event.data);
        handleStreamEventRef.current(ev);
      } catch (err) {
        appendTerminal(`[RAW] ${event.data}`);
      }
    };

    ws.onclose = () => {
      setWsStatus('disconnected');
      appendTerminal('[SYSTEM] Orchestration stream disconnected.');
      const delay = Math.min(1000 * Math.pow(2, reconnectAttemptsRef.current), 15000);
      reconnectAttemptsRef.current += 1;
      reconnectTimeoutRef.current = window.setTimeout(connectWebSocket, delay);
    };

    ws.onerror = () => {
      setWsStatus('disconnected');
    };
  }, []);

  // Connect WebSocket on mount with auto-reconnect cleanup
  useEffect(() => {
    connectWebSocket();
    return () => {
      if (reconnectTimeoutRef.current) clearTimeout(reconnectTimeoutRef.current);
      if (wsRef.current) wsRef.current.close();
    };
  }, [connectWebSocket]);

  const dispatchAction = () => {
    const payload: ActionRequest = {
      action_type: activeAction,
      target_node_ids: selectedNodeIDs,
      repo_path: repoPath,
      git_branch: activeAction === 'pull' ? gitBranch : undefined,
      make_target: activeAction === 'build' ? makeTarget : undefined,
      custom_command: activeAction === 'custom' ? customCommand : undefined,
      target_service: (activeAction === 'restart' || activeAction === 'logs') ? targetService : undefined,
      apt_mode: activeAction === 'apt' ? aptMode : undefined,
      restart_mode: restartMode,
      log_mode: logMode,
      log_lines: logLines,
      ssh_user: sshUser || undefined,
      ssh_key_path: sshKeyPath || undefined,
      ssh_port: sshPort && sshPort !== 22 ? sshPort : undefined,
      restart_params: { ...presets, ...nodeParamsOverrides },
    };

    if (wsRef.current && wsRef.current.readyState === WebSocket.OPEN) {
      wsRef.current.send(JSON.stringify(payload));
    } else {
      appendTerminal('⚠️ [ERROR] WebSocket is not connected. Reconnecting...');
    }
  };

  const handleExecute = (e?: React.FormEvent) => {
    if (e) e.preventDefault();
    if (selectedNodeIDs.length === 0) {
      alert('Please select at least one node to target.');
      return;
    }
    if (activeAction === 'reboot') {
      setShowRebootModal(true);
      return;
    }
    dispatchAction();
  };

  // Deterministically sort target nodes numerically (0, 1, ... 8 -> FS-1 to FS-9)
  const sortedTargetNodes = useMemo(() => {
    if (!cluster?.nodes) return [];
    return [...cluster.nodes].sort((a, b) => {
      const numA = parseInt(a.fsID, 10);
      const numB = parseInt(b.fsID, 10);
      if (!isNaN(numA) && !isNaN(numB)) return numA - numB;
      return a.fsID.localeCompare(b.fsID);
    });
  }, [cluster?.nodes]);

  const toggleSelectAll = () => {
    if (!cluster) return;
    if (selectedNodeIDs.length === cluster.nodes.length) {
      setSelectedNodeIDs([]);
    } else {
      setSelectedNodeIDs(sortedTargetNodes.map((n) => n.fsID));
    }
  };

  const selectHealthyOnly = () => {
    if (!cluster) return;
    const healthy = sortedTargetNodes.filter((n) => n.status === 'online').map((n) => n.fsID);
    setSelectedNodeIDs(healthy);
  };

  const copyTerminalOutput = () => {
    navigator.clipboard.writeText(terminalLines.join('\n'));
    alert('Terminal output copied to clipboard!');
  };

  return (
    <div className="container-fluid px-4 py-4">
      {/* Page Header */}
      <div className="d-flex flex-column flex-md-row justify-content-between align-items-md-center gap-3 mb-4">
        <div>
          <h4 className="fw-bold mb-1">
            <i className="bi bi-play-circle me-2 text-primary"></i>Cluster Orchestration & Actions
          </h4>
          <p className="text-muted small mb-0">
            Execute remote maintenance, software updates, process restarts, and live diagnostic commands across cluster nodes.
          </p>
        </div>
        <div className="d-flex align-items-center gap-2">
          <span className="badge bg-light text-dark border px-3 py-2">
            <i className="bi bi-shield-lock me-1 text-secondary"></i>SSH Key-Based Auth
          </span>
        </div>
      </div>

      {/* Node Selector Card */}
      <div className="card shadow-sm border-0 mb-4">
        <div className="card-header bg-white py-3 border-bottom d-flex flex-wrap justify-content-between align-items-center gap-2">
          <h6 className="mb-0 fw-bold">
            <i className="bi bi-hdd-network me-2 text-primary"></i>
            Target Nodes ({selectedNodeIDs.length} / {cluster?.nodes.length || 0} selected)
          </h6>
          <div className="d-flex gap-2">
            <button type="button" className="btn btn-outline-secondary btn-sm" onClick={toggleSelectAll}>
              {selectedNodeIDs.length === (cluster?.nodes.length || 0) ? 'Clear All' : 'Select All'}
            </button>
            <button type="button" className="btn btn-outline-success btn-sm" onClick={selectHealthyOnly}>
              <i className="bi bi-check-circle me-1"></i>Healthy Only
            </button>
          </div>
        </div>
        <div className="card-body">
          <div className="row g-3">
            {sortedTargetNodes.map((node) => {
              const isSelected = selectedNodeIDs.includes(node.fsID);
              const nodeDisplay = formatNodeDisplayName(node);
              const machine = formatMachineName(node);
              return (
                <div key={node.fsID} className="col-sm-6 col-md-4 col-xl-3">
                  <div
                    className={`card p-3 border h-100 ${isSelected ? 'border-primary bg-primary bg-opacity-10' : 'bg-light'}`}
                    style={{ cursor: 'pointer' }}
                    onClick={() => {
                      if (isSelected) {
                        setSelectedNodeIDs(selectedNodeIDs.filter((id) => id !== node.fsID));
                      } else {
                        setSelectedNodeIDs([...selectedNodeIDs, node.fsID]);
                      }
                    }}
                  >
                    <div className="d-flex justify-content-between align-items-center mb-1">
                      <div className="form-check mb-0">
                        <input
                          type="checkbox"
                          className="form-check-input"
                          checked={isSelected}
                          onChange={() => {}}
                        />
                        <label className="form-check-label fw-bold ms-1">
                          {nodeDisplay} <small className="text-muted fw-normal">({machine})</small>
                        </label>
                      </div>
                      <span className={`badge ${getStatusBadgeClass(node.status)}`}>{node.status}</span>
                    </div>
                    <small className="text-muted text-truncate">{node.address}</small>
                    <small className="text-muted mt-1">Uptime: {formatUptime(node.metrics.uptime_seconds)}</small>
                  </div>
                </div>
              );
            })}
            {(!cluster || cluster.nodes.length === 0) && (
              <p className="text-muted mb-0">No nodes discovered in metaserver state.</p>
            )}
          </div>
        </div>
      </div>

      {/* Main Operations Grid */}
      <div className="row g-4 mb-4">
        {/* Left Column: Action Configuration */}
        <div className="col-lg-5">
          <div className="card shadow-sm border-0 h-100">
            <div className="card-header bg-white border-bottom p-0">
              <ul className="nav nav-tabs card-header-tabs m-0 border-0">
                <li className="nav-item">
                  <button
                    className={`nav-link border-0 py-3 px-3 fw-semibold ${activeAction === 'pull' ? 'active text-primary' : 'text-muted'}`}
                    onClick={() => setActiveAction('pull')}
                  >
                    <i className="bi bi-git me-1"></i>Pull Repo
                  </button>
                </li>
                <li className="nav-item">
                  <button
                    className={`nav-link border-0 py-3 px-3 fw-semibold ${activeAction === 'build' ? 'active text-primary' : 'text-muted'}`}
                    onClick={() => setActiveAction('build')}
                  >
                    <i className="bi bi-hammer me-1"></i>Build
                  </button>
                </li>
                <li className="nav-item">
                  <button
                    className={`nav-link border-0 py-3 px-3 fw-semibold ${activeAction === 'restart' ? 'active text-primary' : 'text-muted'}`}
                    onClick={() => setActiveAction('restart')}
                  >
                    <i className="bi bi-arrow-repeat me-1"></i>Restart
                  </button>
                </li>
                <li className="nav-item">
                  <button
                    className={`nav-link border-0 py-3 px-3 fw-semibold ${activeAction === 'reboot' ? 'active text-danger' : 'text-muted'}`}
                    onClick={() => setActiveAction('reboot')}
                  >
                    <i className="bi bi-power me-1 text-danger"></i>Reboot
                  </button>
                </li>
                <li className="nav-item">
                  <button
                    className={`nav-link border-0 py-3 px-3 fw-semibold ${activeAction === 'apt' ? 'active text-success' : 'text-muted'}`}
                    onClick={() => setActiveAction('apt')}
                  >
                    <i className="bi bi-arrow-up-circle me-1 text-success"></i>APT Update
                  </button>
                </li>
                <li className="nav-item">
                  <button
                    className={`nav-link border-0 py-3 px-3 fw-semibold ${activeAction === 'logs' ? 'active text-primary' : 'text-muted'}`}
                    onClick={() => setActiveAction('logs')}
                  >
                    <i className="bi bi-journal-text me-1"></i>Logs
                  </button>
                </li>
                <li className="nav-item">
                  <button
                    className={`nav-link border-0 py-3 px-3 fw-semibold ${activeAction === 'custom' ? 'active text-primary' : 'text-muted'}`}
                    onClick={() => setActiveAction('custom')}
                  >
                    <i className="bi bi-terminal me-1"></i>Custom
                  </button>
                </li>
              </ul>
            </div>

            <div className="card-body py-4">
              <form onSubmit={handleExecute}>
                {/* 1. Pull Form */}
                {activeAction === 'pull' && (
                  <div>
                    <h6 className="fw-bold mb-3">Git Pull Configuration</h6>
                    <div className="mb-3">
                      <label className="form-label small fw-semibold">Cloned Repository Path</label>
                      <input
                        type="text"
                        className="form-control"
                        value={repoPath}
                        onChange={(e) => setRepoPath(e.target.value)}
                        placeholder="~/Distributed-Virtual-File-System"
                        required
                      />
                    </div>
                    <div className="mb-3">
                      <label className="form-label small fw-semibold">Branch</label>
                      <input
                        type="text"
                        className="form-control"
                        value={gitBranch}
                        onChange={(e) => setGitBranch(e.target.value)}
                        placeholder="main"
                      />
                    </div>
                    <p className="text-muted small">
                      Runs <code>git -C {repoPath} pull origin {gitBranch}</code> via SSH across {selectedNodeIDs.length} selected node(s).
                    </p>
                  </div>
                )}

                {/* 2. Build Form */}
                {activeAction === 'build' && (
                  <div>
                    <h6 className="fw-bold mb-3">Build Binary Configuration</h6>
                    <div className="mb-3">
                      <label className="form-label small fw-semibold">Repository Path</label>
                      <input
                        type="text"
                        className="form-control"
                        value={repoPath}
                        onChange={(e) => setRepoPath(e.target.value)}
                        required
                      />
                    </div>
                    <div className="mb-3">
                      <label className="form-label small fw-semibold">Make Target (Optional)</label>
                      <input
                        type="text"
                        className="form-control"
                        value={makeTarget}
                        onChange={(e) => setMakeTarget(e.target.value)}
                        placeholder="e.g. all, fileserver"
                      />
                    </div>
                    <p className="text-muted small">
                      Runs <code>make -C {repoPath} {makeTarget}</code> on remote nodes.
                    </p>
                  </div>
                )}

                {/* 3. Restart Form */}
                {activeAction === 'restart' && (
                  <div>
                    <div className="d-flex justify-content-between align-items-center mb-3">
                      <h6 className="fw-bold mb-0">Service Process Restart</h6>
                      <div className="btn-group btn-group-sm" role="group">
                        <button
                          type="button"
                          className={`btn ${restartMode === 'systemctl' ? 'btn-primary' : 'btn-outline-secondary'}`}
                          onClick={() => setRestartMode('systemctl')}
                        >
                          systemctl
                        </button>
                        <button
                          type="button"
                          className={`btn ${restartMode === 'binary' ? 'btn-primary' : 'btn-outline-secondary'}`}
                          onClick={() => setRestartMode('binary')}
                        >
                          Direct Binary
                        </button>
                      </div>
                    </div>

                    {/* Service Target Selector */}
                    <div className="mb-3">
                      <label className="form-label small fw-semibold">Target Service to Restart</label>
                      <div className="btn-group btn-group-sm w-100" role="group">
                        <button
                          type="button"
                          className={`btn ${targetService === 'fileserver' ? 'btn-primary' : 'btn-outline-secondary'}`}
                          onClick={() => setTargetService('fileserver')}
                        >
                          <i className="bi bi-hdd-network me-1"></i>Fileserver
                        </button>
                        <button
                          type="button"
                          className={`btn ${targetService === 'metaserver' ? 'btn-primary' : 'btn-outline-secondary'}`}
                          onClick={() => setTargetService('metaserver')}
                        >
                          <i className="bi bi-diagram-3 me-1"></i>Metaserver
                        </button>
                        <button
                          type="button"
                          className={`btn ${targetService === 'admin' ? 'btn-primary' : 'btn-outline-secondary'}`}
                          onClick={() => setTargetService('admin')}
                        >
                          <i className="bi bi-speedometer2 me-1"></i>Admin Console
                        </button>
                        <button
                          type="button"
                          className={`btn ${targetService === 'all' ? 'btn-primary' : 'btn-outline-secondary'}`}
                          onClick={() => setTargetService('all')}
                        >
                          <i className="bi bi-grid-fill me-1"></i>All Services
                        </button>
                      </div>
                    </div>

                    {restartMode === 'systemctl' ? (
                      <div className="alert alert-info py-2 small mb-3">
                        <i className="bi bi-info-circle me-1"></i>
                        {targetService === 'fileserver' && (
                          <span>Executes <code>sudo systemctl restart dvfs-fileserver</code>. Requires systemd service installed on nodes.</span>
                        )}
                        {targetService === 'metaserver' && (
                          <span>Executes <code>sudo systemctl restart dvfs-metaserver</code> on selected nodes.</span>
                        )}
                        {targetService === 'admin' && (
                          <span>Executes <code>sudo systemctl restart dvfs-admin</code> on selected nodes.</span>
                        )}
                        {targetService === 'all' && (
                          <span>Executes <code>sudo systemctl restart dvfs-metaserver dvfs-fileserver dvfs-admin</code> on selected nodes.</span>
                        )}
                      </div>
                    ) : (
                      <div className="mb-3">
                        <small className="text-muted d-block mb-2">
                          {targetService === 'fileserver' && (
                            <>Direct launch uses <code>pkill</code> followed by <code>nohup ./bin/fileserver ...</code>.</>
                          )}
                          {targetService === 'metaserver' && (
                            <>Direct launch uses <code>pkill -f metaserver</code> followed by <code>nohup ./bin/metaserver ... &</code>.</>
                          )}
                          {targetService === 'admin' && (
                            <>Direct launch uses <code>pkill -f bin/admin</code> followed by <code>nohup ./bin/admin ... &</code>.</>
                          )}
                          {targetService === 'all' && (
                            <>Direct launch stops active binaries and restarts metaserver, fileserver, and admin in the background.</>
                          )}
                        </small>
                        {targetService === 'fileserver' && (
                          <div className="accordion accordion-flush border rounded" id="restartParamsAccordion">
                            {selectedNodeIDs.map((id) => {
                              const p = nodeParamsOverrides[id] || presets[id] || {
                                fs_id: id,
                                port: 50052,
                                meta_addr: '127.0.0.1:50051',
                                own_ip: '127.0.0.1',
                                data_dir: `./fileserver_data_${id}`,
                              };
                              return (
                                <div key={id} className="accordion-item">
                                  <h2 className="accordion-header" id={`flush-heading-${id}`}>
                                    <button
                                      className="accordion-button collapsed py-2 small fw-semibold"
                                      type="button"
                                      data-bs-toggle="collapse"
                                      data-bs-target={`#flush-collapse-${id}`}
                                    >
                                      Node FS-{id} Parameters ({p.host || p.address})
                                    </button>
                                  </h2>
                                  <div id={`flush-collapse-${id}`} className="accordion-collapse collapse p-3 bg-light">
                                    <div className="row g-2 small">
                                      <div className="col-6">
                                        <label className="form-label">Port</label>
                                        <input
                                          type="number"
                                          className="form-control form-control-sm"
                                          value={p.port}
                                          onChange={(e) =>
                                            setNodeParamsOverrides({
                                              ...nodeParamsOverrides,
                                              [id]: { ...p, port: Number(e.target.value) },
                                            })
                                          }
                                        />
                                      </div>
                                      <div className="col-6">
                                        <label className="form-label">Own IP</label>
                                        <input
                                          type="text"
                                          className="form-control form-control-sm"
                                          value={p.own_ip}
                                          onChange={(e) =>
                                            setNodeParamsOverrides({
                                              ...nodeParamsOverrides,
                                              [id]: { ...p, own_ip: e.target.value },
                                            })
                                          }
                                        />
                                      </div>
                                      <div className="col-12">
                                        <label className="form-label">Meta Server Addr</label>
                                        <input
                                          type="text"
                                          className="form-control form-control-sm"
                                          value={p.meta_addr}
                                          onChange={(e) =>
                                            setNodeParamsOverrides({
                                              ...nodeParamsOverrides,
                                              [id]: { ...p, meta_addr: e.target.value },
                                            })
                                          }
                                        />
                                      </div>
                                    </div>
                                  </div>
                                </div>
                              );
                            })}
                          </div>
                        )}
                      </div>
                    )}
                  </div>
                )}

                {/* 3b. Reboot Form */}
                {activeAction === 'reboot' && (
                  <div>
                    <h6 className="fw-bold mb-3 text-danger">
                      <i className="bi bi-power me-2"></i>Machine Remote Reboot
                    </h6>
                    <div className="alert alert-danger d-flex align-items-start gap-2 mb-3">
                      <i className="bi bi-exclamation-triangle-fill fs-5 mt-1 flex-shrink-0"></i>
                      <div className="small">
                        <strong>Host-Level Machine Reboot:</strong>
                        <p className="mb-0 mt-1">
                          This action runs <code>sudo -n reboot</code> on the selected machine(s) with a 2-second background delay, allowing the SSH session to close cleanly before host reboot begins.
                        </p>
                        <p className="mb-0 mt-1 text-danger-emphasis">
                          All processes running on the host will be interrupted. Fileservers running as systemd services will restart automatically once the node completes boot.
                        </p>
                      </div>
                    </div>
                    <div className="p-3 bg-light rounded border mb-3">
                      <div className="small text-muted mb-2">Target Machines to Reboot ({selectedNodeIDs.length}):</div>
                      <div className="d-flex flex-wrap gap-1">
                        {selectedNodeIDs.length === 0 ? (
                          <span className="text-danger small fst-italic">No target nodes selected!</span>
                        ) : (
                          selectedNodeIDs.map((id) => (
                            <span key={id} className="badge bg-danger bg-opacity-10 text-danger border border-danger border-opacity-25 px-2 py-1">
                              <i className="bi bi-server me-1"></i>{formatNodeDisplayName(id)} ({formatMachineName(id)})
                            </span>
                          ))
                        )}
                      </div>
                    </div>
                  </div>
                )}

                {/* 3c. APT Update Form */}
                {activeAction === 'apt' && (
                  <div>
                    <h6 className="fw-bold mb-3 text-success">
                      <i className="bi bi-arrow-up-circle me-2"></i>APT Package Update & Upgrade
                    </h6>
                    <div className="alert alert-success d-flex align-items-start gap-2 mb-3">
                      <i className="bi bi-check-circle-fill fs-5 mt-1 flex-shrink-0"></i>
                      <div className="small">
                        <strong>Debian / Ubuntu Package Management:</strong>
                        <p className="mb-0 mt-1">
                          Executes system package updates across selected cluster nodes via <code>apt</code> with non-interactive flags.
                        </p>
                      </div>
                    </div>

                    <div className="mb-3">
                      <label className="form-label small fw-semibold">Update Mode</label>
                      <div className="d-flex flex-column gap-2">
                        <div className="form-check">
                          <input
                            className="form-check-input"
                            type="radio"
                            name="aptModeRadio"
                            id="aptModeBoth"
                            checked={aptMode === 'update_upgrade'}
                            onChange={() => setAptMode('update_upgrade')}
                          />
                          <label className="form-check-label small" htmlFor="aptModeBoth">
                            <strong>Update & Upgrade:</strong> <code>sudo apt update && sudo apt upgrade -y</code>
                            <div className="text-muted small">Synchronizes package lists and upgrades all installed packages non-interactively.</div>
                          </label>
                        </div>
                        <div className="form-check">
                          <input
                            className="form-check-input"
                            type="radio"
                            name="aptModeRadio"
                            id="aptModeUpdateOnly"
                            checked={aptMode === 'update_only'}
                            onChange={() => setAptMode('update_only')}
                          />
                          <label className="form-check-label small" htmlFor="aptModeUpdateOnly">
                            <strong>Update Lists Only:</strong> <code>sudo apt update</code>
                            <div className="text-muted small">Refreshes repository indices without modifying installed software.</div>
                          </label>
                        </div>
                      </div>
                    </div>

                    <div className="p-3 bg-light rounded border mb-3">
                      <div className="small text-muted mb-2">Target Machines for APT ({selectedNodeIDs.length}):</div>
                      <div className="d-flex flex-wrap gap-1">
                        {selectedNodeIDs.length === 0 ? (
                          <span className="text-danger small fst-italic">No target nodes selected!</span>
                        ) : (
                          selectedNodeIDs.map((id) => (
                            <span key={id} className="badge bg-success bg-opacity-10 text-success border border-success border-opacity-25 px-2 py-1">
                              <i className="bi bi-server me-1"></i>{formatNodeDisplayName(id)} ({formatMachineName(id)})
                            </span>
                          ))
                        )}
                      </div>
                    </div>
                  </div>
                )}

                {/* 4. Logs Form */}
                {activeAction === 'logs' && (
                  <div>
                    <div className="d-flex justify-content-between align-items-center mb-3">
                      <h6 className="fw-bold mb-0">View Service Logs</h6>
                      <div className="btn-group btn-group-sm" role="group">
                        <button
                          type="button"
                          className={`btn ${logMode === 'journalctl' ? 'btn-primary' : 'btn-outline-secondary'}`}
                          onClick={() => setLogMode('journalctl')}
                        >
                          journalctl
                        </button>
                        <button
                          type="button"
                          className={`btn ${logMode === 'tail' ? 'btn-primary' : 'btn-outline-secondary'}`}
                          onClick={() => setLogMode('tail')}
                        >
                          File Tail
                        </button>
                      </div>
                    </div>

                    <div className="mb-3">
                      <label className="form-label small fw-semibold">Target Service</label>
                      <div className="btn-group btn-group-sm w-100" role="group">
                        <button
                          type="button"
                          className={`btn ${targetService === 'fileserver' ? 'btn-primary' : 'btn-outline-secondary'}`}
                          onClick={() => setTargetService('fileserver')}
                        >
                          <i className="bi bi-hdd-network me-1"></i>Fileserver
                        </button>
                        <button
                          type="button"
                          className={`btn ${targetService === 'metaserver' ? 'btn-primary' : 'btn-outline-secondary'}`}
                          onClick={() => setTargetService('metaserver')}
                        >
                          <i className="bi bi-diagram-3 me-1"></i>Metaserver
                        </button>
                        <button
                          type="button"
                          className={`btn ${targetService === 'admin' ? 'btn-primary' : 'btn-outline-secondary'}`}
                          onClick={() => setTargetService('admin')}
                        >
                          <i className="bi bi-speedometer2 me-1"></i>Admin Console
                        </button>
                      </div>
                    </div>

                    <div className="mb-3">
                      <label className="form-label small fw-semibold">Number of Lines</label>
                      <select
                        className="form-select"
                        value={logLines}
                        onChange={(e) => setLogLines(Number(e.target.value))}
                      >
                        <option value={25}>Last 25 lines</option>
                        <option value={50}>Last 50 lines</option>
                        <option value={100}>Last 100 lines</option>
                        <option value={200}>Last 200 lines</option>
                      </select>
                    </div>

                    <p className="text-muted small">
                      {logMode === 'journalctl'
                        ? `Fetches logs using journalctl -u ${targetService === 'metaserver' ? 'dvfs-metaserver' : targetService === 'admin' ? 'dvfs-admin' : 'dvfs-fileserver'} -n ${logLines} --no-pager.`
                        : `Tails ${repoPath}/${targetService === 'metaserver' ? 'metaserver.log' : targetService === 'admin' ? 'admin.log' : 'fileserver.log'}.`}
                    </p>
                  </div>
                )}

                {/* 5. Custom Command Form */}
                {activeAction === 'custom' && (
                  <div>
                    <h6 className="fw-bold mb-3">Custom Command Execution</h6>
                    <div className="mb-3">
                      <label className="form-label small fw-semibold">Command to Run</label>
                      <input
                        type="text"
                        className="form-control font-monospace"
                        value={customCommand}
                        onChange={(e) => setCustomCommand(e.target.value)}
                        placeholder="e.g. uptime, df -h, free -m"
                        required
                      />
                    </div>
                    <div className="d-flex flex-wrap gap-2 mb-3">
                      <span className="small text-muted me-1">Presets:</span>
                      {['uptime', 'df -h', 'free -m', 'sudo systemctl status dvfs-fileserver'].map((preset) => (
                        <button
                          key={preset}
                          type="button"
                          className="btn btn-outline-secondary btn-sm py-0 px-2 font-monospace"
                          style={{ fontSize: '0.75rem' }}
                          onClick={() => setCustomCommand(preset)}
                        >
                          {preset}
                        </button>
                      ))}
                    </div>
                  </div>
                )}

                {/* SSH Credentials Override (Collapsible) */}
                <div className="border-top pt-3 mt-3">
                  <details className="small">
                    <summary className="text-muted fw-semibold" style={{ cursor: 'pointer' }}>
                      <i className="bi bi-gear me-1"></i>SSH Credentials & Overrides
                    </summary>
                    <div className="row g-2 mt-2">
                      <div className="col-5">
                        <label className="form-label text-muted">SSH Username</label>
                        <input
                          type="text"
                          className="form-control form-control-sm"
                          placeholder="e.g. ubuntu"
                          value={sshUser}
                          onChange={(e) => setSshUser(e.target.value)}
                        />
                      </div>
                      <div className="col-5">
                        <label className="form-label text-muted">Key Path</label>
                        <input
                          type="text"
                          className="form-control form-control-sm"
                          placeholder="~/.ssh/id_ed25519"
                          value={sshKeyPath}
                          onChange={(e) => setSshKeyPath(e.target.value)}
                        />
                      </div>
                      <div className="col-2">
                        <label className="form-label text-muted">Port</label>
                        <input
                          type="number"
                          className="form-control form-control-sm"
                          value={sshPort}
                          onChange={(e) => setSshPort(Number(e.target.value) || 22)}
                        />
                      </div>
                    </div>
                  </details>
                </div>

                {/* Submit Action Button */}
                <div className="mt-4">
                  <button
                    type="submit"
                    className={`btn ${activeAction === 'reboot' ? 'btn-danger' : activeAction === 'apt' ? 'btn-success' : 'btn-primary'} w-100 py-2 fw-semibold d-flex align-items-center justify-content-center gap-2`}
                    disabled={executing || selectedNodeIDs.length === 0}
                  >
                    {executing ? (
                      <>
                        <span className="spinner-border spinner-border-sm" role="status" aria-hidden="true"></span>
                        Executing Action...
                      </>
                    ) : activeAction === 'reboot' ? (
                      <>
                        <i className="bi bi-power fs-5"></i>
                        Reboot {selectedNodeIDs.length} Machine(s)
                      </>
                    ) : activeAction === 'apt' ? (
                      <>
                        <i className="bi bi-arrow-up-circle fs-5"></i>
                        Run APT {aptMode === 'update_upgrade' ? 'Update & Upgrade' : 'Update'} on {selectedNodeIDs.length} Node(s)
                      </>
                    ) : (
                      <>
                        <i className="bi bi-play-fill fs-5"></i>
                        Execute {activeAction.toUpperCase()} on {selectedNodeIDs.length} Node(s)
                      </>
                    )}
                  </button>
                </div>
              </form>
            </div>
          </div>
        </div>

        {/* Right Column: Live Monospace Terminal & Per-Node Status Grid */}
        <div className="col-lg-7">
          {/* Live Per-Node Status Matrix (Section 4.7) */}
          {Object.keys(nodeStatusMap).length > 0 && (
            <div className="card shadow-sm border-0 mb-3">
              <div className="card-header bg-white py-2 px-3 border-bottom d-flex justify-content-between align-items-center">
                <span className="fw-semibold small">
                  <i className="bi bi-diagram-3 me-2 text-primary"></i>Live Node Execution Status
                </span>
                <span className="badge bg-light text-dark border small">
                  {Object.values(nodeStatusMap).filter((n) => n.status === 'success').length} / {Object.keys(nodeStatusMap).length} Finished
                </span>
              </div>
              <div className="card-body p-2 bg-light">
                <div className="row g-2">
                  {Object.entries(nodeStatusMap).map(([nodeId, info]) => (
                    <div key={nodeId} className="col-sm-6 col-md-4">
                      <div className="p-2 rounded bg-white border d-flex justify-content-between align-items-center">
                        <span className="fw-bold small">{formatNodeDisplayName(nodeId)}</span>
                        <div>
                          {info.status === 'pending' && (
                            <span className="badge bg-secondary text-light small">
                              <i className="bi bi-hourglass me-1"></i>Pending
                            </span>
                          )}
                          {info.status === 'running' && (
                            <span className="badge bg-warning text-dark small">
                              <span className="spinner-border spinner-border-sm me-1" style={{ width: '0.65rem', height: '0.65rem' }}></span>Running
                            </span>
                          )}
                          {info.status === 'success' && (
                            <span className="badge bg-success small">
                              <i className="bi bi-check-circle me-1"></i>Success ({info.durationMs}ms)
                            </span>
                          )}
                          {info.status === 'failed' && (
                            <span className="badge bg-danger small" title={info.error || `Exit ${info.exitCode}`}>
                              <i className="bi bi-x-circle me-1"></i>Failed
                            </span>
                          )}
                        </div>
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            </div>
          )}

          <div className="card shadow-sm border-0 h-100 d-flex flex-column">
            <div className="card-header bg-dark text-light py-2 px-3 d-flex justify-content-between align-items-center">
              <div className="d-flex align-items-center gap-2">
                <i className="bi bi-terminal text-success"></i>
                <span className="fw-semibold small font-monospace">Live Output Terminal</span>
                {execStatus === 'running' && (
                  <span className="badge bg-warning text-dark ms-1" style={{ fontSize: '0.7rem' }}>
                    <span className="spinner-grow spinner-grow-sm me-1" style={{ width: '0.6rem', height: '0.6rem' }} role="status"></span>
                    Running
                  </span>
                )}
                {execStatus === 'success' && (
                  <span className="badge bg-success ms-1" style={{ fontSize: '0.7rem' }}>Success</span>
                )}
                {execStatus === 'failed' && (
                  <span className="badge bg-danger ms-1" style={{ fontSize: '0.7rem' }}>Failed</span>
                )}

                {/* WebSocket Status Indicator */}
                {wsStatus === 'connected' && (
                  <span className="badge bg-success bg-opacity-25 text-success border border-success ms-1" style={{ fontSize: '0.7rem' }}>
                    <i className="bi bi-wifi me-1"></i>Connected
                  </span>
                )}
                {wsStatus === 'connecting' && (
                  <span className="badge bg-warning bg-opacity-25 text-warning border border-warning ms-1" style={{ fontSize: '0.7rem' }}>
                    <span className="spinner-grow spinner-grow-sm me-1" style={{ width: '0.45rem', height: '0.45rem' }}></span>Connecting...
                  </span>
                )}
                {wsStatus === 'disconnected' && (
                  <div className="d-inline-flex align-items-center gap-1 ms-1">
                    <span className="badge bg-danger bg-opacity-25 text-danger border border-danger" style={{ fontSize: '0.7rem' }}>
                      <i className="bi bi-wifi-off me-1"></i>Offline
                    </span>
                    <button
                      type="button"
                      className="btn btn-outline-warning btn-sm py-0 px-1"
                      style={{ fontSize: '0.65rem' }}
                      onClick={connectWebSocket}
                      title="Reconnect WebSocket stream"
                    >
                      Reconnect
                    </button>
                  </div>
                )}
              </div>

              <div className="d-flex align-items-center gap-2">
                <button
                  type="button"
                  className={`btn btn-sm ${autoScroll ? 'btn-outline-light' : 'btn-secondary'} py-0 px-2`}
                  style={{ fontSize: '0.75rem' }}
                  onClick={() => setAutoScroll(!autoScroll)}
                  title="Toggle Auto-Scroll"
                >
                  <i className="bi bi-arrow-down-short me-1"></i>Scroll: {autoScroll ? 'ON' : 'OFF'}
                </button>
                <button
                  type="button"
                  className="btn btn-outline-light btn-sm py-0 px-2"
                  style={{ fontSize: '0.75rem' }}
                  onClick={copyTerminalOutput}
                  title="Copy Output"
                >
                  <i className="bi bi-clipboard me-1"></i>Copy
                </button>
                <button
                  type="button"
                  className="btn btn-outline-secondary btn-sm py-0 px-2"
                  style={{ fontSize: '0.75rem' }}
                  onClick={() => setTerminalLines([])}
                  title="Clear Terminal"
                >
                  <i className="bi bi-trash"></i>
                </button>
              </div>
            </div>

            <div
              ref={terminalContainerRef}
              className="card-body bg-dark text-light p-3 flex-grow-1 font-monospace"
              style={{
                minHeight: '380px',
                maxHeight: '480px',
                overflowY: 'auto',
                fontSize: '0.85rem',
                lineHeight: '1.45',
              }}
            >
              {terminalLines.length === 0 ? (
                <div className="text-secondary text-center py-5">
                  <i className="bi bi-terminal fs-1 d-block mb-2 opacity-50"></i>
                  Terminal idle. Trigger an action to stream output live.
                </div>
              ) : (
                terminalLines.map((line, idx) => (
                  <div
                    key={idx}
                    className="text-break"
                    style={{
                      color: line.includes('❌') || line.includes('[ERROR]') || line.includes('[STDERR]')
                        ? '#ff6b6b'
                        : line.includes('✅')
                        ? '#51cf66'
                        : line.includes('🚀')
                        ? '#74c0fc'
                        : line.includes('⚡')
                        ? '#fcc419'
                        : '#dee2e6',
                    }}
                  >
                    {line}
                  </div>
                ))
              )}
            </div>
          </div>
        </div>
      </div>

      {/* Command History Audit Log */}
      <div className="card shadow-sm border-0">
        <div className="card-header bg-white py-3 border-bottom d-flex justify-content-between align-items-center">
          <h6 className="mb-0 fw-bold">
            <i className="bi bi-clock-history me-2 text-secondary"></i>
            Command Execution History (Capped at 100 Entries)
          </h6>
          <span className="badge bg-light text-dark border">
            {history.length} record(s)
          </span>
        </div>

        <div className="table-responsive">
          <table className="table table-hover align-middle mb-0">
            <thead className="table-light text-secondary small">
              <tr>
                <th>Time</th>
                <th>Action</th>
                <th>Command</th>
                <th>Target Nodes</th>
                <th>Status</th>
                <th>Duration</th>
                <th className="text-end pe-4">Details</th>
              </tr>
            </thead>
            <tbody>
              {history.length === 0 ? (
                <tr>
                  <td colSpan={7} className="text-center py-4 text-muted small">
                    No commands executed yet.
                  </td>
                </tr>
              ) : (
                history.map((h) => {
                  const date = new Date(h.timestamp * 1000).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
                  return (
                    <tr key={h.id}>
                      <td className="small text-muted">{date}</td>
                      <td>
                        <span className="badge bg-primary bg-opacity-10 text-primary border border-primary text-uppercase">
                          {h.action_type}
                        </span>
                      </td>
                      <td className="font-monospace small text-truncate" style={{ maxWidth: 260 }} title={h.command}>
                        {h.command || `[${h.action_type}]`}
                      </td>
                      <td>
                        <div className="d-flex flex-wrap gap-1">
                          {h.target_nodes.map((nID) => (
                            <span key={nID} className="badge bg-light text-dark border" style={{ fontSize: '0.72rem' }}>
                              {formatNodeDisplayName(nID)}
                            </span>
                          ))}
                        </div>
                      </td>
                      <td>
                        <span
                          className={`badge ${
                            h.status === 'success'
                              ? 'bg-success'
                              : h.status === 'failed'
                              ? 'bg-danger'
                              : 'bg-warning text-dark'
                          }`}
                        >
                          {h.status}
                        </span>
                      </td>
                      <td className="small text-muted">{h.duration_ms}ms</td>
                      <td className="text-end pe-4">
                        <button
                          type="button"
                          className="btn btn-outline-secondary btn-sm py-0 px-2"
                          onClick={() => setViewingRecord(h)}
                        >
                          <i className="bi bi-eye me-1"></i>View Output
                        </button>
                      </td>
                    </tr>
                  );
                })
              )}
            </tbody>
          </table>
        </div>
      </div>

      {/* Details Output Modal */}
      {viewingRecord && (
        <div className="modal show d-block" tabIndex={-1} style={{ backgroundColor: 'rgba(0,0,0,0.5)' }}>
          <div className="modal-dialog modal-lg modal-dialog-centered">
            <div className="modal-content border-0 shadow">
              <div className="modal-header border-bottom">
                <h6 className="modal-title fw-bold">
                  <i className="bi bi-terminal me-2 text-primary"></i>
                  Execution Details: {viewingRecord.id}
                </h6>
                <button
                  type="button"
                  className="btn-close"
                  onClick={() => setViewingRecord(null)}
                  aria-label="Close"
                />
              </div>
              <div className="modal-body py-3">
                <div className="mb-3 d-flex flex-wrap gap-3 small text-muted">
                  <div><strong>Action:</strong> {viewingRecord.action_type}</div>
                  <div><strong>Command:</strong> <code>{viewingRecord.command || viewingRecord.action_type}</code></div>
                  <div><strong>Status:</strong> <span className={`badge ${viewingRecord.status === 'success' ? 'bg-success' : 'bg-danger'}`}>{viewingRecord.status}</span></div>
                  <div><strong>Total Duration:</strong> {viewingRecord.duration_ms}ms</div>
                </div>

                <h6 className="small fw-bold text-secondary mb-2">Per-Node Outputs:</h6>
                {Object.values(viewingRecord.node_results || {}).map((nr) => (
                  <div key={nr.node_id} className="card bg-light border mb-2">
                    <div className="card-header py-1 px-3 d-flex justify-content-between align-items-center small bg-white border-bottom">
                      <span className="fw-semibold">Node {formatNodeDisplayName(nr.node_id)} ({formatMachineName(nr.node_id)} | {nr.address})</span>
                      <div>
                        <span className={`badge ${nr.exit_code === 0 ? 'bg-success' : 'bg-danger'} me-2`}>
                          Exit {nr.exit_code}
                        </span>
                        <span className="text-muted">{nr.duration_ms}ms</span>
                      </div>
                    </div>
                    <div className="card-body p-2">
                      <pre className="bg-dark text-light p-2 rounded mb-0 font-monospace small" style={{ maxHeight: '180px', overflowY: 'auto' }}>
                        {nr.output || '(No output)'}
                      </pre>
                    </div>
                  </div>
                ))}
              </div>
              <div className="modal-footer border-top py-2">
                <button type="button" className="btn btn-secondary btn-sm" onClick={() => setViewingRecord(null)}>
                  Close
                </button>
              </div>
            </div>
          </div>
        </div>
      )}

      {/* Reboot Confirmation Modal */}
      {showRebootModal && (
        <div className="modal show d-block" style={{ backgroundColor: 'rgba(0,0,0,0.5)', zIndex: 1060 }} tabIndex={-1}>
          <div className="modal-dialog modal-dialog-centered">
            <div className="modal-content border-0 shadow">
              <div className="modal-header bg-danger text-white">
                <h5 className="modal-title fw-bold">
                  <i className="bi bi-exclamation-triangle-fill me-2"></i>Confirm Machine Reboot
                </h5>
                <button type="button" className="btn-close btn-close-white" onClick={() => setShowRebootModal(false)}></button>
              </div>
              <div className="modal-body py-4">
                <p className="fw-semibold">Are you sure you want to reboot the following machine(s)?</p>
                <div className="d-flex flex-wrap gap-2 mb-3">
                  {selectedNodeIDs.map((id) => (
                    <span key={id} className="badge bg-danger bg-opacity-10 text-danger border border-danger border-opacity-25 px-2 py-2">
                      <i className="bi bi-server me-1"></i>{formatNodeDisplayName(id)} ({formatMachineName(id)})
                    </span>
                  ))}
                </div>
                <p className="text-muted small mb-0">
                  The command <code>sudo reboot</code> will be scheduled with a 2-second background delay. The machine(s) will disconnect and remain offline temporarily until the operating system completes its reboot.
                </p>
              </div>
              <div className="modal-footer bg-light">
                <button type="button" className="btn btn-secondary" onClick={() => setShowRebootModal(false)}>Cancel</button>
                <button
                  type="button"
                  className="btn btn-danger fw-semibold"
                  onClick={() => {
                    setShowRebootModal(false);
                    dispatchAction();
                  }}
                >
                  <i className="bi bi-power me-1"></i>Yes, Reboot Now
                </button>
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
