#!/bin/sh
set -e

echo "looking for manifest file"

MANIFEST_FILE=$(find $(workspaces.manifest-config-workspace.path) -name '*.mpp.yml' -o -name '*.aib.yml' -type f | head -n 1)

if [ -z "$MANIFEST_FILE" ]; then
  echo "No manifest file found in the ConfigMap"
  exit 1
fi

echo "found manifest file at $MANIFEST_FILE"

echo $MANIFEST_FILE > /tekton/results/manifest-file-path
