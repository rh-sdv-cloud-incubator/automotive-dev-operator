import React from 'react';
import { BrowserRouter as Router, Routes, Route } from 'react-router-dom';
import {
  Page,
  PageSidebar,
  Nav,
  NavList,
  NavItem,
  Brand,
  PageSidebarBody,
  Masthead,
  MastheadMain,
  MastheadBrand,
  MastheadToggle,
  PageToggleButton
} from '@patternfly/react-core';
import { CubesIcon, ListIcon, BarsIcon } from '@patternfly/react-icons';
import CreateBuildPage from './components/CreateBuildPage';
import BuildListPage from './components/BuildListPage';

const App: React.FC = () => {
  const [isSidebarOpen, setIsSidebarOpen] = React.useState(true);

  const onSidebarToggle = () => {
    setIsSidebarOpen(!isSidebarOpen);
  };

  const masthead = (
    <Masthead>
      <MastheadToggle>
        <PageToggleButton
          variant="plain"
          aria-label="Global navigation"
          isSidebarOpen={isSidebarOpen}
          onSidebarToggle={onSidebarToggle}
        >
          <BarsIcon />
        </PageToggleButton>
      </MastheadToggle>
      <MastheadMain>
        <MastheadBrand>
            AIB UI
        </MastheadBrand>
      </MastheadMain>
    </Masthead>
  );

  const Navigation = (
    <Nav>
      <NavList>
        <NavItem to="/" isActive={window.location.pathname === '/'}>
          <CubesIcon /> Create Build
        </NavItem>
        <NavItem to="/builds" isActive={window.location.pathname === '/builds'}>
          <ListIcon /> Build List
        </NavItem>
      </NavList>
    </Nav>
  );

  const sidebar = (
    <PageSidebar isSidebarOpen={isSidebarOpen}>
      <PageSidebarBody>
        {Navigation}
      </PageSidebarBody>
    </PageSidebar>
  );

  return (
    <Router>
      <Page masthead={masthead} sidebar={sidebar}>
        <Routes>
          <Route path="/" element={<CreateBuildPage />} />
          <Route path="/builds" element={<BuildListPage />} />
        </Routes>
      </Page>
    </Router>
  );
};

export default App;