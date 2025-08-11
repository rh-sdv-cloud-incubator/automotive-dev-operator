import { useEffect, useMemo, useRef, useState } from 'react';
import { useParams } from 'react-router-dom';
import { getBuild, streamLogs, type BuildResponse } from '@/api/client';
import { PageSection, Title, Button, CodeBlock, CodeBlockCode } from '@patternfly/react-core';

export function BuildDetailsPage() {
  const { name = '' } = useParams();
  const [build, setBuild] = useState<BuildResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [logs, setLogs] = useState<string>('');
  const abortRef = useRef<AbortController | null>(null);

  useEffect(() => {
    let timer: number | undefined;
    const fetchOnce = () => getBuild(name).then(setBuild).catch((e) => setError(String(e)));
    fetchOnce();
    timer = window.setInterval(fetchOnce, 3000);
    return () => {
      if (timer) window.clearInterval(timer);
    };
  }, [name]);

  useEffect(() => {
    if (!name) return;
    const ac = new AbortController();
    abortRef.current = ac;
    setLogs('');
    streamLogs(name, (t) => setLogs((prev) => prev + t), ac.signal);
    return () => {
      // Abort quietly; errors are ignored in client
      try {
        ac.abort();
      } catch {
        // ignore
      }
    };
  }, [name]);

  const canDownload = useMemo(() => build?.phase === 'Completed', [build]);

  if (error) {
    return (
      <PageSection>
        <Title headingLevel="h1">{name}</Title>
        <p>{error}</p>
      </PageSection>
    );
  }

  return (
    <PageSection>
      <Title headingLevel="h1">{name}</Title>
      {build && (
        <div style={{ margin: '12px 0' }}>
          <div>Phase: <strong>{build.phase}</strong></div>
          <div style={{ marginTop: 8 }}>
            <Button
              isDisabled={!canDownload}
              component="a"
              href={`/v1/builds/${encodeURIComponent(name)}/artifact`}
            >
              Download artifact
            </Button>
          </div>
        </div>
      )}

      <Title headingLevel="h2" style={{ marginTop: 16 }}>Logs</Title>
      <CodeBlock>
        <CodeBlockCode>
          {logs || 'Waiting for logs...'}
        </CodeBlockCode>
      </CodeBlock>
    </PageSection>
  );
}


