import React, { useState, useEffect, useRef } from 'react';
import {
  PageSection,
  Title,
  Card,
  CardBody,
  Button,
  Badge,
  EmptyState,
  EmptyStateActions,
  EmptyStateBody,
  Spinner,
  Alert,
  Modal,
  ModalVariant,
  ModalBody,
  ModalFooter,
  CodeBlock,
  CodeBlockCode,
  Tabs,
  Tab,
  TabTitleText,
  Bullseye,
  Flex,
  FlexItem,
  ActionGroup
} from '@patternfly/react-core';
import { Table, Thead, Tr, Th, Tbody, Td } from '@patternfly/react-table';
import { CubesIcon, DownloadIcon, EyeIcon, RedoIcon } from '@patternfly/react-icons';
import { useNavigate } from 'react-router-dom';

interface BuildItem {
  name: string;
  phase: string;
  message: string;
  requestedBy?: string;
  createdAt: string;
  startTime?: string;
  completionTime?: string;
}

interface BuildDetails {
  name: string;
  phase: string;
  message: string;
  requestedBy?: string;
  artifactURL?: string;
  artifactFileName?: string;
  startTime?: string;
  completionTime?: string;
}

interface BuildParams {
  name: string;
  manifest?: string;
  manifestFileName?: string;
  distro?: string;
  target?: string;
  architecture?: string;
  exportFormat?: string;
  mode?: string;
  automotiveImageBuilder?: string;
}

