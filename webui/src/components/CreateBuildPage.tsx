import React, { useEffect, useRef, useState } from "react";
import {
  PageSection,
  Title,
  Form,
  FormGroup,
  TextInput,
  TextArea,
  Button,
  Alert,
  Card,
  CardBody,
  Grid,
  GridItem,
  FileUpload,
  ExpandableSection,
  Flex,
  FlexItem,
  Stack,
  StackItem,
  Split,
  SplitItem,
  Badge,
  Popover,


} from "@patternfly/react-core";
import { PlusCircleIcon, TrashIcon, InfoCircleIcon } from "@patternfly/react-icons";
import { useLocation, useNavigate } from "react-router-dom";

interface TextFile {
  id: string;
  name: string;
  content: string;
}

interface UploadedFile {
  id: string;
  name: string;
  file: File;
}

interface BuildFormData {
  name: string;
  manifest: string;
  manifestFileName: string;
  distro: string;
  target: string;
  architecture: string;
  exportFormat: string;
  mode: string;
  automotiveImageBuilder: string;
  aibExtraArgs: string;
  aibOverrideArgs: string;
  serveArtifact: boolean;
}

interface BuildTemplateResponse {
  name: string;
  manifest: string;
  manifestFileName: string;
  distro: string;
  target: string;
  architecture: string;
  exportFormat: string;
  mode: string;
  automotiveImageBuilder: string;
  aibExtraArgs?: string[];
  aibOverrideArgs?: string[];
  serveArtifact: boolean;
  sourceFiles?: string[];
}

const PopoverLabel: React.FC<{ label: string; popoverContent: string; isRequired?: boolean }> = ({ label, popoverContent, isRequired }) => (
  <span>
    {label}
    {isRequired && <span style={{ color: "var(--pf-v5-global--danger-color--100)" }}> *</span>}
    <Popover
      aria-label={`${label} information`}
      bodyContent={popoverContent}
    >
      <button
        type="button"
        aria-label={`More info for ${label}`}
        style={{
          background: 'none',
          border: 'none',
          color: 'var(--pf-v5-global--Color--200)',
          marginLeft: '4px',
          cursor: 'pointer',
          fontSize: '0.875rem'
        }}
      >
        <InfoCircleIcon />
      </button>
    </Popover>
  </span>
);

