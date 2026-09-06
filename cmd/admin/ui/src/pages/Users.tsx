import { useState, useMemo, Fragment } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { fetchUsers, updateUserQuota } from '../api';
import type { UserSummary } from '../types';
import { formatBytes, getUserQuotaColor, getUserQuotaBadge, formatNodeDisplayName, formatMachineName } from '../utils';
import { ResponsiveContainer, BarChart, Bar, XAxis, YAxis, Tooltip, CartesianGrid } from 'recharts';
import { useAuth } from '../context/AuthContext';
import AdminRequiredBarrier from '../components/AdminRequiredBarrier';

type SortField = 'username' | 'home_node' | 'used_storage' | 'quota_limit' | 'usage_percent' | 'active_sessions' | 'is_online';

export default function Users() {
  const queryClient = useQueryClient();
  const [search, setSearch] = useState('');
  const [sortField, setSortField] = useState<SortField>('username');
  const [sortDirection, setSortDirection] = useState<'asc' | 'desc'>('asc');
  const [currentPage, setCurrentPage] = useState<number>(1);
  const [pageSize, setPageSize] = useState<number>(10);
  const [expandedUser, setExpandedUser] = useState<string | null>(null);
  const [editingUser, setEditingUser] = useState<UserSummary | null>(null);

  // Modal form state
  const { isAuthenticated } = useAuth();
  const [quotaValue, setQuotaValue] = useState<number>(1);
  const [quotaUnit, setQuotaUnit] = useState<'MiB' | 'GiB' | 'TiB'>('GiB');
  const [modalError, setModalError] = useState<string | null>(null);
  const [saveSuccessMsg, setSaveSuccessMsg] = useState<string | null>(null);

  const { data: users = [], isLoading, error } = useQuery<UserSummary[]>({
    queryKey: ['users'],
    queryFn: fetchUsers,
    refetchInterval: 5000,
    enabled: isAuthenticated,
  });

  const quotaMutation = useMutation({
    mutationFn: async ({ username, quotaBytes }: { username: string; quotaBytes: number }) => {
      return updateUserQuota(username, quotaBytes);
    },
    onSuccess: (_, variables) => {
      queryClient.invalidateQueries({ queryKey: ['users'] });
      queryClient.invalidateQueries({ queryKey: ['cluster'] });
      setSaveSuccessMsg(`Quota for ${variables.username} successfully updated!`);
      setEditingUser(null);
      setTimeout(() => setSaveSuccessMsg(null), 4000);
    },
    onError: (err: Error) => {
      setModalError(err.message || 'Failed to update quota');
    },
  });

  const filteredUsers = useMemo(() => {
    return users.filter((u) => u.username.toLowerCase().includes(search.toLowerCase()));
  }, [users, search]);

  const sortedUsers = useMemo(() => {
    const list = [...filteredUsers];
    list.sort((a, b) => {
      let cmp = 0;
      switch (sortField) {
        case 'username':
          cmp = a.username.localeCompare(b.username);
          break;
        case 'home_node':
          cmp = a.home_fs_id.localeCompare(b.home_fs_id, undefined, { numeric: true });
          break;
        case 'used_storage':
          cmp = a.quota_used - b.quota_used;
          break;
        case 'quota_limit':
          cmp = a.quota_limit - b.quota_limit;
          break;
        case 'usage_percent':
          cmp = a.usage_percent - b.usage_percent;
          break;
        case 'active_sessions':
          cmp = a.active_sessions - b.active_sessions;
          break;
        case 'is_online':
          cmp = (a.is_online ? 1 : 0) - (b.is_online ? 1 : 0);
          break;
      }
      return sortDirection === 'asc' ? cmp : -cmp;
    });
    return list;
  }, [filteredUsers, sortField, sortDirection]);

  const totalPages = Math.max(1, Math.ceil(sortedUsers.length / pageSize));
  const paginatedUsers = useMemo(() => {
    const start = (currentPage - 1) * pageSize;
    return sortedUsers.slice(start, start + pageSize);
  }, [sortedUsers, currentPage, pageSize]);

  const handleSort = (field: SortField) => {
    if (sortField === field) {
      setSortDirection(sortDirection === 'asc' ? 'desc' : 'asc');
    } else {
      setSortField(field);
      setSortDirection('asc');
    }
    setCurrentPage(1);
  };

  const getSortIcon = (field: SortField) => {
    if (sortField !== field) {
      return <i className="bi bi-arrow-down-up text-muted ms-1" style={{ fontSize: '0.75rem', opacity: 0.5 }}></i>;
    }
    return sortDirection === 'asc' ? (
      <i className="bi bi-sort-up text-primary ms-1" style={{ fontSize: '0.85rem' }}></i>
    ) : (
      <i className="bi bi-sort-down text-primary ms-1" style={{ fontSize: '0.85rem' }}></i>
    );
  };

  const openEditModal = (u: UserSummary) => {
    setEditingUser(u);
    setModalError(null);
    const tib = u.quota_limit / (1024 * 1024 * 1024 * 1024);
    const gib = u.quota_limit / (1024 * 1024 * 1024);
    if (tib >= 1 && Number.isInteger(tib)) {
      setQuotaValue(tib);
      setQuotaUnit('TiB');
    } else if (gib >= 1 && Number.isInteger(gib)) {
      setQuotaValue(gib);
      setQuotaUnit('GiB');
    } else {
      const mib = Math.round(u.quota_limit / (1024 * 1024));
      setQuotaValue(mib);
      setQuotaUnit('MiB');
    }
  };

  const handleSaveQuota = (e: React.FormEvent) => {
    e.preventDefault();
    if (!editingUser) return;
    if (quotaValue <= 0) {
      setModalError('Quota must be greater than 0');
      return;
    }

    const multiplier =
      quotaUnit === 'TiB' ? 1024 * 1024 * 1024 * 1024 :
      quotaUnit === 'GiB' ? 1024 * 1024 * 1024 : 1024 * 1024;
    const quotaBytes = Math.round(quotaValue * multiplier);

    quotaMutation.mutate({ username: editingUser.username, quotaBytes });
  };

  const calculatedBytes = quotaValue * (
    quotaUnit === 'TiB' ? 1024 * 1024 * 1024 * 1024 :
    quotaUnit === 'GiB' ? 1024 * 1024 * 1024 : 1024 * 1024
  );
  const isLowerThanUsage = editingUser ? calculatedBytes < editingUser.quota_used : false;

  if (!isAuthenticated) {
    return (
      <AdminRequiredBarrier
        title="User & Quota Management"
        description="Viewing and managing user accounts, quotas, and active sessions requires administrator access."
        icon="bi-people-fill"
      />
    );
  }

  return (
    <div className="container-fluid px-4 py-4">
      {/* Header */}
      <div className="d-flex flex-column flex-md-row justify-content-between align-items-md-center gap-3 mb-4">
        <div>
          <h4 className="fw-bold mb-1">
            <i className="bi bi-people me-2 text-primary"></i>User & Quota Management
          </h4>
          <p className="text-muted small mb-0">
            View allocated quotas, monitor storage consumption, and configure per-user storage limits.
          </p>
        </div>
        <div className="d-flex align-items-center gap-2">
          <div className="input-group" style={{ maxWidth: 300 }}>
            <span className="input-group-text bg-white border-end-0">
              <i className="bi bi-search text-muted"></i>
            </span>
            <input
              type="text"
              className="form-control border-start-0 ps-0"
              placeholder="Filter users..."
              value={search}
              onChange={(e) => {
                setSearch(e.target.value);
                setCurrentPage(1);
              }}
            />
          </div>
        </div>
      </div>

      {saveSuccessMsg && (
        <div className="alert alert-success alert-dismissible fade show d-flex align-items-center mb-4" role="alert">
          <i className="bi bi-check-circle-fill me-2 fs-5"></i>
          <div>{saveSuccessMsg}</div>
          <button type="button" className="btn-close" onClick={() => setSaveSuccessMsg(null)} aria-label="Close"></button>
        </div>
      )}

      {error && (
        <div className="alert alert-danger d-flex align-items-center mb-4" role="alert">
          <i className="bi bi-exclamation-triangle-fill me-2 fs-5"></i>
          <div>Failed to load users: {(error as Error).message}</div>
        </div>
      )}

      {/* Users Table Card */}
      <div className="card shadow-sm border-0">
        <div className="card-header bg-white py-3 border-bottom d-flex justify-content-between align-items-center">
          <h6 className="mb-0 fw-bold">
            <i className="bi bi-person-lines-fill me-2 text-secondary"></i>
            Registered Users ({sortedUsers.length})
          </h6>
          <span className="badge bg-light text-dark border">
            Auto-refreshing (5s)
          </span>
        </div>

        <div className="table-responsive">
          <table className="table table-hover align-middle mb-0">
            <thead className="table-light text-secondary small">
              <tr>
                <th style={{ width: '40px' }}></th>
                <th style={{ cursor: 'pointer', userSelect: 'none' }} onClick={() => handleSort('username')}>
                  Username {getSortIcon('username')}
                </th>
                <th style={{ cursor: 'pointer', userSelect: 'none' }} onClick={() => handleSort('home_node')}>
                  Home Node {getSortIcon('home_node')}
                </th>
                <th style={{ cursor: 'pointer', userSelect: 'none' }} onClick={() => handleSort('used_storage')}>
                  Used Storage {getSortIcon('used_storage')}
                </th>
                <th style={{ cursor: 'pointer', userSelect: 'none' }} onClick={() => handleSort('quota_limit')}>
                  Quota Limit {getSortIcon('quota_limit')}
                </th>
                <th style={{ minWidth: '200px', cursor: 'pointer', userSelect: 'none' }} onClick={() => handleSort('usage_percent')}>
                  % Used {getSortIcon('usage_percent')}
                </th>
                <th style={{ cursor: 'pointer', userSelect: 'none' }} onClick={() => handleSort('active_sessions')}>
                  Active Sessions {getSortIcon('active_sessions')}
                </th>
                <th style={{ cursor: 'pointer', userSelect: 'none' }} onClick={() => handleSort('is_online')}>
                  Connection {getSortIcon('is_online')}
                </th>
                <th>Quota Status</th>
                <th className="text-end pe-4">Actions</th>
              </tr>
            </thead>
            <tbody>
              {isLoading ? (
                <tr>
                  <td colSpan={10} className="text-center py-5 text-muted">
                    <div className="spinner-border spinner-border-sm me-2" role="status"></div>
                    Loading cluster user list...
                  </td>
                </tr>
              ) : paginatedUsers.length === 0 ? (
                <tr>
                  <td colSpan={10} className="text-center py-5 text-muted">
                    <i className="bi bi-person-x fs-1 d-block mb-2 text-secondary"></i>
                    No users found matching your criteria.
                  </td>
                </tr>
              ) : (
                paginatedUsers.map((u) => {
                  const badge = getUserQuotaBadge(u.usage_percent);
                  const barColor = getUserQuotaColor(u.usage_percent);
                  const isExpanded = expandedUser === u.username;
                  const displayPct = Math.min(u.usage_percent, 100);

                  return (
                    <Fragment key={u.username}>
                      <tr style={{ cursor: 'pointer' }} onClick={() => setExpandedUser(isExpanded ? null : u.username)}>
                        <td className="text-center text-muted">
                          <i className={`bi bi-chevron-${isExpanded ? 'down' : 'right'} small`}></i>
                        </td>
                        <td>
                          <div className="d-flex align-items-center gap-2">
                            <div
                              className="rounded-circle bg-primary bg-opacity-10 text-primary d-flex align-items-center justify-content-center fw-bold"
                              style={{ width: 34, height: 34, fontSize: '0.85rem' }}
                            >
                              {u.username.charAt(0).toUpperCase()}
                            </div>
                            <div>
                              <div className="d-flex align-items-center gap-2">
                                <span className="fw-semibold">{u.username}</span>
                                {u.is_online ? (
                                  <span className="badge bg-success bg-opacity-10 text-success border border-success border-opacity-25 px-2 py-0" style={{ fontSize: '0.68rem' }}>
                                    <i className="bi bi-circle-fill me-1" style={{ fontSize: '0.45rem' }}></i>Online
                                  </span>
                                ) : (
                                  <span className="badge bg-secondary bg-opacity-10 text-secondary border border-secondary border-opacity-25 px-2 py-0" style={{ fontSize: '0.68rem' }}>
                                    <i className="bi bi-circle me-1" style={{ fontSize: '0.45rem' }}></i>Offline
                                  </span>
                                )}
                              </div>
                              <small className="text-muted">Home {formatNodeDisplayName(u.home_fs_id)} ({formatMachineName(u.home_fs_id)})</small>
                            </div>
                          </div>
                        </td>
                        <td>
                          <span className="badge bg-secondary bg-opacity-10 text-secondary border">
                            <i className="bi bi-server me-1"></i>{formatNodeDisplayName(u.home_fs_id)}
                          </span>
                        </td>
                        <td className="fw-medium">{formatBytes(u.quota_used)}</td>
                        <td className="text-secondary">{formatBytes(u.quota_limit)}</td>
                        <td>
                          <div className="d-flex align-items-center gap-2">
                            <div className="progress flex-grow-1" style={{ height: '8px' }}>
                              <div
                                className="progress-bar"
                                role="progressbar"
                                style={{ width: `${displayPct}%`, backgroundColor: barColor }}
                                aria-valuenow={u.usage_percent}
                                aria-valuemin={0}
                                aria-valuemax={100}
                              />
                            </div>
                            <span className="small fw-semibold text-nowrap" style={{ color: barColor, minWidth: '45px' }}>
                              {u.usage_percent.toFixed(1)}%
                            </span>
                          </div>
                        </td>
                        <td>
                          <span className={`badge ${u.is_online ? 'bg-success bg-opacity-10 text-success border border-success border-opacity-25' : 'bg-light text-dark border'}`}>
                            <i className="bi bi-link-45deg me-1"></i>{u.active_sessions}
                          </span>
                        </td>
                        <td>
                          {u.is_online ? (
                            <span className="badge bg-success bg-opacity-10 text-success border border-success border-opacity-25">
                              <i className="bi bi-check-circle-fill me-1"></i>Connected
                            </span>
                          ) : (
                            <span className="badge bg-secondary bg-opacity-10 text-secondary border border-secondary border-opacity-25">
                              <i className="bi bi-dash-circle me-1"></i>Idle
                            </span>
                          )}
                        </td>
                        <td>
                          {badge ? (
                            <span
                              className={`badge ${badge.badgeClass}`}
                              style={badge.color === '#fd7e14' ? { backgroundColor: '#fd7e14', color: '#fff' } : {}}
                            >
                              {badge.label}
                            </span>
                          ) : (
                            <span className="badge bg-success bg-opacity-10 text-success border border-success">
                              Normal
                            </span>
                          )}
                        </td>
                        <td className="text-end pe-4" onClick={(e) => e.stopPropagation()}>
                          <button
                            type="button"
                            className="btn btn-outline-primary btn-sm px-3"
                            onClick={() => openEditModal(u)}
                          >
                            <i className="bi bi-pencil me-1"></i>Edit Quota
                          </button>
                        </td>
                      </tr>

                      {/* Expanded Row: Per-Node Distribution */}
                      {isExpanded && (
                        <tr className="bg-light">
                          <td colSpan={10} className="p-4">
                            <div className="card border shadow-sm">
                              <div className="card-body">
                                <h6 className="fw-bold mb-3">
                                  <i className="bi bi-hdd-stack me-2 text-primary"></i>
                                  Storage Distribution for '{u.username}' Across Nodes
                                </h6>
                                {u.nodes.length === 0 ? (
                                  <p className="text-muted small mb-0">No active storage chunks found for this user.</p>
                                ) : (
                                  <div className="row align-items-center">
                                    <div className="col-md-7">
                                      <div style={{ width: '100%', height: 180 }}>
                                        <ResponsiveContainer>
                                          <BarChart
                                            data={u.nodes.map((n) => ({
                                              name: `${formatNodeDisplayName(n.fs_id)} (${formatMachineName(n.fs_id)})`,
                                              used: Number((n.used_bytes / (1024 * 1024)).toFixed(2)),
                                            }))}
                                            layout="vertical"
                                            margin={{ top: 5, right: 30, left: 40, bottom: 5 }}
                                          >
                                            <CartesianGrid strokeDasharray="3 3" horizontal={false} />
                                            <XAxis type="number" unit=" MB" tick={{ fontSize: 11 }} />
                                            <YAxis dataKey="name" type="category" tick={{ fontSize: 12 }} />
                                            <Tooltip formatter={(value: number) => [`${value} MB`, 'Used']} />
                                            <Bar dataKey="used" fill="#0d6efd" radius={[0, 4, 4, 0]} />
                                          </BarChart>
                                        </ResponsiveContainer>
                                      </div>
                                    </div>
                                    <div className="col-md-5">
                                      <ul className="list-group list-group-flush small">
                                        {u.nodes.map((n) => (
                                          <li key={n.fs_id} className="list-group-item d-flex justify-content-between align-items-center px-0 bg-transparent">
                                            <div>
                                              <span className="fw-semibold">Node {formatNodeDisplayName(n.fs_id)} ({formatMachineName(n.fs_id)})</span>
                                              <small className="text-muted d-block">{n.address}</small>
                                            </div>
                                            <span className="fw-medium text-dark">{formatBytes(n.used_bytes)}</span>
                                          </li>
                                        ))}
                                      </ul>
                                    </div>
                                  </div>
                                )}
                              </div>
                            </div>
                          </td>
                        </tr>
                      )}
                    </Fragment>
                  );
                })
              )}
            </tbody>
          </table>
        </div>

        {/* Pagination Controls */}
        {sortedUsers.length > 0 && (
          <div className="card-footer bg-white border-top py-3 d-flex flex-column flex-md-row justify-content-between align-items-center gap-3">
            <div className="small text-muted">
              Showing <span className="fw-semibold">{(currentPage - 1) * pageSize + 1}</span> to{' '}
              <span className="fw-semibold">{Math.min(currentPage * pageSize, sortedUsers.length)}</span> of{' '}
              <span className="fw-semibold">{sortedUsers.length}</span> users
            </div>
            <div className="d-flex align-items-center gap-3">
              <div className="d-flex align-items-center gap-2">
                <label htmlFor="pageSizeSelect" className="small text-muted text-nowrap mb-0">Per page:</label>
                <select
                  id="pageSizeSelect"
                  className="form-select form-select-sm"
                  style={{ width: '75px' }}
                  value={pageSize}
                  onChange={(e) => {
                    setPageSize(Number(e.target.value));
                    setCurrentPage(1);
                  }}
                >
                  <option value={5}>5</option>
                  <option value={10}>10</option>
                  <option value={25}>25</option>
                  <option value={50}>50</option>
                </select>
              </div>
              {totalPages > 1 && (
                <nav aria-label="Users pagination">
                  <ul className="pagination pagination-sm mb-0">
                    <li className={`page-item ${currentPage === 1 ? 'disabled' : ''}`}>
                      <button
                        type="button"
                        className="page-link"
                        onClick={() => setCurrentPage((p) => Math.max(1, p - 1))}
                        disabled={currentPage === 1}
                      >
                        Previous
                      </button>
                    </li>
                    {Array.from({ length: totalPages }, (_, i) => i + 1).map((page) => (
                      <li key={page} className={`page-item ${currentPage === page ? 'active' : ''}`}>
                        <button type="button" className="page-link" onClick={() => setCurrentPage(page)}>
                          {page}
                        </button>
                      </li>
                    ))}
                    <li className={`page-item ${currentPage === totalPages ? 'disabled' : ''}`}>
                      <button
                        type="button"
                        className="page-link"
                        onClick={() => setCurrentPage((p) => Math.min(totalPages, p + 1))}
                        disabled={currentPage === totalPages}
                      >
                        Next
                      </button>
                    </li>
                  </ul>
                </nav>
              )}
            </div>
          </div>
        )}
      </div>

      {/* Quota Edit Modal */}
      {editingUser && (
        <div className="modal show d-block" tabIndex={-1} style={{ backgroundColor: 'rgba(0,0,0,0.5)' }}>
          <div className="modal-dialog modal-dialog-centered">
            <div className="modal-content border-0 shadow">
              <div className="modal-header border-bottom">
                <h5 className="modal-title fw-bold">
                  <i className="bi bi-pencil-square me-2 text-primary"></i>
                  Edit Quota: {editingUser.username}
                </h5>
                <button
                  type="button"
                  className="btn-close"
                  onClick={() => setEditingUser(null)}
                  aria-label="Close"
                  disabled={quotaMutation.isPending}
                />
              </div>

              <form onSubmit={handleSaveQuota}>
                <div className="modal-body py-4">
                  {modalError && (
                    <div className="alert alert-danger small py-2 d-flex align-items-center mb-3">
                      <i className="bi bi-exclamation-octagon me-2"></i>
                      <div>{modalError}</div>
                    </div>
                  )}

                  {/* Summary Details */}
                  <div className="bg-light p-3 rounded mb-3 small">
                    <div className="d-flex justify-content-between mb-1">
                      <span className="text-muted">Target Home Fileserver:</span>
                      <span className="fw-semibold">FS-{editingUser.home_fs_id} ({editingUser.home_fs_address})</span>
                    </div>
                    <div className="d-flex justify-content-between mb-1">
                      <span className="text-muted">Current Storage Used:</span>
                      <span className="fw-semibold">{formatBytes(editingUser.quota_used)}</span>
                    </div>
                    <div className="d-flex justify-content-between">
                      <span className="text-muted">Current Quota Limit:</span>
                      <span className="fw-semibold">{formatBytes(editingUser.quota_limit)}</span>
                    </div>
                  </div>

                  {/* Quota Input */}
                  <div className="mb-3">
                    <label className="form-label fw-semibold small">New Quota Limit</label>
                    <div className="input-group">
                      <input
                        type="number"
                        step="any"
                        min="0.1"
                        className="form-control"
                        value={quotaValue}
                        onChange={(e) => setQuotaValue(parseFloat(e.target.value) || 0)}
                        required
                        disabled={quotaMutation.isPending}
                      />
                      <select
                        className="form-select"
                        style={{ maxWidth: '100px' }}
                        value={quotaUnit}
                        onChange={(e) => setQuotaUnit(e.target.value as 'MiB' | 'GiB' | 'TiB')}
                        disabled={quotaMutation.isPending}
                      >
                        <option value="MiB">MiB</option>
                        <option value="GiB">GiB</option>
                        <option value="TiB">TiB</option>
                      </select>
                    </div>
                    <small className="text-muted mt-1 d-block">
                      Equates to: {formatBytes(calculatedBytes)}
                    </small>
                  </div>

                  {/* Warning if reducing quota below usage */}
                  {isLowerThanUsage && (
                    <div className="alert alert-warning small py-2 d-flex align-items-start mb-0">
                      <i className="bi bi-exclamation-triangle-fill text-warning me-2 fs-6 mt-1"></i>
                      <div>
                        <strong>Notice:</strong> The new quota ({formatBytes(calculatedBytes)}) is less than current usage ({formatBytes(editingUser.quota_used)}).
                        The user will be flagged in red and blocked from creating or uploading any new files until space is freed.
                      </div>
                    </div>
                  )}
                </div>

                <div className="modal-footer border-top bg-light">
                  <button
                    type="button"
                    className="btn btn-outline-secondary"
                    onClick={() => setEditingUser(null)}
                    disabled={quotaMutation.isPending}
                  >
                    Cancel
                  </button>
                  <button
                    type="submit"
                    className="btn btn-primary px-4 d-flex align-items-center gap-2"
                    disabled={quotaMutation.isPending}
                  >
                    {quotaMutation.isPending && (
                      <span className="spinner-border spinner-border-sm" role="status" aria-hidden="true" />
                    )}
                    Save Quota
                  </button>
                </div>
              </form>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
