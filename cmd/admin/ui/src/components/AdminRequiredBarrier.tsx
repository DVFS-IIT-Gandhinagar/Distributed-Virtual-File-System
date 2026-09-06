import { useAuth } from '../context/AuthContext';

interface Props {
  title: string;
  description: string;
  icon?: string;
}

export default function AdminRequiredBarrier({
  title,
  description,
  icon = 'bi-shield-lock',
}: Props) {
  const { openLoginModal } = useAuth();

  return (
    <div className="container py-5">
      <div className="row justify-content-center">
        <div className="col-md-8 col-lg-6">
          <div className="card shadow-sm border-0 text-center p-4 p-md-5">
            <div className="mb-3">
              <div
                className="d-inline-flex align-items-center justify-content-center rounded-circle bg-light text-warning"
                style={{ width: 80, height: 80, fontSize: '2.5rem' }}
              >
                <i className={`bi ${icon}`}></i>
              </div>
            </div>
            <h4 className="fw-bold mb-2">{title}</h4>
            <p className="text-muted small mb-4">{description}</p>
            <div>
              <button
                type="button"
                className="btn btn-primary d-inline-flex align-items-center gap-2 px-4 py-2"
                onClick={openLoginModal}
              >
                <i className="bi bi-key-fill"></i>
                Enter Admin Password
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
