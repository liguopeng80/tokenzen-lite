import { useEffect } from 'react';
import { useRoutes } from 'react-router-dom';
import { ErrorBoundary } from '@token-zen/shared/components/ErrorBoundary';
import { useSiteStore } from '@/stores/site';
import { routes } from './router';

function App() {
  const element = useRoutes(routes);
  const fetchConfig = useSiteStore((s) => s.fetchConfig);

  useEffect(() => {
    fetchConfig();
  }, [fetchConfig]);

  return <ErrorBoundary>{element}</ErrorBoundary>;
}

export default App;
