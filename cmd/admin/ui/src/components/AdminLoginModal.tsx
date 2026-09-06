import React, { useState, useEffect, useRef } from 'react';
import { useAuth } from '../context/AuthContext';

export default function AdminLoginModal() {
  const { isLoginModalOpen, closeLoginModal, login } = useAuth();
  const [password, setPassword] = useState('');
  const [showPassword, setShowPassword] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (isLoginModalOpen) {
      setPassword('');
      setError(null);
      setTimeout(() => inputRef.current?.focus(), 50);
    }
  }, [isLoginModalOpen]);

  if (!isLoginModalOpen) {
    return null;
  }

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!password.trim()) {
      setError('Please enter the admin password.');
      return;
    }

    setSubmitting(true);
    setError(null);

    const res = await login(password);
    setSubmitting(false);

    if (!res.success) {
      setError(res.error || 'Invalid password. Check the password hash configured in .env.');
    }
  };

  return (
    <>
      <div
        className="modal-backdrop fade show"
        style={{ zIndex: 1050 }}
        onClick={closeLoginModal}
      />
      <div
        className="modal fade show d-block"
        tabIndex={-1}
        role="dialog"
        style={{ zIndex: 1055 }}
        onClick={(e) => {
          if (e.target === e.currentTarget) closeLoginModal();
        }}
      >
        <div className="modal-dialog modal-dialog-centered">
          <div className="modal-content shadow border-0">
            <div className="modal-header bg-dark text-white border-0 py-3">
              <h5 className="modal-title d-flex align-items-center gap-2 fs-6 fw-bold">
                <i className="bi bi-shield-lock-fill text-warning"></i>
                Admin Authentication
              </h5>
              <button
                type="button"
                className="btn-close btn-close-white"
                aria-label="Close"
                onClick={closeLoginModal}
              />
            </div>

            <form onSubmit={handleSubmit}>
              <div className="modal-body p-4">
                <p className="text-muted small mb-3">
                  Enter the administrator password to unlock cluster orchestration (git, build, reboot),
                  live system logs, user quotas, and sensitive cluster telemetry.
                </p>

                {error && (
                  <div className="alert alert-danger d-flex align-items-center gap-2 py-2 small" role="alert">
                    <i className="bi bi-exclamation-triangle-fill flex-shrink-0"></i>
                    <div>{error}</div>
                  </div>
                )}

                <div className="mb-3">
                  <label className="form-label small fw-semibold text-secondary">
                    Admin Password
                  </label>
                  <div className="input-group">
                    <input
                      ref={inputRef}
                      type={showPassword ? 'text' : 'password'}
                      className="form-control"
                      placeholder="Enter admin password"
                      value={password}
                      onChange={(e) => {
                        setPassword(e.target.value);
                        if (error) setError(null);
                      }}
                      disabled={submitting}
                      required
                    />
                    <button
                      type="button"
                      className="btn btn-outline-secondary"
                      onClick={() => setShowPassword((s) => !s)}
                      title={showPassword ? 'Hide password' : 'Show password'}
                    >
                      <i className={`bi ${showPassword ? 'bi-eye-slash' : 'bi-eye'}`}></i>
                    </button>
                  </div>
                  <div className="form-text text-muted small" style={{ fontSize: '0.75rem' }}>
                    Verified via secure SHA-256 hash in <code>.env</code>.
                  </div>
                </div>
              </div>

              <div className="modal-footer bg-light border-0 py-2 px-4 d-flex justify-content-end gap-2">
                <button
                  type="button"
                  className="btn btn-sm btn-outline-secondary"
                  onClick={closeLoginModal}
                  disabled={submitting}
                >
                  Cancel
                </button>
                <button
                  type="submit"
                  className="btn btn-sm btn-primary d-flex align-items-center gap-2 px-3"
                  disabled={submitting}
                >
                  {submitting ? (
                    <>
                      <span className="spinner-border spinner-border-sm" role="status" aria-hidden="true" />
                      Authenticating...
                    </>
                  ) : (
                    <>
                      <i className="bi bi-unlock-fill"></i>
                      Unlock Console
                    </>
                  )}
                </button>
              </div>
            </form>
          </div>
        </div>
      </div>
    </>
  );
}
