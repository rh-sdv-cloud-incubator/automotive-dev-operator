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
  ActionGroup,
  Divider,
  FileUpload,
  ExpandableSection,
  List,
  ListItem,
  Tab,
  Tabs,
  TabTitleText,
  Switch,
} from "@patternfly/react-core";
import { PlusCircleIcon, TrashIcon, UploadIcon } from "@patternfly/react-icons";
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
  const [activeFileTab, setActiveFileTab] = useState<string | number>(0);
  const [isAdvancedOpen, setIsAdvancedOpen] = useState(false);

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
      setTextFiles((prev) => {
        const existing = new Set(prev.map((f) => f.name));
        const seenIncoming = new Set<string>();
        const toAdd = t!
          .sourceFiles!.map((p) => p.trim())
          .filter((p) => p.length > 0)
          // de-duplicate within incoming list and against existing entries
          .filter((p) => {
            if (existing.has(p) || seenIncoming.has(p)) return false;
            seenIncoming.add(p);
            return true;
          })
          .map(
            (p) =>
              ({
                id: `text-${crypto.randomUUID?.() || Date.now()}`,
                name: p,
                content: "",
              }) as TextFile,
          );
        return [...prev, ...toAdd];
      });
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
          architecture: "arm64",
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

        // Navigate to builds list after 2 seconds
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
      <Title headingLevel="h1" size="2xl" style={{ marginBottom: "24px" }}>
        Create New Image Build
      </Title>

      {alert && (
        <Alert
          variant={alert.type}
          title={alert.message}
          style={{ marginBottom: "24px" }}
          isInline
        />
      )}

      <Card>
        <CardBody>
          <Form onSubmit={handleSubmit}>
            <Grid hasGutter>
              <GridItem span={6}>
                <FormGroup label="Build Name" isRequired fieldId="name">
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

              <GridItem span={6}>
                <FormGroup
                  label="Manifest File Name"
                  fieldId="manifestFileName"
                >
                  <TextInput
                    id="manifestFileName"
                    value={formData.manifestFileName}
                    onChange={(_event, value) =>
                      handleInputChange("manifestFileName", value)
                    }
                    placeholder="manifest.aib.yml"
                  />
                </FormGroup>
              </GridItem>

              <GridItem span={12}>
                <FormGroup
                  label="Manifest Content"
                  isRequired
                  fieldId="manifest"
                >
                  <TextArea
                    id="manifest"
                    value={formData.manifest}
                    onChange={(_event, value) =>
                      handleInputChange("manifest", value)
                    }
                    placeholder="Enter your YAML manifest content here..."
                    rows={10}
                    isRequired
                  />
                </FormGroup>
              </GridItem>

              <GridItem span={12}>
                <Divider style={{ margin: "24px 0" }} />
                <Title
                  headingLevel="h2"
                  size="lg"
                  style={{ marginBottom: "16px" }}
                >
                  Build Configuration
                </Title>
              </GridItem>

              <GridItem span={4}>
                <FormGroup label="Distribution" fieldId="distro">
                  <TextInput
                    id="distro"
                    value={formData.distro}
                    onChange={(_event, value) =>
                      handleInputChange("distro", value)
                    }
                    placeholder="cs9"
                  />
                </FormGroup>
              </GridItem>

              <GridItem span={4}>
                <FormGroup label="Target" fieldId="target">
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

              <GridItem span={4}>
                <FormGroup label="Architecture" fieldId="architecture">
                  <TextInput
                    id="architecture"
                    value={formData.architecture}
                    onChange={(_event, value) =>
                      handleInputChange("architecture", value)
                    }
                    placeholder="arm64"
                  />
                </FormGroup>
              </GridItem>

              <GridItem span={6}>
                <FormGroup label="Export Format" fieldId="exportFormat">
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

              <GridItem span={6}>
                <FormGroup label="Mode" fieldId="mode">
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
                  label="Automotive Image Builder Container"
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

              <GridItem span={12}>
                <Divider style={{ margin: "24px 0" }} />
                <ExpandableSection
                  toggleText="Advanced options"
                  isExpanded={isAdvancedOpen}
                  onToggle={(_event, expanded) =>
                    setIsAdvancedOpen(expanded as boolean)
                  }
                >
                  <Grid hasGutter>
                    <GridItem span={6}>
                      <FormGroup
                        label="AIB Extra Arguments"
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
                        label="AIB Override Arguments"
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
                </ExpandableSection>
              </GridItem>
            </Grid>

            <Divider style={{ margin: "24px 0" }} />
            <Title headingLevel="h2" size="lg" style={{ marginBottom: "16px" }}>
              Files
            </Title>

            <ExpandableSection toggleText="File Management" isExpanded>
              <Tabs
                activeKey={activeFileTab}
                onSelect={(_event, tabIndex) => setActiveFileTab(tabIndex)}
              >
                <Tab
                  eventKey={0}
                  title={<TabTitleText>Text Files</TabTitleText>}
                >
                  <div style={{ padding: "16px 0" }}>
                    <Button
                      variant="secondary"
                      onClick={addTextFile}
                      icon={<PlusCircleIcon />}
                      style={{ marginBottom: "16px" }}
                    >
                      Add Text File
                    </Button>

                    {textFiles.length === 0 ? (
                      <div
                        style={{
                          textAlign: "center",
                          padding: "20px",
                          color: "#6A6E73",
                        }}
                      >
                        No text files added. Click "Add Text File" to create a
                        new file.
                      </div>
                    ) : (
                      <List>
                        {textFiles.map((file) => (
                          <ListItem key={file.id}>
                            <Card style={{ marginBottom: "16px" }}>
                              <CardBody>
                                <Grid hasGutter>
                                  <GridItem span={10}>
                                    <FormGroup
                                      label="File Name"
                                      fieldId={`filename-${file.id}`}
                                    >
                                      <TextInput
                                        id={`filename-${file.id}`}
                                        value={file.name}
                                        onChange={(_event, value) =>
                                          updateTextFile(file.id, "name", value)
                                        }
                                        placeholder="Enter file name"
                                      />
                                    </FormGroup>
                                  </GridItem>
                                  <GridItem span={2}>
                                    <Button
                                      variant="danger"
                                      onClick={() => removeTextFile(file.id)}
                                      icon={<TrashIcon />}
                                      style={{ marginTop: "24px" }}
                                    >
                                      Remove
                                    </Button>
                                  </GridItem>
                                  <GridItem span={12}>
                                    <FormGroup
                                      label="File Content"
                                      fieldId={`content-${file.id}`}
                                    >
                                      <TextArea
                                        id={`content-${file.id}`}
                                        value={file.content}
                                        onChange={(_event, value) =>
                                          updateTextFile(
                                            file.id,
                                            "content",
                                            value,
                                          )
                                        }
                                        placeholder="Enter file content"
                                        rows={8}
                                      />
                                    </FormGroup>
                                  </GridItem>
                                </Grid>
                              </CardBody>
                            </Card>
                          </ListItem>
                        ))}
                      </List>
                    )}
                  </div>
                </Tab>

                <Tab
                  eventKey={1}
                  title={<TabTitleText>File Uploads</TabTitleText>}
                >
                  <div style={{ padding: "16px 0" }}>
                    <FormGroup label="Upload Files" fieldId="file-upload">
                      <FileUpload
                        id="file-upload"
                        type="dataURL"
                        value=""
                        filename=""
                        filenamePlaceholder="Choose file to upload"
                        onFileInputChange={(_event, file) => {
                          if (file) {
                            handleFileUpload(file);
                          }
                        }}
                        browseButtonText="Choose file"
                        clearButtonText="Clear"
                      />
                    </FormGroup>

                    {uploadedFiles.length === 0 ? (
                      <div
                        style={{
                          textAlign: "center",
                          padding: "20px",
                          color: "#6A6E73",
                        }}
                      >
                        No files uploaded. Use the file browser above to select
                        files.
                      </div>
                    ) : (
                      <div style={{ marginTop: "16px" }}>
                        <Title
                          headingLevel="h4"
                          size="md"
                          style={{ marginBottom: "12px" }}
                        >
                          Uploaded Files
                        </Title>
                        <List>
                          {uploadedFiles.map((file) => (
                            <ListItem key={file.id}>
                              <div
                                style={{
                                  display: "flex",
                                  justifyContent: "space-between",
                                  alignItems: "center",
                                  padding: "8px",
                                }}
                              >
                                <span>
                                  <UploadIcon style={{ marginRight: "8px" }} />
                                  {file.name} (
                                  {(file.file.size / 1024).toFixed(1)} KB)
                                </span>
                                <Button
                                  variant="plain"
                                  onClick={() => removeUploadedFile(file.id)}
                                  icon={<TrashIcon />}
                                  aria-label="Remove file"
                                />
                              </div>
                            </ListItem>
                          ))}
                        </List>
                      </div>
                    )}
                  </div>
                </Tab>
              </Tabs>
            </ExpandableSection>

            <ActionGroup style={{ marginTop: "32px" }}>
              <Button
                variant="primary"
                type="submit"
                isLoading={isSubmitting}
                isDisabled={!formData.name || !formData.manifest}
              >
                {isSubmitting ? "Creating Build..." : "Create Build"}
              </Button>
              <Button variant="link" onClick={() => navigate("/builds")}>
                Cancel
              </Button>
            </ActionGroup>
          </Form>
        </CardBody>
      </Card>
    </PageSection>
  );
};

export default CreateBuildPage;