const CreateBuildPage: React.FC = () => {
  const navigate = useNavigate();
  const location = useLocation();
  const [formData, setFormData] = useState<BuildFormData>({
    name: "",
    manifest: "",
    manifestFileName: "manifest.aib.yml",
    distro: "autosd",
    target: "qemu",
    architecture: "arm64",
    exportFormat: "image",
    mode: "image",
    automotiveImageBuilder:
      "quay.io/centos-sig-automotive/automotive-image-builder:1.0.0",
    aibExtraArgs: "",
    aibOverrideArgs: "",
    serveArtifact: true,
  });

  const [isSubmitting, setIsSubmitting] = useState(false);
  const [alert, setAlert] = useState<{
    type: "success" | "danger";
    message: string;
  } | null>(null);
  const [textFiles, setTextFiles] = useState<TextFile[]>([]);
  const [uploadedFiles, setUploadedFiles] = useState<UploadedFile[]>([]);
  const [isAdvancedOpen, setIsAdvancedOpen] = useState(false);
  const [expectedFiles, setExpectedFiles] = useState<string[]>([]);


  const initializedFromTemplate = useRef(false);
  useEffect(() => {
    if (initializedFromTemplate.current) return;
    let t: BuildTemplateResponse | null =
      (location as any)?.state?.template || null;
    if (!t) {
      const raw = sessionStorage.getItem("aib-template");
      if (raw) {
        try {
          t = JSON.parse(raw);
        } catch {
          t = null;
        }
        sessionStorage.removeItem("aib-template");
      }
    }
    if (!t) return;

    setFormData((prev) => ({
      ...prev,
      name: "",
      manifest: t?.manifest ?? prev.manifest,
      manifestFileName: t?.manifestFileName ?? prev.manifestFileName,
      distro: t?.distro ?? prev.distro,
      target: t?.target ?? prev.target,
      architecture: t?.architecture ?? prev.architecture,
      exportFormat: t?.exportFormat ?? prev.exportFormat,
      mode: t?.mode ?? prev.mode,
      automotiveImageBuilder:
        t?.automotiveImageBuilder ?? prev.automotiveImageBuilder,
      aibExtraArgs: (t?.aibExtraArgs ?? []).join(" "),
      aibOverrideArgs: (t?.aibOverrideArgs ?? []).join(" "),
      serveArtifact: t?.serveArtifact ?? prev.serveArtifact,
    }));

    if (t.sourceFiles && t.sourceFiles.length > 0) {
      const templateFiles = t.sourceFiles
        .map((p) => p.trim())
        .filter((p) => p.length > 0);
      setExpectedFiles(templateFiles);
    }

    initializedFromTemplate.current = true;
  }, [location]);

  const handleInputChange = (
    field: keyof BuildFormData,
    value: string | boolean,
  ) => {
    setFormData((prev) => ({ ...prev, [field]: value }));
  };

  const addTextFile = () => {
    const newFile: TextFile = {
      id: `text-${Date.now()}`,
      name: `file-${textFiles.length + 1}.txt`,
      content: "",
    };
    setTextFiles((prev) => [...prev, newFile]);
  };

  const updateTextFile = (
    id: string,
    field: "name" | "content",
    value: string,
  ) => {
    setTextFiles((prev) =>
      prev.map((file) => (file.id === id ? { ...file, [field]: value } : file)),
    );
  };

  const removeTextFile = (id: string) => {
    setTextFiles((prev) => prev.filter((file) => file.id !== id));
  };

  const handleFileUpload = (file: File) => {
    setUploadedFiles((prev) => {
      const existingIndex = prev.findIndex(
        (f) =>
          f.name === file.name &&
          f.file.size === file.size &&
          f.file.lastModified === file.lastModified,
      );

      if (existingIndex !== -1) {
        const updated = prev.slice();
        updated[existingIndex] = {
          id: updated[existingIndex].id,
          name: file.name,
          file,
        };
        return updated;
      }

      const uploadedFile: UploadedFile = {
        id: `upload-${(crypto as any).randomUUID?.() || Date.now()}`,
        name: file.name,
        file,
      };
      return [...prev, uploadedFile];
    });
  };

  const removeUploadedFile = (id: string) => {
    setUploadedFiles((prev) => prev.filter((file) => file.id !== id));
  };

  const API_BASE = (window as any).__API_BASE || "";
  const authFetch = async (input: RequestInfo | URL, init?: RequestInit) => {
    return fetch(input, { credentials: "include", ...init });
  };
  const uploadFiles = async (buildName: string) => {
    if (textFiles.length === 0 && uploadedFiles.length === 0) return;

    const sleep = (ms: number) =>
      new Promise((resolve) => setTimeout(resolve, ms));
    const maxAttempts = 60; // ~2 minutes with 2s delay
    const delayMs = 2000;

    for (let attempt = 1; attempt <= maxAttempts; attempt++) {
      try {
        const formData = new FormData();

        textFiles.forEach((textFile) => {
          const blob = new Blob([textFile.content], { type: "text/plain" });
          formData.append("file", blob, textFile.name);
        });

        uploadedFiles.forEach((uploadedFile) => {
          formData.append("file", uploadedFile.file, uploadedFile.name);
        });

        const response = await authFetch(
          `${API_BASE}/v1/builds/${buildName}/uploads`,
          {
            method: "POST",
            body: formData,
          },
        );

        if (response.ok) {
          return;
        }

        if (response.status === 503) {
          if (attempt < maxAttempts) {
            await sleep(delayMs);
            continue;
          }
          const msg = await response.text();
          throw new Error(`Upload pod not ready: ${msg}`);
        }

        const msg = await response.text();
        throw new Error(`Failed to upload files: ${response.status} ${msg}`);
      } catch (error) {
        if (attempt >= maxAttempts) {
          console.error("File upload error:", error);
          setAlert({
            type: "danger",
            message: `Error uploading files: ${error}`,
          });
          return;
        }
        await sleep(delayMs);
      }
    }
  };

  const handleSubmit = async (event: React.FormEvent) => {
    event.preventDefault();
    setIsSubmitting(true);
    setAlert(null);

    try {
      const payload = {
        name: formData.name,
        manifest: formData.manifest,
        manifestFileName: formData.manifestFileName,
        distro: formData.distro,
        target: formData.target,
        architecture: formData.architecture,
        exportFormat: formData.exportFormat,
        mode: formData.mode,
        automotiveImageBuilder: formData.automotiveImageBuilder,
        aibExtraArgs: formData.aibExtraArgs
          ? formData.aibExtraArgs.split(" ").filter((arg) => arg.trim())
          : [],
        aibOverrideArgs: formData.aibOverrideArgs
          ? formData.aibOverrideArgs.split(" ").filter((arg) => arg.trim())
          : [],
        serveArtifact: formData.serveArtifact,
      };

      const response = await authFetch(`${API_BASE}/v1/builds`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
        },
        body: JSON.stringify(payload),
      });

      if (response.ok) {
        const result = await response.json();

        if (textFiles.length > 0 || uploadedFiles.length > 0) {
          await uploadFiles(formData.name);
        }

        setAlert({
          type: "success",
          message: `Build "${result.name}" created successfully!`,
        });

        setFormData({
          name: "",
          manifest: "",
          manifestFileName: "manifest.aib.yml",
          distro: "autosd",
          target: "qemu",
          architecture: "",
          exportFormat: "image",
          mode: "image",
          automotiveImageBuilder:
            "quay.io/centos-sig-automotive/automotive-image-builder:1.0.0",
          aibExtraArgs: "",
          aibOverrideArgs: "",
          serveArtifact: true,
        });
        setTextFiles([]);
        setUploadedFiles([]);
        setExpectedFiles([]);

        setTimeout(() => {
          navigate("/builds");
        }, 2000);
      } else {
        const error = await response.text();
        setAlert({ type: "danger", message: `Error creating build: ${error}` });
      }
    } catch (error) {
      setAlert({ type: "danger", message: `Network error: ${error}` });
    } finally {
      setIsSubmitting(false);
    }
  };

  return (
    <PageSection>
      <Stack hasGutter>
        <StackItem>
          <Title headingLevel="h1" size="2xl">
            Create New Image Build
          </Title>
        </StackItem>

        {alert && (
          <StackItem>
            <Alert
              variant={alert.type}
              title={alert.message}
              isInline
            />
          </StackItem>
        )}

        <StackItem>
          <Form onSubmit={handleSubmit}>
            <Stack hasGutter>
              {/* Basic Build Information */}
              <StackItem>
                <Card>
                  <CardBody>
                    <Stack hasGutter>
                      <StackItem>
                        <Title headingLevel="h2" size="lg">
                          Basic Information
                        </Title>
                      </StackItem>
                      <StackItem>
                        <Grid hasGutter>
                          <GridItem span={6}>
                            <FormGroup 
                              label={<PopoverLabel label="Build Name" popoverContent="A unique identifier for your build" isRequired />} 
                              fieldId="name"
                            >
                              <TextInput
                                id="name"
                                value={formData.name}
                                onChange={(_event, value) =>
                                  handleInputChange("name", value)
                                }
                                placeholder="Enter a unique name for this build"
                                isRequired
                              />
                            </FormGroup>
                          </GridItem>
                          <GridItem span={12}>
                            <FormGroup
                              label={<PopoverLabel label="Manifest Content" popoverContent="YAML configuration that defines your build requirements" isRequired />}
                              fieldId="manifest"
                            >
                              <TextArea
                                id="manifest"
                                value={formData.manifest}
                                onChange={(_event, value) =>
                                  handleInputChange("manifest", value)
                                }
                                placeholder="Enter your YAML manifest content here..."
                                rows={12}
                                isRequired
                              />
                            </FormGroup>
                          </GridItem>
                        </Grid>
                      </StackItem>
                    </Stack>
                  </CardBody>
                </Card>
              </StackItem>

              {/* Build Configuration */}
              <StackItem>
                <Card>
                  <CardBody>
                    <Stack hasGutter>
                      <StackItem>
                        <Title headingLevel="h2" size="lg">
                          Build Configuration
                        </Title>
                      </StackItem>
                      <StackItem>
                        <Grid hasGutter>
                          <GridItem xl={4} lg={6} md={12}>
                            <FormGroup label={<PopoverLabel label="Distribution" popoverContent="Target distribution (e.g., autosd, cs9)" />} fieldId="distro">
                              <TextInput
                                id="distro"
                                value={formData.distro}
                                onChange={(_event, value) =>
                                  handleInputChange("distro", value)
                                }
                                placeholder="autosd"
                              />
                            </FormGroup>
                          </GridItem>

                          <GridItem xl={4} lg={6} md={12}>
                            <FormGroup label={<PopoverLabel label="Target Platform" popoverContent="target (e.g., qemu, ridesx4)" />} fieldId="target">
                              <TextInput
                                id="target"
                                value={formData.target}
                                onChange={(_event, value) =>
                                  handleInputChange("target", value)
                                }
                                placeholder="qemu"
                              />
                            </FormGroup>
                          </GridItem>

                          <GridItem xl={4} lg={6} md={12}>
                            <FormGroup label={<PopoverLabel label="Architecture" popoverContent="CPU architecture (arm64, amd64)" isRequired />} fieldId="architecture">
                              <TextInput
                                id="architecture"
                                value={formData.architecture}
                                onChange={(_event, value) =>
                                  handleInputChange("architecture", value)
                                }
                                placeholder="arm64"
                                isRequired
                              />
                            </FormGroup>
                          </GridItem>

                          <GridItem xl={6} lg={6} md={12}>
                            <FormGroup label={<PopoverLabel label="Export Format" popoverContent="format (e.g., image, qcow2)" />} fieldId="exportFormat">
                              <TextInput
                                id="exportFormat"
                                value={formData.exportFormat}
                                onChange={(_event, value) =>
                                  handleInputChange("exportFormat", value)
                                }
                                placeholder="image"
                              />
                            </FormGroup>
                          </GridItem>

                          <GridItem xl={6} lg={6} md={12}>
                            <FormGroup label={<PopoverLabel label="Build Mode" popoverContent="Build mode (image, package)" />} fieldId="mode">
                              <TextInput
                                id="mode"
                                value={formData.mode}
                                onChange={(_event, value) =>
                                  handleInputChange("mode", value)
                                }
                                placeholder="image"
                              />
                            </FormGroup>
                          </GridItem>

                          <GridItem span={12}>
                            <FormGroup
                              label={<PopoverLabel label="Automotive Image Builder Container" popoverContent="Container image used for building" />}
                              fieldId="automotiveImageBuilder"
                            >
                              <TextInput
                                id="automotiveImageBuilder"
                                value={formData.automotiveImageBuilder}
                                onChange={(_event, value) =>
                                  handleInputChange("automotiveImageBuilder", value)
                                }
                                placeholder="quay.io/centos-sig-automotive/automotive-image-builder:1.0.0"
                              />
                            </FormGroup>
                          </GridItem>
                        </Grid>
                      </StackItem>
                    </Stack>
                  </CardBody>
                </Card>
              </StackItem>

              {/* Advanced Options */}
              <StackItem>
                <Card>
                  <CardBody>
                    <ExpandableSection
                      toggleText="Advanced Options"
                      isExpanded={isAdvancedOpen}
                      onToggle={(_event, expanded) =>
                        setIsAdvancedOpen(expanded as boolean)
                      }
                    >
                      <div style={{ padding: "16px 0" }}>
                        <Grid hasGutter>
                          <GridItem span={6}>
                            <FormGroup
                              label={<PopoverLabel label="AIB Extra Arguments" popoverContent="Additional arguments for automotive-image-builder (e.g., --fusa, --define)" />}
                              fieldId="aibExtraArgs"
                            >
                              <TextInput
                                id="aibExtraArgs"
                                value={formData.aibExtraArgs}
                                onChange={(_event, value) =>
                                  handleInputChange("aibExtraArgs", value)
                                }
                                placeholder="--verbose --debug"
                              />
                            </FormGroup>
                          </GridItem>

                          <GridItem span={6}>
                            <FormGroup
                              label={<PopoverLabel label="AIB Override Arguments" popoverContent="arguments to be passed as-is to automotive-image-builder" />}
                              fieldId="aibOverrideArgs"
                            >
                              <TextInput
                                id="aibOverrideArgs"
                                value={formData.aibOverrideArgs}
                                onChange={(_event, value) =>
                                  handleInputChange("aibOverrideArgs", value)
                                }
                                placeholder="Complete override of AIB arguments"
                              />
                            </FormGroup>
                          </GridItem>
                        </Grid>
                      </div>
                    </ExpandableSection>
                  </CardBody>
                </Card>
              </StackItem>

              {/* Files Section */}
              <StackItem>
                <Card>
                  <CardBody>
                    <Stack hasGutter>
                      <StackItem>
                        <Split hasGutter>
                          <SplitItem>
                            <Title headingLevel="h2" size="lg">
                              Files
                            </Title>
                          </SplitItem>
                          <SplitItem isFilled />
                          <SplitItem>
                            <Flex spaceItems={{ default: "spaceItemsSm" }}>
                              {textFiles.length > 0 && (
                                <FlexItem>
                                  <Badge isRead>
                                    {textFiles.length} text file{textFiles.length !== 1 ? 's' : ''}
                                  </Badge>
                                </FlexItem>
                              )}
                              {uploadedFiles.length > 0 && (
                                <FlexItem>
                                  <Badge isRead>
                                    {uploadedFiles.length} uploaded file{uploadedFiles.length !== 1 ? 's' : ''}
                                  </Badge>
                                </FlexItem>
                              )}
                            </Flex>
                          </SplitItem>
                        </Split>
                      </StackItem>
                      <StackItem>
                        <p style={{ color: "var(--pf-v5-global--Color--200)", margin: 0 }}>
                          Upload files to include in your build, or create text files directly in the interface.
                        </p>
                      </StackItem>
                      {expectedFiles.length > 0 && (
                        <StackItem>
                          <div
                            style={{
                              backgroundColor: "var(--pf-v5-global--info-color--100)",
                              color: "var(--pf-v5-global--info-color--200)",
                              padding: "12px 16px",
                              borderRadius: "4px",
                              border: "1px solid var(--pf-v5-global--info-color--100)",
                              fontSize: "0.875rem"
                            }}
                          >
                            <strong>Template expects these files:</strong>
                            <ul style={{ margin: "8px 0 0 0", paddingLeft: "20px" }}>
                              {expectedFiles.map((filename, index) => (
                                <li key={index} style={{ margin: "4px 0" }}>
                                  <code style={{
                                    backgroundColor: "var(--pf-v5-global--BackgroundColor--200)",
                                    padding: "2px 6px",
                                    borderRadius: "3px",
                                    fontSize: "0.8rem"
                                  }}>
                                    {filename}
                                  </code>
                                </li>
                              ))}
                            </ul>
                            <p style={{ margin: "8px 0 0 0", fontSize: "0.8rem" }}>
                              Please upload or create these files to match the template configuration.
                            </p>
                          </div>
                        </StackItem>
                      )}
                      <StackItem>
                        <Card isPlain>
                          <CardBody>
                            <Stack hasGutter>
                              <StackItem>
                                <Title headingLevel="h3" size="md">
                                  Upload Files
                                </Title>
                              </StackItem>
                              <StackItem>
                                <FileUpload
                                  id="file-upload"
                                  type="dataURL"
                                  value=""
                                  filename=""
                                  filenamePlaceholder="Drag and drop files here or click to browse"
                                  onFileInputChange={(_event, file) => {
                                    if (file) {
                                      handleFileUpload(file);
                                    }
                                  }}
                                  browseButtonText="Browse files"
                                  clearButtonText="Clear"
                                />
                              </StackItem>
                            </Stack>
                          </CardBody>
                        </Card>
                      </StackItem>
                      <StackItem>
                        <Split hasGutter>
                          <SplitItem>
                            <p style={{ color: "var(--pf-v5-global--Color--200)", margin: 0, fontSize: "0.875rem" }}>
                              Or create text files directly in the interface:
                            </p>
                          </SplitItem>
                          <SplitItem isFilled />
                          <SplitItem>
                            <Button
                              variant="link"
                              size="sm"
                              onClick={addTextFile}
                              icon={<PlusCircleIcon />}
                            >
                              Add Text File
                            </Button>
                          </SplitItem>
                        </Split>
                      </StackItem>

                      {textFiles.length > 0 && (
                        <StackItem>
                          <Stack hasGutter>
                            <StackItem>
                              <Title headingLevel="h3" size="md">
                                Text Files
                              </Title>
                            </StackItem>
                            <StackItem>
                              <Stack hasGutter>
                                {textFiles.map((file) => (
                                  <StackItem key={file.id}>
                                    <Card>
                                      <CardBody>
                                        <Stack hasGutter>
                                          <StackItem>
                                            <Split hasGutter>
                                              <SplitItem isFilled>
                                                <FormGroup label="File Name" fieldId={`filename-${file.id}`}>
                                                  <TextInput
                                                    id={`filename-${file.id}`}
                                                    value={file.name}
                                                    onChange={(_event, value) => updateTextFile(file.id, "name", value)}
                                                    placeholder="Enter file name"
                                                  />
                                                </FormGroup>
                                              </SplitItem>
                                              <SplitItem>
                                                <Button
                                                  variant="danger"
                                                  size="sm"
                                                  onClick={() => removeTextFile(file.id)}
                                                  icon={<TrashIcon />}
                                                  style={{ marginTop: "24px" }}
                                                >
                                                  Remove
                                                </Button>
                                              </SplitItem>
                                            </Split>
                                          </StackItem>
                                          <StackItem>
                                            <FormGroup label="File Content" fieldId={`content-${file.id}`}>
                                              <TextArea
                                                id={`content-${file.id}`}
                                                value={file.content}
                                                onChange={(_event, value) => updateTextFile(file.id, "content", value)}
                                                placeholder="Enter file content"
                                                rows={8}
                                              />
                                            </FormGroup>
                                          </StackItem>
                                        </Stack>
                                      </CardBody>
                                    </Card>
                                  </StackItem>
                                ))}
                              </Stack>
                            </StackItem>
                          </Stack>
                        </StackItem>
                      )}

                      {uploadedFiles.length > 0 && (
                        <StackItem>
                          <Stack hasGutter>
                            <StackItem>
                              <Title headingLevel="h3" size="md">
                                Uploaded Files
                              </Title>
                            </StackItem>
                            <StackItem>
                              <Stack hasGutter>
                                {uploadedFiles.map((file) => (
                                  <StackItem key={file.id}>
                                    <Card isPlain>
                                      <CardBody>
                                        <Split hasGutter>
                                          <SplitItem isFilled>
                                            <Flex direction={{ default: "column" }}>
                                              <FlexItem>
                                                <strong>{file.name}</strong>
                                              </FlexItem>
                                              <FlexItem>
                                                <small>
                                                  Size: {(file.file.size / 1024).toFixed(1)} KB
                                                </small>
                                              </FlexItem>
                                            </Flex>
                                          </SplitItem>
                                          <SplitItem>
                                            <Button
                                              variant="plain"
                                              size="sm"
                                              onClick={() => removeUploadedFile(file.id)}
                                              icon={<TrashIcon />}
                                              aria-label="Remove file"
                                            />
                                          </SplitItem>
                                        </Split>
                                      </CardBody>
                                    </Card>
                                  </StackItem>
                                ))}
                              </Stack>
                            </StackItem>
                          </Stack>
                        </StackItem>
                      )}
                    </Stack>
                  </CardBody>
                </Card>
              </StackItem>

              <StackItem>
                <Card>
                  <CardBody>
                    <Split hasGutter>
                      <SplitItem>
                        <Button
                          variant="primary"
                          type="submit"
                          size="lg"
                          isLoading={isSubmitting}
                          isDisabled={!formData.name || !formData.manifest || !formData.architecture}
                        >
                          {isSubmitting ? "Creating Build..." : "Create Build"}
                        </Button>
                      </SplitItem>
                      <SplitItem>
                        <Button variant="link" onClick={() => navigate("/builds")}>
                          Cancel
                        </Button>
                      </SplitItem>
                      <SplitItem isFilled />
                      <SplitItem>
                        <small style={{ color: "var(--pf-v5-global--Color--200)" }}>
                          {!formData.name && "Build name required"}
                          {!formData.manifest && formData.name && "Manifest content required"}
                          {!formData.architecture && formData.name && formData.manifest && "Architecture required"}
                        </small>
                      </SplitItem>
                    </Split>
                  </CardBody>
                </Card>
              </StackItem>
            </Stack>
          </Form>
        </StackItem>
      </Stack>
    </PageSection>
  );
};

export default CreateBuildPage;
