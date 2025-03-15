#!/bin/sh
set -e

echo "looking for manifest file..."

echo "listing contents of manifest config workspace:"
ls -la $(workspaces.manifest-config-workspace.path)

MANIFEST_FILE=$(find $(workspaces.manifest-config-workspace.path) -name '*.mpp.yml' -o -name '*.aib.yml' -type f | head -n 1)

if [ -z "$MANIFEST_FILE" ]; then
  echo "No manifest file found in the ConfigMap"
  exit 1
fi

echo "found manifest file at $MANIFEST_FILE"

manifest_basename=$(basename "$MANIFEST_FILE")
workspace_manifest="/manifest-work/$manifest_basename"

cp "$MANIFEST_FILE" "$workspace_manifest"
echo "created working copy of manifest at $workspace_manifest"

yq eval -i "(.content.add_files.[].source_path) |= \"$(workspaces.shared-workspace.path)/\" + ." "$workspace_manifest"

if yq eval '.qm.content.add_files' "$workspace_manifest" | grep -q '^[^#]'; then
  yq eval -i "(.qm.content.add_files.[].source_path) |= \"$(workspaces.shared-workspace.path)/\" + ." "$workspace_manifest"
fi

echo "updated manifest contents:"
cat "$workspace_manifest"

mkdir -p /tekton/results
echo -n "$workspace_manifest" > /tekton/results/manifest-file-path
