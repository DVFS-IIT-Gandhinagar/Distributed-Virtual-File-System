import { Routes, Route } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { fetchCluster } from './api';
import AppNavbar from './components/Navbar';
import Overview from './pages/Overview';
import Nodes from './pages/Nodes';
import Users from './pages/Users';

export default function App() {
  const { data, dataUpdatedAt } = useQuery({
    queryKey: ['cluster'],
    queryFn: fetchCluster,
    refetchInterval: 5000,
  });

  return (
    <div className="d-flex flex-column min-vh-100" style={{ backgroundColor: '#f8f9fa' }}>
      <AppNavbar cluster={data} lastUpdated={dataUpdatedAt} />
      <main className="flex-grow-1">
        <Routes>
          <Route path="/" element={<Overview />} />
          <Route path="/nodes" element={<Nodes />} />
          <Route path="/users" element={<Users />} />
        </Routes>
      </main>
      <footer className="py-3 text-center text-muted small border-top bg-white">
        DVFS Admin Console &mdash; Distributed Virtual File System
      </footer>
    </div>
  );
}
