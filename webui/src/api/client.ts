export type BuildListItem = {
  name: string;
  phase: string;
  message: string;
  createdAt: string; // RFC3339
};

export type BuildResponse = {
  name: string;
  phase: string;
  message: string;
  artifactURL?: string | null;
  artifactFileName?: string | null;
};

export type BuildRequest = {
  name: string;
  manifest: string;
  manifestFileName?: string;
  distro?: string;
  target?: string;
  architecture?: string;
  exportFormat?: string;
  mode?: string;
  automotiveImageBuilder?: string;
  storageClass?: string;
  runtimeClassName?: string;
  customDefs?: string[];
  aibExtraArgs?: string[];
  serveArtifact?: boolean;
  exposeRoute?: boolean;
};

// In dev, always use relative path to hit Vite proxy and avoid CORS.
// In prod, allow overriding with VITE_API_BASE.
const base = import.meta.env.DEV ? '' : (import.meta.env.VITE_API_BASE ?? '');

export async function listBuilds(): Promise<BuildListItem[]> {
  const r = await fetch(`${base}/v1/builds`);
  if (!r.ok) throw new Error(`list builds: ${r.status}`);
  return r.json();
}

export async function getBuild(name: string): Promise<BuildResponse> {
  const r = await fetch(`${base}/v1/builds/${encodeURIComponent(name)}`);
  if (!r.ok) throw new Error(`get build: ${r.status}`);
  return r.json();
}

export function streamLogs(name: string, onChunk: (text: string) => void, signal?: AbortSignal) {
  const url = `${base}/v1/builds/${encodeURIComponent(name)}/logs?follow=true`;
  fetch(url, { signal })
    .then(async (r) => {
    if (!r.ok || !r.body) return;
    const reader = r.body.getReader();
    const decoder = new TextDecoder();
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      onChunk(decoder.decode(value));
    }
    })
    .catch((err) => {
      // Ignore AbortError from cleanup; log other errors for visibility
      if (!(err instanceof DOMException && err.name === 'AbortError')) {
        // eslint-disable-next-line no-console
        console.warn('log stream error', err);
      }
    });
}

export async function downloadArtifact(name: string): Promise<void> {
  const r = await fetch(`${base}/v1/builds/${encodeURIComponent(name)}/artifact`);
  if (!r.ok) throw new Error('artifact not ready');
  const blob = await r.blob();
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  const cd = r.headers.get('Content-Disposition') || '';
  const match = cd.match(/filename="?([^";]+)"?/i);
  a.download = match?.[1] || `${name}-artifact`;
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}

export async function createBuild(req: BuildRequest): Promise<BuildResponse> {
  const r = await fetch(`${base}/v1/builds`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  });
  if (!r.ok) throw new Error(`create build: ${r.status}`);
  return r.json();
}

export async function uploadFile(name: string, file: File, destPath: string): Promise<void> {
  const form = new FormData();
  form.append('file', file, destPath);
  const r = await fetch(`${base}/v1/builds/${encodeURIComponent(name)}/uploads`, {
    method: 'POST',
    body: form,
  });
  if (!r.ok) throw new Error(`upload failed: ${r.status}`);
}

export async function uploadFilesBatch(
  name: string,
  items: Array<{ file?: File; text?: string; destPath: string }>,
): Promise<void> {
  if (items.length === 0) return;
  const form = new FormData();
  for (const item of items) {
    if (item.file) {
      form.append('file', item.file, item.destPath);
    } else if (item.text != null) {
      const blob = new Blob([item.text], { type: 'text/plain' });
      const f = new File([blob], item.destPath, { type: 'text/plain' });
      form.append('file', f, item.destPath);
    }
  }
  const r = await fetch(`${base}/v1/builds/${encodeURIComponent(name)}/uploads`, {
    method: 'POST',
    body: form,
  });
  if (!r.ok) throw new Error(`upload failed: ${r.status}`);
}


