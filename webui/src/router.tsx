import { createBrowserRouter } from 'react-router-dom';
import { RootLayout } from './ui/RootLayout';
import { BuildsPage } from './pages/BuildsPage';
import { BuildDetailsPage } from './pages/BuildDetailsPage';
import { NewBuildPage } from './pages/NewBuildPage';

export const router = createBrowserRouter([
  {
    path: '/',
    element: <RootLayout />,
    children: [
      { index: true, element: <BuildsPage /> },
      { path: 'builds', element: <BuildsPage /> },
      { path: 'builds/:name', element: <BuildDetailsPage /> },
      { path: 'new', element: <NewBuildPage /> },
    ],
  },
]);


