import { useState, useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { fetchCluster } from '../api';
import NodeCard from '../components/NodeCard';
import NodeDetailPanel from '../components/NodeDetailPanel';
import type { NodeInfo } from '../types';

export default function Nodes() {
  const [selected, setSelected] = useState<NodeInfo | null>(null);

  const { data: cluster, isLoading, isError } = useQuery({
    queryKey: ['cluster'],
    queryFn: fetchCluster,
    refetchInterval: 5000,
  });

  const sortedNodes = useMemo(() => {
    if (!cluster?.nodes) return [];
    return [...cluster.nodes].sort((a, b) => {
      if (a.displayID && b.displayID && a.displayID !== b.displayID) {
        return a.displayID - b.displayID;
      }
      const numA = parseInt(a.fsID, 10);
      const numB = parseInt(b.fsID, 10);
      if (!isNaN(numA) && !isNaN(numB)) return numA - numB;
      return a.fsID.localeCompare(b.fsID);
    });
  }, [cluster?.nodes]);

  const nodeCounts = useMemo(() => {
    let online = 0;
    let warning = 0;
    let degraded = 0;
    let critical = 0;
    let offline = 0;

    for (const node of cluster?.nodes || []) {
      switch (node.status) {
        case 'online':
          online++;
          break;
        case 'warning':
          warning++;
          break;
        case 'degraded':
          degraded++;
          break;
        case 'critical':
          critical++;
          break;
        case 'offline':
        default:
          offline++;
          break;
      }
    }
    const unhealthy = warning + degraded + critical;
    return {
      online,
      warning,
      degraded,
      critical,
      offline,
      unhealthy,
      total: cluster?.nodes?.length || 0,
    };
  }, [cluster?.nodes]);

  if (isLoading) {
    return (
      <div className="container-fluid py-5 text-center">
        <div className="spinner-border text-primary" role="status">
          <span className="visually-hidden">Loading...</span>
        </div>
        <p className="mt-3 text-muted">Loading nodes...</p>
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

  return (
    <>
      <div className="container-fluid py-4 px-4">
        {/* Header */}
        <div className="d-flex align-items-center justify-content-between mb-4">
          <div>
            <h4 className="fw-bold mb-0">
              <i className="bi bi-server me-2 text-primary"></i>
              Nodes
            </h4>
            <p className="text-muted small mb-0">
              {nodeCounts.online} of {nodeCounts.total} node{nodeCounts.total !== 1 ? 's' : ''} healthy
              {nodeCounts.unhealthy > 0 && (
                <span className="text-warning fw-semibold ms-1">
                  ({nodeCounts.unhealthy} {nodeCounts.unhealthy === 1 ? 'node has issues' : 'nodes have issues'})
                </span>
              )}
            </p>
          </div>
          <div className="d-flex align-items-center gap-2 flex-wrap">
            <span className="badge bg-success rounded-pill px-3">{nodeCounts.online} Online</span>
            {nodeCounts.unhealthy > 0 && (
              <span
                className={`badge rounded-pill px-3 ${
                  nodeCounts.critical > 0
                    ? 'bg-danger'
                    : 'bg-warning text-dark'
                }`}
              >
                {nodeCounts.unhealthy} Issues
              </span>
            )}
            <span className="badge bg-secondary rounded-pill px-3">
              {nodeCounts.offline} Offline
            </span>
          </div>
        </div>

        {/* Node Grid */}
        {sortedNodes.length === 0 ? (
          <div className="text-center py-5">
            <i className="bi bi-hdd-network text-muted" style={{ fontSize: '3rem' }}></i>
            <p className="mt-3 text-muted">No nodes registered in the cluster.</p>
          </div>
        ) : (
          <div className="row g-4">
            {sortedNodes.map(node => (
              <div key={node.fsID} className="col-sm-6 col-lg-4 col-xl-3">
                <NodeCard
                  node={node}
                  onClick={() => setSelected(node)}
                />
              </div>
            ))}
          </div>
        )}
      </div>

      {/* Offcanvas Detail Panel */}
      {selected && (
        <NodeDetailPanel
          node={selected}
          show={selected !== null}
          onClose={() => setSelected(null)}
        />
      )}
    </>
  );
}
