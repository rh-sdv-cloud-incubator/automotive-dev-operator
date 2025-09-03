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
import { authFetch, API_BASE } from "../utils/auth";

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
  envSecretRef: string;
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
  envSecretRef?: string;
}

const BUILD_MODE_OPTIONS = [
  { value: "image", label: "image" },
  { value: "package", label: "package" },
];

const ARCHITECTURE_OPTIONS = [
  { value: "arm64", label: "arm64 (aarch64)" },
  { value: "amd64", label: "amd64 (x86_64)" },
];

const DISTRO_OPTIONS = [
  { value: "autosd", label: "autosd - Alias of 'autosd9'" },
  { value: "autosd10", label: "autosd10 - AutoSD 9 - based on nightly autosd compose" },
  { value: "autosd10-latest-sig", label: "autosd10-latest-sig - AutoSD 10 - latest cs10 and autosd repos, plus the automotive sig community packages" },
  { value: "autosd10-sig", label: "autosd10-sig - AutoSD 9 - based on nightly autosd compose, plus the automotive sig community packages" },
  { value: "autosd9", label: "autosd9 - AutoSD 9 - based on nightly autosd compose" },
  { value: "autosd9-latest-sig", label: "autosd9-latest-sig - AutoSD 9 - latest cs9 and autosd repos, plus the automotive sig community packages" },
  { value: "autosd9-sig", label: "autosd9-sig - AutoSD 9 - based on nightly autosd compose, plus the automotive sig community packages" },
  { value: "cs9", label: "cs9 - Alias of 'autosd9-latest-sig'" },
  { value: "eln", label: "eln - Fedora ELN" },
  { value: "f40", label: "f40 - Fedora 40" },
  { value: "f41", label: "f41 - Fedora 41" },
  { value: "rhivos", label: "rhivos - Alias of 'rhivos1'" },
  { value: "rhivos1", label: "rhivos1 - RHIVOS 1" },
];

const TARGET_OPTIONS = [
  { value: "_abootqemu", label: "_abootqemu - Implementation of abootqemu.ipp.yml but loaded after derived target" },
  { value: "_abootqemukvm", label: "_abootqemukvm - Implementation of abootqemukvm.ipp.yml but loaded after derived target" },
  { value: "_ridesx4_r3", label: "_ridesx4_r3 - Implementation of ridesx4_r3.ipp.yml but loaded after derived target" },
  { value: "_ridesx4_scmi", label: "_ridesx4_scmi - Implementation of ridesx4_scmi.ipp.yml, but loaded after derived target" },
  { value: "abootqemu", label: "abootqemu - This is a qemu target similar to the regular \"qemu\" target, but using an android boot partition instead of grub." },
  { value: "abootqemukvm", label: "abootqemukvm - This is a qemu target similar to the \"abootqemu\", but using a dtb that enables kvm." },
  { value: "acrn", label: "acrn" },
  { value: "am62sk", label: "am62sk - Target for the TI SK-AM62 Evaluation Board" },
  { value: "am69sk", label: "am69sk - Target for the TI SK-AM69 Evaluation Board" },
  { value: "aws", label: "aws" },
  { value: "azure", label: "azure - Target for Azure images" },
  { value: "beagleplay", label: "beagleplay - Target for the TI BeaglePlay Board" },
  { value: "ccimx93dvk", label: "ccimx93dvk" },
  { value: "imx8qxp_mek", label: "imx8qxp_mek - Target for the Multisensory Enablement Kit i.MX 8QuadXPlus MEK CPU Board" },
  { value: "j784s4evm", label: "j784s4evm - Target for the TI J784S4XEVM Evaluation Board" },
  { value: "pc", label: "pc" },
  { value: "qdrive3", label: "qdrive3" },
  { value: "qemu", label: "qemu - Target for general virtualized images (typically for qemu)" },
  { value: "rcar_s4", label: "rcar_s4 - Target for Renesas R-Car S4 with stock functionality" },
  { value: "rcar_s4_can", label: "rcar_s4_can - Target for Renesas R-Car S4 with CAN bus enablement, requires CAN unlock app running on G4MH cores to boot" },
  { value: "ridesx4", label: "ridesx4 - A target for the QC RIDESX4 board." },
  { value: "ridesx4_r3", label: "ridesx4_r3 - A target for the QC RIDESX4 board, Rev 3." },
  { value: "ridesx4_scmi", label: "ridesx4_scmi - A target for the QC RIDESX4 board flashed with recent firmware using SCMI for drivers resources." },
  { value: "rpi4", label: "rpi4 - Target for the Raspberry Pi 4" },
  { value: "s32g_vnp_rdb3", label: "s32g_vnp_rdb3 - Target for the NXP S32G3 Vehicle Networking Reference Design Board" },
  { value: "tda4vm_sk", label: "tda4vm_sk - Target for the TI SK-TDA4VM Evaluation Board" },
];

