import React, { createContext, useContext, useState, useEffect, useCallback } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { loginAdmin, logoutAdmin, fetchAuthStatus, getAdminToken } from '../api';

interface AuthContextType {
  isAuthenticated: boolean;
  isLoading: boolean;
  isLoginModalOpen: boolean;
  openLoginModal: () => void;
  closeLoginModal: () => void;
  login: (password: string) => Promise<{ success: boolean; error?: string }>;
  logout: () => Promise<void>;
}

const AuthContext = createContext<AuthContextType | undefined>(undefined);

export const AuthProvider: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const [isAuthenticated, setIsAuthenticated] = useState<boolean>(() => Boolean(getAdminToken()));
  const [isLoading, setIsLoading] = useState<boolean>(true);
  const [isLoginModalOpen, setIsLoginModalOpen] = useState<boolean>(false);
  const queryClient = useQueryClient();

  useEffect(() => {
    let mounted = true;
    const check = async () => {
      const status = await fetchAuthStatus();
      if (mounted) {
        setIsAuthenticated(status.authenticated);
        setIsLoading(false);
      }
    };
    check();
    return () => {
      mounted = false;
    };
  }, []);

  const openLoginModal = useCallback(() => setIsLoginModalOpen(true), []);
  const closeLoginModal = useCallback(() => setIsLoginModalOpen(false), []);

  const login = useCallback(async (password: string) => {
    const res = await loginAdmin(password);
    if (res.success) {
      setIsAuthenticated(true);
      setIsLoginModalOpen(false);
      // Invalidate cluster, users, alerts queries so fresh authenticated data is fetched
      await queryClient.invalidateQueries();
      return { success: true };
    }
    return { success: false, error: res.error || 'Invalid admin password' };
  }, [queryClient]);

  const logout = useCallback(async () => {
    await logoutAdmin();
    setIsAuthenticated(false);
    // Invalidate queries so redacted public data is fetched immediately
    await queryClient.invalidateQueries();
  }, [queryClient]);

  return (
    <AuthContext.Provider
      value={{
        isAuthenticated,
        isLoading,
        isLoginModalOpen,
        openLoginModal,
        closeLoginModal,
        login,
        logout,
      }}
    >
      {children}
    </AuthContext.Provider>
  );
};

export function useAuth(): AuthContextType {
  const context = useContext(AuthContext);
  if (!context) {
    throw new Error('useAuth must be used within an AuthProvider');
  }
  return context;
}