const BuildListPage: React.FC = () => {
  const navigate = useNavigate();
  const [builds, setBuilds] = useState<BuildItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [selectedBuild, setSelectedBuild] = useState<string | null>(null);
  const [buildDetails, setBuildDetails] = useState<BuildDetails | null>(null);
  const [buildParams, setBuildParams] = useState<BuildParams | null>(null);
  const [logs, setLogs] = useState<string>('');
  const [isModalOpen, setIsModalOpen] = useState(false);
  const [activeTab, setActiveTab] = useState<string | number>(0);
  const [loadingDetails, setLoadingDetails] = useState(false);
  const [loadingLogs, setLoadingLogs] = useState(false);
  const [isStreaming, setIsStreaming] = useState(false);
  const [autoRefresh, setAutoRefresh] = useState(false);
  const logContainerRef = useRef<HTMLDivElement | null>(null);
  const autoRefreshIntervalRef = useRef<NodeJS.Timeout | null>(null);
  const logsAbortRef = useRef<AbortController | null>(null);
  const lastChunkAtRef = useRef<number>(0);
  const watchdogIntervalRef = useRef<NodeJS.Timeout | null>(null);
  const lineBufferRef = useRef<string>('');
  const INACTIVITY_RESTART_SEC = 15;
  const [nowTs, setNowTs] = useState<number>(Date.now());
  const liveTimerRef = useRef<NodeJS.Timeout | null>(null);

  const scrollLogsToBottom = () => {
    requestAnimationFrame(() => {
      const el = logContainerRef.current;
      if (el) {
        el.scrollTop = el.scrollHeight;
      }
    });
  };

  useEffect(() => {
    scrollLogsToBottom();
  }, [logs]);

  useEffect(() => {
    if (isModalOpen && activeTab === 1) {
      setTimeout(scrollLogsToBottom, 0);
    }
  }, [isModalOpen, activeTab]);

  useEffect(() => {
    return () => {
      if (autoRefreshIntervalRef.current) {
        clearInterval(autoRefreshIntervalRef.current);
      }
    };
  }, []);

  // Live duration ticker while any build is running
  useEffect(() => {
    const anyRunning = builds.some(b => b.phase && b.phase.toLowerCase() === 'building');
    if (anyRunning && !liveTimerRef.current) {
      liveTimerRef.current = setInterval(() => setNowTs(Date.now()), 1000);
    } else if (!anyRunning && liveTimerRef.current) {
      clearInterval(liveTimerRef.current);
      liveTimerRef.current = null;
    }
    return () => {
      if (liveTimerRef.current) {
        clearInterval(liveTimerRef.current);
        liveTimerRef.current = null;
      }
    };
  }, [builds]);

  const formatDuration = (totalSeconds: number): string => {
    const sec = Math.max(0, Math.floor(totalSeconds));
    const hh = Math.floor(sec / 3600);
    const mm = Math.floor((sec % 3600) / 60);
    const ss = sec % 60;
    if (hh > 0) return `${hh}h ${mm}m ${ss}s`;
    return `${mm}m ${ss}s`;
  };

  const startWatchdog = (buildName: string) => {
    stopWatchdog();
    watchdogIntervalRef.current = setInterval(() => {
      const secondsSinceLastChunk = (Date.now() - lastChunkAtRef.current) / 1000;
      if ((!isStreaming || secondsSinceLastChunk > INACTIVITY_RESTART_SEC) && selectedBuild && activeTab === 1) {
        if (logsAbortRef.current) {
          try { logsAbortRef.current.abort(); } catch {}
          logsAbortRef.current = null;
        }
        fetchLogs(buildName, true);
      }
    }, 5000);
  };

  const stopWatchdog = () => {
    if (watchdogIntervalRef.current) {
      clearInterval(watchdogIntervalRef.current);
      watchdogIntervalRef.current = null;
    }
  };

  const API_BASE = (window as any).__API_BASE || '';
  const authFetch = async (input: RequestInfo | URL, init?: RequestInit) => {
    const resp = await fetch(input, {
      credentials: 'include',
      cache: 'no-store',
      keepalive: true,
      ...init,
    });
    if (resp.status === 401 || resp.status === 403) {
      const rd = encodeURIComponent(window.location.href);
      window.location.href = `${API_BASE}/oauth/start?rd=${rd}`;
      throw new Error('Redirecting to login');
    }
    return resp;
  };

  const fetchBuilds = async () => {
    try {
      setLoading(true);
      setError(null);
      const response = await authFetch(`${API_BASE}/v1/builds`);
      if (response.ok) {
        const data = await response.json();
        let list: any[] = Array.isArray(data) ? data : [];
        // Enrich missing times for completed/failed items
        const enriched = await Promise.all(
          list.map(async (b) => {
            const hasTimes = !!(b.startTime) && !!(b.completionTime);
            const phase = (b.phase || '').toLowerCase();
            if (!hasTimes && (phase === 'completed' || phase === 'failed')) {
              try {
                const r = await authFetch(`${API_BASE}/v1/builds/${encodeURIComponent(b.name)}`);
                if (r.ok) {
                  const d = await r.json();
                  return { ...b, startTime: d.startTime || b.startTime, completionTime: d.completionTime || b.completionTime };
                }
              } catch {}
            }
            return b;
          })
        );
        setBuilds(enriched as BuildItem[]);
      } else {
        throw new Error(`Failed to fetch builds: ${response.status}`);
      }
    } catch (err) {
      setError(`Error fetching builds: ${err}`);
    } finally {
      setLoading(false);
    }
  };

  const fetchBuildDetails = async (buildName: string): Promise<BuildDetails | null> => {
    try {
      setLoadingDetails(true);
      const response = await authFetch(`${API_BASE}/v1/builds/${buildName}`);
      if (response.ok) {
        const data = await response.json();
        setBuildDetails(data);
        return data;
      } else {
        throw new Error(`Failed to fetch build details: ${response.status}`);
      }
    } catch (err) {
      setError(`Error fetching build details: ${err}`);
      return null;
    } finally {
      setLoadingDetails(false);
    }
  };

  const fetchLogs = async (buildName: string, isAutoRefresh = false) => {
    const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
    try {
      if (logsAbortRef.current) {
        try { logsAbortRef.current.abort(); } catch {}
      }
      const controller = new AbortController();
      logsAbortRef.current = controller;

      if (!isAutoRefresh) {
        setLoadingLogs(true);
        setLogs('');
      }
      setIsStreaming(true);

      const maxAttempts = 60;
      const delayMs = 2000;
      let response: Response | null = null;
      for (let attempt = 1; attempt <= maxAttempts; attempt++) {
        response = await authFetch(`${API_BASE}/v1/builds/${buildName}/logs`, {
          signal: controller.signal,
          headers: { 'Accept': 'text/plain' },
        });
        if (response.ok) break;
        if (response.status === 503 && attempt < maxAttempts) {
          await sleep(delayMs);
          continue;
        }
        throw new Error(`Failed to fetch logs: ${response.status}`);
      }

      if (!response || !response.ok) {
        throw new Error('Logs not available');
      }

      const reader = response.body?.getReader();
      const decoder = new TextDecoder();
      let logContent = isAutoRefresh ? logs : '';
      let gotFirstChunk = false;

      if (reader) {
        while (true) {
          const { done, value } = await reader.read();
          if (done) {
            setIsStreaming(false);
            if (lineBufferRef.current) {
              logContent += lineBufferRef.current + "\n";
              lineBufferRef.current = '';
              setLogs(logContent);
            }
            fetchBuilds();
            if (selectedBuild) {
              fetchBuildDetails(selectedBuild);
            }
            break;
          }

          const chunk = decoder.decode(value, { stream: true });
          const combined = (lineBufferRef.current || '') + chunk;
          const parts = combined.split(/\r?\n/);
          lineBufferRef.current = parts.pop() ?? '';
          if (parts.length > 0) {
            logContent += parts.join('\n') + '\n';
            setLogs(logContent);
            scrollLogsToBottom();
          }
          lastChunkAtRef.current = Date.now();
          if (!gotFirstChunk) {
            gotFirstChunk = true;
            setLoadingLogs(false);
          }
        }
      }
    } catch (err: any) {
      if (err?.name === 'AbortError' || String(err).includes('AbortError')) {
        return;
      }
      // Transient network error: schedule a silent retry
      setTimeout(() => {
        if (selectedBuild) {
          fetchLogs(selectedBuild, true);
        }
      }, 1500);
    } finally {
      setLoadingLogs(false);
      setIsStreaming(false);
      if (logsAbortRef.current) {
        logsAbortRef.current = null;
      }
    }
  };

  const fetchBuildParams = async (buildName: string) => {
    try {
      const resp = await authFetch(`${API_BASE}/v1/builds/${buildName}/template`);
      if (!resp.ok) return;
      const tpl = await resp.json();
      setBuildParams({
        name: tpl.name,
        manifest: tpl.manifest,
        manifestFileName: tpl.manifestFileName,
        distro: tpl.distro,
        target: tpl.target,
        architecture: tpl.architecture,
        exportFormat: tpl.exportFormat,
        mode: tpl.mode,
        automotiveImageBuilder: tpl.automotiveImageBuilder,
      });
    } catch (e) {
    }
  };

  const downloadArtifact = (buildName: string) => {
    try {
      const url = `${API_BASE}/v1/builds/${buildName}/artifact`;
      window.location.href = url;
    } catch (err) {
      setError(`Error initiating download: ${err}`);
    }
  };

  const startAutoRefresh = (buildName: string) => {
    if (autoRefreshIntervalRef.current) {
      clearInterval(autoRefreshIntervalRef.current);
    }

    setAutoRefresh(true);
    autoRefreshIntervalRef.current = setInterval(async () => {
      const fresh = await fetchBuildDetails(buildName);
      if (fresh && (fresh.phase === 'Completed' || fresh.phase === 'Failed')) {
        stopAutoRefresh();
        return;
      }
      if (fresh && (fresh.phase === 'Running' || fresh.phase === 'Pending') && activeTab === 1 && !isStreaming) {
        fetchLogs(buildName, true);
      }
    }, 5000);
    startWatchdog(buildName);
  };

  const stopAutoRefresh = () => {
    if (autoRefreshIntervalRef.current) {
      clearInterval(autoRefreshIntervalRef.current);
      autoRefreshIntervalRef.current = null;
    }
    setAutoRefresh(false);
    if (logsAbortRef.current) {
      try { logsAbortRef.current.abort(); } catch {}
      logsAbortRef.current = null;
    }
    stopWatchdog();
  };

  const openBuildModal = (buildName: string) => {
    setSelectedBuild(buildName);
    setIsModalOpen(true);
    setActiveTab(0);
    fetchBuildDetails(buildName);
    fetchBuildParams(buildName);
    setTimeout(() => {
      if (buildName && !logs && !loadingLogs) {
        fetchLogs(buildName);
      }
    }, 300);
  };

  const applyBuildAsTemplate = async (buildName: string) => {
    try {
      const resp = await fetch(`/v1/builds/${buildName}/template`);
      if (!resp.ok) {
        throw new Error(`Failed to fetch template: ${resp.status}`);
      }
      const template = await resp.json();
      sessionStorage.setItem('aib-template', JSON.stringify(template));
      navigate('/', { state: { template } });
    } catch (err) {
      setError(`Error using as template: ${err}`);
    }
  };

  const closeModal = () => {
    stopAutoRefresh();
    setIsModalOpen(false);
    setSelectedBuild(null);
    setBuildDetails(null);
    setLogs('');
    setActiveTab(0);
    setIsStreaming(false);
  };

  const getPhaseVariant = (phase: string): 'blue' | 'cyan' | 'green' | 'orange' | 'purple' | 'red' | 'grey' => {
    switch (phase.toLowerCase()) {
      case 'completed':
        return 'green';
      case 'failed':
        return 'red';
      case 'building':
        return 'blue';
      default:
        return 'grey';
    }
  };

  useEffect(() => {
    fetchBuilds();
    const interval = setInterval(fetchBuilds, 5000);
    return () => clearInterval(interval);
  }, []);

  if (loading && builds.length === 0) {
    return (
      <PageSection>
        <Bullseye>
          <Spinner size="xl" />
        </Bullseye>
      </PageSection>
    );
  }

  return (
    <PageSection>
      <Flex justifyContent={{ default: 'justifyContentSpaceBetween' }} style={{ marginBottom: '24px' }}>
        <FlexItem>
          <Title headingLevel="h1" size="2xl">
            Image Builds
          </Title>
        </FlexItem>
        <FlexItem>
          <Button variant="primary" onClick={() => navigate('/')}>
            Create New Build
          </Button>
          <Button
            variant="secondary"
            onClick={fetchBuilds}
            style={{ marginLeft: '8px' }}
            icon={<RedoIcon />}
          >
            Refresh
          </Button>
        </FlexItem>
      </Flex>

      {error && (
        <Alert variant="danger" title={error} style={{ marginBottom: '24px' }} isInline />
      )}

      <Card>
        <CardBody>
          {builds.length === 0 ? (
            <EmptyState>
              <EmptyStateActions>
                <CubesIcon />
              </EmptyStateActions>
              <Title headingLevel="h4" size="lg">
                No builds found
              </Title>
              <EmptyStateBody>
                Create your first image build to get started.
              </EmptyStateBody>
              <EmptyStateActions>
                <Button variant="primary" onClick={() => navigate('/')}>
                  Create Build
                </Button>
              </EmptyStateActions>
            </EmptyState>
          ) : (
            <Table aria-label="Builds table">
              <Thead>
                <Tr>
                  <Th>Name</Th>
                  <Th>Requested By</Th>
                  <Th>Status</Th>
                  <Th>Message</Th>
                  <Th>Created</Th>
                  <Th>Duration</Th>
                  <Th>Actions</Th>
                </Tr>
              </Thead>
              <Tbody>
                {builds.map((build) => (
                  <Tr key={build.name}>
                    <Td>{build.name}</Td>
                    <Td>{build.requestedBy || '-'}</Td>
                    <Td>
                      <Badge color={getPhaseVariant(build.phase)}>
                        {build.phase}
                      </Badge>
                    </Td>
                    <Td>{build.message}</Td>
                    <Td>{new Date(build.createdAt).toLocaleString()}</Td>
                    <Td>
                      {(() => {
                        const sRaw = ((build as any).startTime as string | undefined) || build.createdAt;
                        const eRaw = (build as any).completionTime as string | undefined;
                        const phase = (build.phase || '').toLowerCase();
                        const s = new Date(sRaw).getTime();
                        const ref = eRaw ? new Date(eRaw).getTime() : (phase === 'building' ? nowTs : NaN);
                        if (!isFinite(s) || !isFinite(ref) || ref < s) return '-';
                        return formatDuration(Math.round((ref - s) / 1000));
                      })()}
                    </Td>
                    <Td>
                      <Button
                        variant="link"
                        onClick={() => openBuildModal(build.name)}
                        icon={<EyeIcon />}
                      >
                        Details
                      </Button>
                      <Button
                        variant="secondary"
                        onClick={() => applyBuildAsTemplate(build.name)}
                        style={{ marginLeft: '8px' }}
                      >
                        Use as template
                      </Button>
                      {build.phase === 'Completed' && (
                        <Button
                          variant="secondary"
                          onClick={() => downloadArtifact(build.name)}
                          icon={<DownloadIcon />}
                          style={{ marginLeft: '8px' }}
                        >
                          Download
                        </Button>
                      )}
                    </Td>
                  </Tr>
                ))}
              </Tbody>
            </Table>
          )}
        </CardBody>
      </Card>

      <Modal
        variant={ModalVariant.large}
        title={`Build: ${selectedBuild}`}
        isOpen={isModalOpen}
        onClose={closeModal}
      >
        <ModalBody>
          <Tabs activeKey={activeTab} onSelect={(_event, tabIndex) => setActiveTab(tabIndex)}>
            <Tab eventKey={0} title={<TabTitleText>Details</TabTitleText>}>
              {loadingDetails ? (
                <Bullseye style={{ height: '200px' }}>
                  <Spinner />
                </Bullseye>
              ) : buildDetails ? (
                <div style={{ padding: '16px 0' }}>
                  <dl style={{ display: 'grid', gridTemplateColumns: 'auto 1fr', gap: '8px 16px' }}>
                    <dt><strong>Name:</strong></dt>
                    <dd>{buildDetails.name}</dd>
                    <dt><strong>Requested By:</strong></dt>
                    <dd>{buildDetails.requestedBy || '-'}</dd>
                    <dt><strong>Status:</strong></dt>
                    <dd>
                      <Badge color={getPhaseVariant(buildDetails.phase)}>
                        {buildDetails.phase}
                      </Badge>
                    </dd>
                    <dt><strong>Message:</strong></dt>
                    <dd>{buildDetails.message}</dd>
                    {buildDetails.startTime && (
                      <>
                        <dt><strong>Started:</strong></dt>
                        <dd>{new Date(buildDetails.startTime).toLocaleString()}</dd>
                      </>
                    )}
                    {buildDetails.completionTime && (
                      <>
                        <dt><strong>Completed:</strong></dt>
                        <dd>{new Date(buildDetails.completionTime).toLocaleString()}</dd>
                        <dt><strong>Duration:</strong></dt>
                        <dd>{(() => {
                          const s = new Date(buildDetails.startTime || '').getTime();
                          const e = new Date(buildDetails.completionTime || '').getTime();
                          if (!isFinite(s) || !isFinite(e) || e < s) return '-';
                          const total = Math.floor((e - s) / 1000);
                          const hh = Math.floor(total / 3600);
                          const mm = Math.floor((total % 3600) / 60);
                          const ss = total % 60;
                          return hh > 0 ? `${hh}h ${mm}m ${ss}s` : `${mm}m ${ss}s`;
                        })()}</dd>
                      </>
                    )}
                    {buildParams && (
                      <>
                        <dt><strong>Distro:</strong></dt>
                        <dd>{buildParams.distro || '-'}</dd>
                        <dt><strong>Target:</strong></dt>
                        <dd>{buildParams.target || '-'}</dd>
                        <dt><strong>Architecture:</strong></dt>
                        <dd>{buildParams.architecture || '-'}</dd>
                        <dt><strong>Export format:</strong></dt>
                        <dd>{buildParams.exportFormat || '-'}</dd>
                        <dt><strong>Mode:</strong></dt>
                        <dd>{buildParams.mode || '-'}</dd>
                        <dt><strong>Image Builder:</strong></dt>
                        <dd style={{ wordBreak: 'break-all' }}>{buildParams.automotiveImageBuilder || '-'}</dd>
                      </>
                    )}
                    {buildDetails.artifactURL && (
                      <>
                        <dt><strong>Artifact URL:</strong></dt>
                        <dd>{buildDetails.artifactURL}</dd>
                      </>
                    )}
                    {buildDetails.artifactFileName && (
                      <>
                        <dt><strong>Artifact File:</strong></dt>
                        <dd>{buildDetails.artifactFileName}</dd>
                      </>
                    )}
                  </dl>

                  {buildDetails.phase === 'Completed' && selectedBuild && (
                    <ActionGroup style={{ marginTop: '24px' }}>
                      <Button
                        variant="secondary"
                        onClick={() => downloadArtifact(selectedBuild)}
                        icon={<DownloadIcon />}
                      >
                        Download
                      </Button>
                    </ActionGroup>
                  )}
                </div>
              ) : null}
            </Tab>
            <Tab 
              eventKey={1} 
              title={<TabTitleText>Logs</TabTitleText>}
              onSelect={() => {
                if (selectedBuild && buildDetails && (buildDetails.phase === 'Running' || buildDetails.phase === 'Pending')) {
                  startAutoRefresh(selectedBuild);
                  if (!isStreaming) {
                    fetchLogs(selectedBuild);
                  }
                }
              }}
            >
              {loadingLogs ? (
                <Bullseye style={{ height: '200px' }}>
                  <Spinner />
                </Bullseye>
              ) : (
                <div style={{ padding: '16px 0' }}>
                  <Flex style={{ marginBottom: '16px' }}>
                    <FlexItem>
                      <Button
                        variant="secondary"
                        onClick={() => selectedBuild && fetchLogs(selectedBuild)}
                        icon={<RedoIcon />}
                        isDisabled={false}
                      >
                        Refresh Logs
                      </Button>
                    </FlexItem>
                    <FlexItem>
                      {isStreaming && (
                        <Badge isRead>
                          <Spinner size="sm" style={{ marginRight: '8px' }} />
                          Streaming...
                        </Badge>
                      )}
                      {autoRefresh && (
                        <Badge style={{ marginLeft: '8px' }}>
                          Auto-refresh enabled
                        </Badge>
                      )}
                    </FlexItem>
                  </Flex>
                  <div
                    ref={logContainerRef}
                    style={{ maxHeight: '60vh', overflowY: 'auto', border: '1px solid #d2d2d2', borderRadius: 4 }}
                  >
                    <CodeBlock>
                      <CodeBlockCode>
                        {logs || 'No logs available'}
                      </CodeBlockCode>
                    </CodeBlock>
                  </div>
                </div>
              )}
            </Tab>
          </Tabs>
        </ModalBody>
        <ModalFooter>
          <Button variant="primary" onClick={closeModal}>
            Close
          </Button>
        </ModalFooter>
      </Modal>
    </PageSection>
  );
};

export default BuildListPage;