import { useRoutes } from 'react-router-dom';
import { ErrorBoundary } from '@token-zen/shared/components/ErrorBoundary';
import { routes } from './router';

function App() {
  const element = useRoutes(routes);
  return <ErrorBoundary>{element}</ErrorBoundary>;
}

export default App;
