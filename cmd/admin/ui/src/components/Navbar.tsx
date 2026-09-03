import { NavLink } from 'react-router-dom';
import type { ClusterResponse } from '../types';
import { getStatusBadgeClass, getStatusColor } from '../utils';

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
          </ul>

          {/* Right side: health pill + last updated */}
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
              <span className="text-secondary small">
                <i className="bi bi-arrow-clockwise me-1"></i>
                {secsAgo < 5 ? 'Just updated' : `Updated ${secsAgo}s ago`}
              </span>
            )}
          </div>
        </div>
      </div>
    </nav>
  );
}
