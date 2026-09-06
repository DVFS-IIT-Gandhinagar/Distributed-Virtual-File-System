import { NavLink } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import type { ClusterResponse } from '../types';
import { fetchAlertSummary } from '../api';
import { getStatusBadgeClass, getStatusColor } from '../utils';
import { useAuth } from '../context/AuthContext';

interface Props {
  cluster?: ClusterResponse;
  lastUpdated: number;
}

function worstStatus(cluster?: ClusterResponse): string {
  if (!cluster) return 'offline';
  const priority = ['critical', 'degraded', 'warning', 'offline', 'online'];
  let worst = 'online';
  for (const node of cluster.nodes) {
    const idx = priority.indexOf(node.status);
    if (idx !== -1 && idx < priority.indexOf(worst)) worst = node.status;
  }
  return worst;
}

export default function AppNavbar({ cluster, lastUpdated }: Props) {
  const { isAuthenticated, openLoginModal, logout } = useAuth();
  const { data: alertSummary } = useQuery({
    queryKey: ['alertSummary'],
    queryFn: fetchAlertSummary,
    refetchInterval: 5000,
  });

  const status = worstStatus(cluster);
  const badgeClass = getStatusBadgeClass(status);
  const borderColor = getStatusColor(status);
  const secsAgo = lastUpdated ? Math.floor((Date.now() - lastUpdated) / 1000) : null;

  return (
    <nav className="navbar navbar-expand-lg navbar-dark bg-dark shadow-sm">
      <div className="container-fluid px-4">
        {/* Brand */}
        <span className="navbar-brand fw-bold fs-5 d-flex align-items-center gap-2">
          <i className="bi bi-hdd-network" style={{ fontSize: '1.3rem', color: '#0dcaf0' }}></i>
          DVFS Admin
        </span>

        <button
          className="navbar-toggler"
          type="button"
          data-bs-toggle="collapse"
          data-bs-target="#navMenu"
          aria-controls="navMenu"
          aria-expanded="false"
          aria-label="Toggle navigation"
        >
          <span className="navbar-toggler-icon"></span>
        </button>

        <div className="collapse navbar-collapse" id="navMenu">
          <ul className="navbar-nav me-auto mb-2 mb-lg-0">
            <li className="nav-item">
              <NavLink
                to="/"
                end
                className={({ isActive }) =>
                  'nav-link' + (isActive ? ' active fw-semibold' : '')
                }
              >
                <i className="bi bi-speedometer2 me-1"></i>Overview
              </NavLink>
            </li>
            <li className="nav-item">
              <NavLink
                to="/nodes"
                className={({ isActive }) =>
                  'nav-link' + (isActive ? ' active fw-semibold' : '')
                }
              >
                <i className="bi bi-server me-1"></i>Nodes
              </NavLink>
            </li>
            <li className="nav-item">
              <NavLink
                to="/performance"
                className={({ isActive }) =>
                  'nav-link' + (isActive ? ' active fw-semibold' : '')
                }
              >
                <i className="bi bi-graph-up me-1"></i>Performance
              </NavLink>
            </li>
            <li className="nav-item">
              <NavLink
                to="/users"
                className={({ isActive }) =>
                  'nav-link' + (isActive ? ' active fw-semibold' : '')
                }
              >
                <i className="bi bi-people me-1"></i>Users
                {!isAuthenticated && (
                  <i className="bi bi-lock-fill ms-1 text-secondary" style={{ fontSize: '0.7rem' }} title="Admin login required"></i>
                )}
              </NavLink>
            </li>
            <li className="nav-item">
              <NavLink
                to="/actions"
                className={({ isActive }) =>
                  'nav-link' + (isActive ? ' active fw-semibold' : '')
                }
              >
                <i className="bi bi-play-circle me-1"></i>Actions
                {!isAuthenticated && (
                  <i className="bi bi-lock-fill ms-1 text-secondary" style={{ fontSize: '0.7rem' }} title="Admin login required"></i>
                )}
              </NavLink>
            </li>
            <li className="nav-item">
              <NavLink
                to="/logs"
                className={({ isActive }) =>
                  'nav-link position-relative' + (isActive ? ' active fw-semibold' : '')
                }
              >
                <i className="bi bi-bell me-1"></i>Logs &amp; Alerts
                {!isAuthenticated && (
                  <i className="bi bi-lock-fill ms-1 text-secondary" style={{ fontSize: '0.7rem' }} title="Admin login required"></i>
                )}
                {alertSummary && (alertSummary.critical_count > 0 || alertSummary.warning_count > 0) && (
                  <span
                    className={`badge rounded-pill ms-1 ${
                      alertSummary.critical_count > 0 ? 'bg-danger' : 'bg-warning text-dark'
                    }`}
                    style={{ fontSize: '0.65rem' }}
                  >
                    {alertSummary.critical_count + alertSummary.warning_count}
                  </span>
                )}
              </NavLink>
            </li>
          </ul>

          {/* Right side: health pill + last updated + Admin Auth Button */}
          <div className="d-flex align-items-center gap-3">
            {cluster && (
              <span
                className={`badge rounded-pill ${badgeClass} px-3 py-2`}
                style={{ fontSize: '0.78rem', border: `2px solid ${borderColor}` }}
              >
                <i className="bi bi-circle-fill me-1" style={{ fontSize: '0.5rem' }}></i>
                Cluster {status.toUpperCase()}
              </span>
            )}
            {secsAgo !== null && (
              <span className="text-secondary small d-none d-md-inline">
                <i className="bi bi-arrow-clockwise me-1"></i>
                {secsAgo < 5 ? 'Just updated' : `Updated ${secsAgo}s ago`}
              </span>
            )}

            {/* Admin Authentication Control */}
            {isAuthenticated ? (
              <div className="d-flex align-items-center gap-2">
                <span className="badge bg-success px-2 py-1 small d-flex align-items-center gap-1 shadow-sm">
                  <i className="bi bi-shield-check"></i>
                  <span>Admin Mode</span>
                </span>
                <button
                  type="button"
                  className="btn btn-outline-light btn-sm d-flex align-items-center gap-1 py-1 px-2"
                  onClick={logout}
                  title="Logout from Admin Console"
                  style={{ fontSize: '0.78rem' }}
                >
                  <i className="bi bi-box-arrow-right"></i>
                  Logout
                </button>
              </div>
            ) : (
              <button
                type="button"
                className="btn btn-warning btn-sm text-dark fw-bold d-flex align-items-center gap-1 py-1 px-3 shadow-sm"
                onClick={openLoginModal}
                title="Enter password to unlock admin console"
                style={{ fontSize: '0.8rem' }}
              >
                <i className="bi bi-shield-lock-fill"></i>
                Admin
              </button>
            )}
          </div>
        </div>
      </div>
    </nav>
  );
}
