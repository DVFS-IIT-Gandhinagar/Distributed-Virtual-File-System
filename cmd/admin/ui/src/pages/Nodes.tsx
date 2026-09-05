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
              {cluster.online_count} of {cluster.node_count} node{cluster.node_count !== 1 ? 's' : ''} online
            </p>
          </div>
          <div className="d-flex align-items-center gap-2">
            <span className="badge bg-success rounded-pill px-3">{cluster.online_count} Online</span>
            <span className="badge bg-secondary rounded-pill px-3">
              {cluster.node_count - cluster.online_count} Offline
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
