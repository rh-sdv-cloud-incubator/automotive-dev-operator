import React, { useState, useEffect } from 'react';
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
  Bullseye,
  Flex,
  FlexItem,
  Gallery,
  GalleryItem,
  Divider,
  Split,
  SplitItem,
  Stack,
  StackItem,
  Label,
  Content
} from '@patternfly/react-core';
import { 
  DownloadIcon, 
  CubesIcon,
  TagIcon,
  CalendarAltIcon,
  UserIcon
} from '@patternfly/react-icons';
import { useNavigate } from 'react-router-dom';
import { authFetch, API_BASE } from '../utils/auth';

interface BuildListItem {
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
  compression?: string;
}

interface CatalogItem {
  id: string;
  name: string;
  description?: string;
  version: string;
  tags: string[];
  author?: string;
  createdAt: string;
  downloadCount: number;
  size?: string;
  architecture: string;
  distro: string;
  artifactURL?: string;
  templateURL?: string;
  thumbnailURL?: string;
}

const CatalogPage: React.FC = () => {
  const navigate = useNavigate();
  const [catalogItems, setCatalogItems] = useState<CatalogItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [selectedItem, setSelectedItem] = useState<CatalogItem | null>(null);
  const [isModalOpen, setIsModalOpen] = useState(false);
  const [downloadingItem, setDownloadingItem] = useState<string | null>(null);

  // Mock data for initial layout - will be replaced with real API calls
  const mockCatalogItems: CatalogItem[] = [
    {
      id: '1',
      name: 'Base RHEL 9 Image',
      description: 'A minimal RHEL 9 base image optimized for automotive applications',
      version: '1.0.0',
      tags: ['rhel', 'base', 'automotive'],
      author: 'Red Hat',
      createdAt: '2024-01-15T10:30:00Z',
      downloadCount: 127,
      size: '2.1 GB',
      architecture: 'x86_64',
      distro: 'rhel-9'
    },
    {
      id: '2',
      name: 'CentOS Stream 9 Development',
      description: 'Development environment with common automotive development tools',
      version: '2.1.0',
      tags: ['centos', 'development', 'tools'],
      author: 'Automotive Team',
      createdAt: '2024-01-20T14:45:00Z',
      downloadCount: 89,
      size: '3.7 GB',
      architecture: 'aarch64',
      distro: 'centos-stream-9'
    },
    {
      id: '3',
      name: 'Fedora IoT Base',
      description: 'Lightweight Fedora IoT image for edge computing applications',
      version: '1.5.2',
      tags: ['fedora', 'iot', 'edge'],
      author: 'IoT Team',
      createdAt: '2024-02-01T09:15:00Z',
      downloadCount: 203,
      size: '1.8 GB',
      architecture: 'x86_64',
      distro: 'fedora-iot'
    }
  ];

  const fetchCatalogItems = async () => {
    try {
      setLoading(true);
      setError(null);
      
      // TODO: Replace with actual API call
      // const response = await authFetch(`${API_BASE}/catalog`);
      // const data = await response.json();
      // setCatalogItems(data.items || []);
      
      // For now, use mock data
      setTimeout(() => {
        setCatalogItems(mockCatalogItems);
        setLoading(false);
      }, 1000);
      
    } catch (err) {
      console.error('Error fetching catalog items:', err);
      setError('Failed to load catalog items');
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchCatalogItems();
  }, []);

  const handleDownload = async (item: CatalogItem) => {
    try {
      setDownloadingItem(item.id);
      
      // TODO: Implement actual download logic
      console.log('Downloading item:', item.name);
      
      // Simulate download delay
      setTimeout(() => {
        setDownloadingItem(null);
        // TODO: Trigger actual download
      }, 2000);
      
    } catch (err) {
      console.error('Error downloading item:', err);
      setDownloadingItem(null);
    }
  };

  const handleUseAsTemplate = (item: CatalogItem) => {
    // Navigate to create build page with this item as a template
    navigate('/create', { 
      state: { 
        template: {
          name: item.name,
          distro: item.distro,
          architecture: item.architecture,
          templateId: item.id
        }
      }
    });
  };

  const openItemDetails = (item: CatalogItem) => {
    setSelectedItem(item);
    setIsModalOpen(true);
  };

  const closeModal = () => {
    setIsModalOpen(false);
    setSelectedItem(null);
  };

  const formatDate = (dateString: string) => {
    return new Date(dateString).toLocaleDateString('en-US', {
      year: 'numeric',
      month: 'short',
      day: 'numeric'
    });
  };

  const getBadgeColor = (tag: string): "grey" | "red" | "blue" | "purple" | "green" | "orange" | "teal" | "orangered" | "yellow" | undefined => {
    const colorMap: { [key: string]: "grey" | "red" | "blue" | "purple" | "green" | "orange" | "teal" | "orangered" | "yellow" } = {
      'rhel': 'red',
      'centos': 'blue',
      'fedora': 'purple',
      'development': 'green',
      'base': 'grey',
      'iot': 'orange',
      'automotive': 'teal'
    };
    return colorMap[tag] || 'grey';
  };

  if (loading) {
    return (
      <PageSection>
        <Bullseye>
          <Spinner size="xl" />
        </Bullseye>
      </PageSection>
    );
  }

  if (error) {
    return (
      <PageSection>
        <Alert variant="danger" title="Error loading catalog">
          {error}
        </Alert>
      </PageSection>
    );
  }

  return (
    <>
      <PageSection>
        <Stack hasGutter>
          <StackItem>
            <Title headingLevel="h1" size="2xl">
              <CubesIcon className="pf-v6-u-mr-sm" />
              Image Catalog
            </Title>
          </StackItem>
          <StackItem>
            <Content component="p">
              Browse and download published image builds to use as templates for your projects.
            </Content>
          </StackItem>
        </Stack>
      </PageSection>

      <PageSection>
        {catalogItems.length === 0 ? (
          <EmptyState>
            <CubesIcon />
            <Title headingLevel="h4" size="lg">
              No catalog items found
            </Title>
            <EmptyStateBody>
              There are no published image builds available in the catalog yet.
            </EmptyStateBody>
            <EmptyStateActions>
              <Button variant="primary" onClick={() => navigate('/create')}>
                Create New Build
              </Button>
            </EmptyStateActions>
          </EmptyState>
        ) : (
          <Gallery hasGutter minWidths={{ default: '300px' }} maxWidths={{ default: '400px' }}>
            {catalogItems.map((item) => (
              <GalleryItem key={item.id}>
                <Card 
                  onClick={() => openItemDetails(item)}
                  className="pf-m-clickable"
                >
                  <CardBody>
                    <Stack hasGutter>
                      <StackItem>
                        <Split hasGutter>
                          <SplitItem isFilled>
                            <Title headingLevel="h3" size="lg">
                              {item.name}
                            </Title>
                          </SplitItem>
                          <SplitItem>
                            <Badge>{item.version}</Badge>
                          </SplitItem>
                        </Split>
                      </StackItem>
                      
                      <StackItem>
                        <Content component="small">
                          {item.description || 'No description available'}
                        </Content>
                      </StackItem>
                      
                      <StackItem>
                        <Flex spaceItems={{ default: 'spaceItemsXs' }}>
                          {item.tags.map((tag) => (
                            <FlexItem key={tag}>
                              <Label color={getBadgeColor(tag)} icon={<TagIcon />}>
                                {tag}
                              </Label>
                            </FlexItem>
                          ))}
                        </Flex>
                      </StackItem>
                      
                      <StackItem>
                        <Split>
                          <SplitItem>
                            <Content component="small">
                              <CalendarAltIcon className="pf-v6-u-mr-xs" />
                              {formatDate(item.createdAt)}
                            </Content>
                          </SplitItem>
                          <SplitItem isFilled />
                          <SplitItem>
                            <Content component="small">
                              {item.downloadCount} downloads
                            </Content>
                          </SplitItem>
                        </Split>
                      </StackItem>
                      
                      <StackItem>
                        <Split>
                          <SplitItem>
                            <Content component="small">
                              <strong>{item.architecture}</strong>
                            </Content>
                          </SplitItem>
                          <SplitItem isFilled />
                          <SplitItem>
                            <Content component="small">
                              {item.size}
                            </Content>
                          </SplitItem>
                        </Split>
                      </StackItem>
                      
                      <Divider />
                      
                      <StackItem>
                        <Flex>
                          <FlexItem>
                            <Button
                              variant="primary"
                              size="sm"
                              onClick={(e) => {
                                e.stopPropagation();
                                handleUseAsTemplate(item);
                              }}
                            >
                              Use as Template
                            </Button>
                          </FlexItem>
                          <FlexItem>
                            <Button
                              variant="secondary"
                              size="sm"
                              icon={<DownloadIcon />}
                              isLoading={downloadingItem === item.id}
                              onClick={(e) => {
                                e.stopPropagation();
                                handleDownload(item);
                              }}
                            >
                              Download
                            </Button>
                          </FlexItem>
                        </Flex>
                      </StackItem>
                    </Stack>
                  </CardBody>
                </Card>
              </GalleryItem>
            ))}
          </Gallery>
        )}
      </PageSection>

      {/* Item Details Modal */}
      <Modal
        variant={ModalVariant.medium}
        title={selectedItem?.name || ''}
        isOpen={isModalOpen}
        onClose={closeModal}
      >
        <ModalBody>
          {selectedItem && (
            <Stack hasGutter>
              <StackItem>
                <Content component="p">
                  {selectedItem.description || 'No description available'}
                </Content>
              </StackItem>
              
              <StackItem>
                <Split hasGutter>
                  <SplitItem>
                    <strong>Version:</strong> {selectedItem.version}
                  </SplitItem>
                  <SplitItem>
                    <strong>Architecture:</strong> {selectedItem.architecture}
                  </SplitItem>
                  <SplitItem>
                    <strong>Distribution:</strong> {selectedItem.distro}
                  </SplitItem>
                </Split>
              </StackItem>
              
              <StackItem>
                <Split hasGutter>
                  <SplitItem>
                    <strong>Size:</strong> {selectedItem.size}
                  </SplitItem>
                  <SplitItem>
                    <strong>Downloads:</strong> {selectedItem.downloadCount}
                  </SplitItem>
                  <SplitItem>
                    <strong>Created:</strong> {formatDate(selectedItem.createdAt)}
                  </SplitItem>
                </Split>
              </StackItem>
              
              {selectedItem.author && (
                <StackItem>
                  <Content>
                    <UserIcon className="pf-v6-u-mr-xs" />
                    <strong>Author:</strong> {selectedItem.author}
                  </Content>
                </StackItem>
              )}
              
              <StackItem>
                <Content><strong>Tags:</strong></Content>
                <Flex spaceItems={{ default: 'spaceItemsXs' }} className="pf-v6-u-mt-xs">
                  {selectedItem.tags.map((tag) => (
                    <FlexItem key={tag}>
                      <Label color={getBadgeColor(tag)} icon={<TagIcon />}>
                        {tag}
                      </Label>
                    </FlexItem>
                  ))}
                </Flex>
              </StackItem>
            </Stack>
          )}
        </ModalBody>
        <ModalFooter>
          <Button
            variant="primary"
            onClick={() => {
              if (selectedItem) {
                handleUseAsTemplate(selectedItem);
                closeModal();
              }
            }}
          >
            Use as Template
          </Button>
          <Button
            variant="secondary"
            icon={<DownloadIcon />}
            isLoading={downloadingItem === selectedItem?.id}
            onClick={() => {
              if (selectedItem) {
                handleDownload(selectedItem);
              }
            }}
          >
            Download
          </Button>
          <Button variant="link" onClick={closeModal}>
            Close
          </Button>
        </ModalFooter>
      </Modal>
    </>
  );
};

export default CatalogPage;
