import type { ReactNode } from 'react';

interface Props {
  title: string;
  value: ReactNode;
  subtitle?: ReactNode;
  icon: string; // bootstrap-icon class e.g. 'bi-server'
  iconColor?: string;
  onClick?: () => void;
  children?: ReactNode;
}

export default function StatCard({ title, value, subtitle, icon, iconColor = '#0dcaf0', onClick, children }: Props) {
  return (
    <div
      className={`card h-100 shadow-sm border-0 ${onClick ? 'clickable-card' : ''}`}
      style={{ cursor: onClick ? 'pointer' : 'default' }}
      onClick={onClick}
    >
      <div className="card-body">
        <div className="d-flex align-items-start justify-content-between">
          <div>
            <p className="text-muted small mb-1 text-uppercase fw-semibold" style={{ letterSpacing: '0.05em', fontSize: '0.72rem' }}>
              {title}
            </p>
            <h4 className="mb-0 fw-bold">{value}</h4>
            {subtitle && <p className="text-muted small mb-0 mt-1">{subtitle}</p>}
          </div>
          <div
            className="rounded-circle d-flex align-items-center justify-content-center"
            style={{ width: 44, height: 44, background: `${iconColor}1a`, flexShrink: 0 }}
          >
            <i className={`bi ${icon}`} style={{ fontSize: '1.3rem', color: iconColor }}></i>
          </div>
        </div>
        {children && <div className="mt-3">{children}</div>}
      </div>
    </div>
  );
}
