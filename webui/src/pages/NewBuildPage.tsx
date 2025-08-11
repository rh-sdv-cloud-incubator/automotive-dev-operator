import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { createBuild, uploadFilesBatch } from '@/api/client';
import {
  PageSection,
  Title,
  TextInput,
  Form,
  FormGroup,
  TextArea,
  Button,
  FileUpload,
  HelperText,
  HelperTextItem,
} from '@patternfly/react-core';

export function NewBuildPage() {
  const [name, setName] = useState('');
  const [manifest, setManifest] = useState('');
  const [distro, setDistro] = useState('cs9');
  const [target, setTarget] = useState('qemu');
  const [architecture, setArchitecture] = useState('arm64');
  const [exportFormat, setExportFormat] = useState('image');
  const [mode, setMode] = useState('image');
  const [aibImage, setAibImage] = useState('quay.io/centos-sig-automotive/automotive-image-builder:1.0.0');
  const [aibExtraArgs, setAibExtraArgs] = useState<string>('');
  type UploadEntry = { id: string; file: File | null; dest: string };
  type InlineEntry = { id: string; text: string; dest: string };
  const [uploads, setUploads] = useState<UploadEntry[]>([{ id: crypto.randomUUID(), file: null, dest: 'sources/input.tar.gz' }]);
  const [inlineFiles, setInlineFiles] = useState<InlineEntry[]>([]);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const navigate = useNavigate();

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      await createBuild({
        name,
        manifest,
        serveArtifact: true,
        distro,
        target,
        architecture,
        exportFormat,
        mode,
        automotiveImageBuilder: aibImage,
        aibExtraArgs: aibExtraArgs
          .split(/\s+/)
          .map((s) => s.trim())
          .filter((s) => s.length > 0),
      });
      const batch = [
        ...uploads
          .filter((u) => u.file && u.dest.trim() !== '')
          .map((u) => ({ file: u.file as File, destPath: u.dest.trim() })),
        ...inlineFiles
          .filter((i) => i.text.trim() !== '' && i.dest.trim() !== '')
          .map((i) => ({ text: i.text, destPath: i.dest.trim() })),
      ];
      if (batch.length > 0) {
        await uploadFilesBatch(name, batch);
      }
      navigate(`/builds/${encodeURIComponent(name)}`);
    } catch (err) {
      setError(String(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <PageSection>
      <Title headingLevel="h1">New build</Title>
      <Form onSubmit={onSubmit} style={{ maxWidth: 900 }}>
        <FormGroup label="Name" isRequired fieldId="name">
          <TextInput id="name" value={name} onChange={(_e, v) => setName(v)} required />
        </FormGroup>
        <FormGroup label="Distro" fieldId="distro">
          <TextInput id="distro" value={distro} onChange={(_e, v) => setDistro(v)} />
        </FormGroup>
        <FormGroup label="Target" fieldId="target">
          <TextInput id="target" value={target} onChange={(_e, v) => setTarget(v)} />
        </FormGroup>
        <FormGroup label="Architecture" fieldId="arch">
          <TextInput id="arch" value={architecture} onChange={(_e, v) => setArchitecture(v)} />
        </FormGroup>
        <FormGroup label="Export format" fieldId="export">
          <TextInput id="export" value={exportFormat} onChange={(_e, v) => setExportFormat(v)} />
        </FormGroup>
        <FormGroup label="Mode" fieldId="mode">
          <TextInput id="mode" value={mode} onChange={(_e, v) => setMode(v)} />
        </FormGroup>
        <FormGroup label="Automotive Image Builder image" fieldId="aibimg">
          <TextInput id="aibimg" value={aibImage} onChange={(_e, v) => setAibImage(v)} />
        </FormGroup>
        <FormGroup label="Extra args (space-separated)" fieldId="aibargs">
          <TextInput id="aibargs" value={aibExtraArgs} onChange={(_e, v) => setAibExtraArgs(v)} />
        </FormGroup>
        <FormGroup label="Manifest (YAML)" isRequired fieldId="manifest">
          <TextArea id="manifest" value={manifest} onChange={(_e, v) => setManifest(v)} rows={12} required />
          <HelperText>
            <HelperTextItem variant="indeterminate">Include source_path entries if you plan to upload local files</HelperTextItem>
          </HelperText>
        </FormGroup>
        <Title headingLevel="h2" style={{ marginTop: 16 }}>Optional: Upload local files referenced by manifest</Title>
        {uploads.map((u, idx) => (
          <div key={u.id} style={{ display: 'grid', gridTemplateColumns: '1fr 1fr auto', gap: 12, alignItems: 'end', marginBottom: 8 }}>
            <FormGroup label={`File ${idx + 1}`} fieldId={`file-${u.id}`}>
              <FileUpload
                id={`file-${u.id}`}
                value={u.file ?? undefined}
                filename={u.file?.name}
                onFileInputChange={(_event, file: File | null) => {
                  const next = uploads.slice();
                  next[idx] = { ...u, file };
                  setUploads(next);
                }}
                browseButtonText="Choose file"
              />
            </FormGroup>
            <FormGroup label="Destination path" fieldId={`dest-${u.id}`}>
              <TextInput id={`dest-${u.id}`} value={u.dest} onChange={(_e, v) => {
                const next = uploads.slice();
                next[idx] = { ...u, dest: v };
                setUploads(next);
              }} />
            </FormGroup>
            <div>
              <Button variant="link" onClick={() => setUploads((prev) => prev.filter((x) => x.id !== u.id))} isDisabled={uploads.length === 1}>Remove</Button>
            </div>
          </div>
        ))}
        <div style={{ marginBottom: 16 }}>
          <Button variant="secondary" onClick={() => setUploads((prev) => [...prev, { id: crypto.randomUUID(), file: null, dest: 'sources/input.tar.gz' }])}>Add another file</Button>
        </div>

        <Title headingLevel="h2" style={{ marginTop: 16 }}>Optional: Create text files inline</Title>
        {inlineFiles.map((i, idx) => (
          <div key={i.id} style={{ display: 'grid', gridTemplateColumns: '1fr 2fr auto', gap: 12, alignItems: 'start', marginBottom: 8 }}>
            <FormGroup label={`Destination path ${idx + 1}`} fieldId={`it-dest-${i.id}`}>
              <TextInput id={`it-dest-${i.id}`} value={i.dest} onChange={(_e, v) => {
                const next = inlineFiles.slice();
                next[idx] = { ...i, dest: v };
                setInlineFiles(next);
              }} />
            </FormGroup>
            <FormGroup label="Contents" fieldId={`it-text-${i.id}`}>
              <TextArea id={`it-text-${i.id}`} value={i.text} onChange={(_e, v) => {
                const next = inlineFiles.slice();
                next[idx] = { ...i, text: v };
                setInlineFiles(next);
              }} rows={8} />
            </FormGroup>
            <div>
              <Button variant="link" onClick={() => setInlineFiles((prev) => prev.filter((x) => x.id !== i.id))} isDisabled={inlineFiles.length === 0}>Remove</Button>
            </div>
          </div>
        ))}
        <div>
          <Button variant="secondary" onClick={() => setInlineFiles((prev) => [...prev, { id: crypto.randomUUID(), text: '', dest: 'sources/config.txt' }])}>Add inline text file</Button>
        </div>
        {error && <div style={{ color: 'var(--pf-v5-global--danger-color--100)' }}>{error}</div>}
        <div style={{ marginTop: 16 }}>
          <Button type="submit" variant="primary" isDisabled={submitting}>Create build</Button>
        </div>
      </Form>
    </PageSection>
  );
}


