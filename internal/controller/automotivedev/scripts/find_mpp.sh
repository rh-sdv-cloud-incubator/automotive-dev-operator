#!/bin/sh
set -e

echo "looking for .mpp.yml file"

MPP_FILE=$(find $(workspaces.mpp-config-workspace.path) -name '*.mpp.yml' -type f)

if [ -z "$MPP_FILE" ]; then
  echo "No .mpp.yml file found in the ConfigMap"
  exit 1
fi

echo "found .mpp.yml file at $MPP_FILE"

echo $MPP_FILE > /tekton/results/mpp-file-path