const EXPORT_FORMAT_OPTIONS = [
  { value: "aboot", label: "aboot - Aboot image" },
  { value: "aboot.simg", label: "aboot.simg - Aboot image in simg format" },
  { value: "bootc", label: "bootc - Bootc image in local store" },
  { value: "bootc-archive", label: "bootc-archive - Bootc OCI image archive" },
  { value: "container", label: "container - Container image" },
  { value: "ext4", label: "ext4 - Ext4 filesystem image without partitions" },
  { value: "ext4.simg", label: "ext4.simg - Ext4 filesystem partition in simg format" },
  { value: "image", label: "image - Raw disk image" },
  { value: "ostree-commit", label: "ostree-commit - OSTree repo containing a commit" },
  { value: "qcow2", label: "qcow2 - Disk image in qcow2 format" },
  { value: "rootfs", label: "rootfs - Directory with image rootfs files" },
  { value: "rpmlist", label: "rpmlist - List of rpms that are in the image" },
  { value: "simg", label: "simg - Partitioned image in simg format" },
  { value: "tar", label: "tar - Tar archive with files from rootfs" },
];

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
    distro: "",
    target: "",
    architecture: "",
    exportFormat: "",
    mode: "",
    automotiveImageBuilder:
      "quay.io/centos-sig-automotive/automotive-image-builder:1.0.0",
    aibExtraArgs: "",
    aibOverrideArgs: "",
    serveArtifact: true,
    envSecretRef: "",
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
      envSecretRef: t?.envSecretRef ?? prev.envSecretRef,
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
        envSecretRef: formData.envSecretRef,
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
          distro: "",
          target: "",
          architecture: "",
          exportFormat: "",
          mode: "",
          automotiveImageBuilder:
            "quay.io/centos-sig-automotive/automotive-image-builder:1.0.0",
          aibExtraArgs: "",
          aibOverrideArgs: "",
          serveArtifact: true,
          envSecretRef: "",
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
                                placeholder="Select or type a distribution..."
                                list="distro-options"
                              />
                              <datalist id="distro-options">
                                {DISTRO_OPTIONS.map((option, index) => (
                                  <option key={index} value={option.value} title={option.label.split(' - ')[1]} />
                                ))}
                              </datalist>
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
                                placeholder="Select or type a target platform..."
                                list="target-options"
                              />
                              <datalist id="target-options">
                                {TARGET_OPTIONS.map((option, index) => (
                                  <option key={index} value={option.value} title={option.label.split(' - ')[1]} />
                                ))}
                              </datalist>
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
                                placeholder="Select or type an architecture..."
                                list="architecture-options"
                                isRequired
                              />
                              <datalist id="architecture-options">
                                {ARCHITECTURE_OPTIONS.map((option, index) => (
                                  <option key={index} value={option.value} title={option.label.split(' - ')[1]} />
                                ))}
                              </datalist>
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
                                placeholder="Select or type an export format..."
                                list="export-format-options"
                              />
                              <datalist id="export-format-options">
                                {EXPORT_FORMAT_OPTIONS.map((option, index) => (
                                  <option key={index} value={option.value} title={option.label.split(' - ')[1]} />
                                ))}
                              </datalist>
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
                                placeholder="Select or type a build mode..."
                                list="mode-options"
                              />
                              <datalist id="mode-options">
                                {BUILD_MODE_OPTIONS.map((option, index) => (
                                  <option key={index} value={option.value} />
                                ))}
                              </datalist>
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
                          <GridItem span={12}>
                            <ExpandableSection
                              toggleText="Advanced Build Options"
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
                                        placeholder=""
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

                                  <GridItem span={12}>
                                    <FormGroup
                                      label={<PopoverLabel label="Environment Secret Reference" popoverContent="Name of a Kubernetes secret containing environment variables for private registry authentication (e.g., REGISTRY_USERNAME, REGISTRY_PASSWORD, REGISTRY_URL)" />}
                                      fieldId="envSecretRef"
                                    >
                                      <TextInput
                                        id="envSecretRef"
                                        value={formData.envSecretRef}
                                        onChange={(_event, value) =>
                                          handleInputChange("envSecretRef", value)
                                        }
                                      />
                                    </FormGroup>
                                  </GridItem>
                                </Grid>
                              </div>
                            </ExpandableSection>
                          </GridItem>
                        </Grid>
                      </StackItem>
                    </Stack>
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
                          <Alert
                            variant="info"
                            title="Template expects these files"
                            isInline
                          >
                            <ul style={{ margin: "8px 0 0 0", paddingLeft: "20px" }}>
                              {expectedFiles.map((filename, index) => (
                                <li key={index} style={{ margin: "4px 0" }}>
                                  <code>{filename}</code>
                                </li>
                              ))}
                            </ul>
                            <p style={{ margin: "8px 0 0 0" }}>
                              Please upload or create these files to match the template configuration.
                            </p>
                          </Alert>
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
                      {(!formData.name || (!formData.manifest && formData.name) || (!formData.architecture && formData.name && formData.manifest)) && (
                        <SplitItem>
                          <Alert
                            variant="warning"
                            title={
                              !formData.name 
                                ? "Build name required"
                                : !formData.manifest && formData.name
                                  ? "Manifest content required"
                                  : "Architecture required"
                            }
                            isInline
                            isPlain
                          />
                        </SplitItem>
                      )}
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
