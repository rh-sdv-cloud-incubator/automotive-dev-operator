import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { listBuilds, type BuildListItem } from '@/api/client';
import { PageSection, Title, Spinner, Bullseye, EmptyState, EmptyStateBody, Button } from '@patternfly/react-core';
import { Table, Thead, Tbody, Tr, Th, Td } from '@patternfly/react-table';

export function BuildsPage() {
  const [items, setItems] = useState<BuildListItem[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    listBuilds().then(setItems).catch((e) => setError(String(e)));
  }, []);

  if (error) {
    return (
      <PageSection>
        <Title headingLevel="h1">Builds</Title>
        <p>{error}</p>
      </PageSection>
    );
  }

  if (!items) {
    return (
      <Bullseye>
        <Spinner size="xl" />
      </Bullseye>
    );
  }

  if (items.length === 0) {
    return (
      <PageSection>
        <EmptyState headingLevel="h2" titleText="No builds yet">
          <EmptyStateBody>Get started by creating your first build.</EmptyStateBody>
          <Button variant="primary" component={(props) => <Link to="/new" {...props} />}>New build</Button>
        </EmptyState>
      </PageSection>
    );
  }

  return (
    <PageSection>
      <Title headingLevel="h1">Builds</Title>
      <div style={{ marginTop: 16 }}>
        <Table aria-label="Builds table" variant="compact">
          <Thead>
            <Tr>
              <Th>Name</Th>
              <Th>Phase</Th>
              <Th>Message</Th>
              <Th>Created</Th>
            </Tr>
          </Thead>
          <Tbody>
            {items.map((b) => (
              <Tr key={b.name}>
                <Td dataLabel="Name">
                  <Link to={`/builds/${encodeURIComponent(b.name)}`}>{b.name}</Link>
                </Td>
                <Td dataLabel="Phase">{b.phase}</Td>
                <Td dataLabel="Message">{b.message}</Td>
                <Td dataLabel="Created">{new Date(b.createdAt).toLocaleString()}</Td>
              </Tr>
            ))}
          </Tbody>
        </Table>
      </div>
    </PageSection>
  );
}


