import { Outlet, Link, useLocation } from 'react-router-dom';
import {
  Masthead,
  MastheadBrand,
  MastheadContent,
  MastheadMain,
  Page,
  PageSidebar,
  Nav,
  NavList,
  NavItem,
} from '@patternfly/react-core';

export function RootLayout() {
  const location = useLocation();
  const path = location.pathname;

  const sidebar = (
    <PageSidebar>
      <Nav>
        <NavList>
          <NavItem isActive={path === '/' || path.startsWith('/builds')} itemId="builds">
            <Link to="/builds">Builds</Link>
          </NavItem>
          <NavItem isActive={path.startsWith('/new')} itemId="new">
            <Link to="/new">New build</Link>
          </NavItem>
        </NavList>
      </Nav>
    </PageSidebar>
  );

  const header = (
    <Masthead>
      <MastheadMain>
        <MastheadBrand>
          <span style={{ fontWeight: 600, fontSize: 18 }}>Automotive Build UI</span>
        </MastheadBrand>
      </MastheadMain>
      <MastheadContent />
    </Masthead>
  );

  return (
    <Page header={header} sidebar={sidebar} isManagedSidebar>
      <Outlet />
    </Page>
  );
}


