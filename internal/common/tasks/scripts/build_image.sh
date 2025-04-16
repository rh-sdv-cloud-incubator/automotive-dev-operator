#!/bin/sh
set -e


# Make the internal registry trusted
# TODO think about whether this is really the right approach
mkdir -p /etc/containers
cat > /etc/containers/registries.conf << EOF
[registries.insecure]
registries = ['image-registry.openshift-image-registry.svc:5000']
EOF

TOKEN=$(cat /var/run/secrets/kubernetes.io/serviceaccount/token)
REGISTRY="image-registry.openshift-image-registry.svc:5000"
NAMESPACE=$(cat /var/run/secrets/kubernetes.io/serviceaccount/namespace)

mkdir -p $HOME/.config
cat > $HOME/.authjson <<EOF
{
  "auths": {
    "$REGISTRY": {
      "auth": "$(echo -n "serviceaccount:$TOKEN" | base64 -w0)"
    }
  }
}
EOF

export REGISTRY_AUTH_FILE=$HOME/.authjson
export CONTAINERS_REGISTRIES_CONF="/etc/containers/registries.conf"

osbuildPath="/usr/bin/osbuild"
storePath="/_build"
runTmp="/run/osbuild/"

mkdir -p "$storePath"
mkdir -p "$runTmp"

MANIFEST_FILE=$(cat /tekton/results/manifest-file-path)
if [ -z "$MANIFEST_FILE" ]; then
    echo "Error: No manifest file path provided"
    exit 1
fi

echo "using manifest file: $MANIFEST_FILE"

if [ ! -f "$MANIFEST_FILE" ]; then
    echo "error: Manifest file not found at $MANIFEST_FILE"
    exit 1
fi

if mountpoint -q "$osbuildPath"; then
    exit 0
fi

rootType="system_u:object_r:root_t:s0"
chcon "$rootType" "$storePath"

installType="system_u:object_r:install_exec_t:s0"
if ! mountpoint -q "$runTmp"; then
  mount -t tmpfs tmpfs "$runTmp"
fi

destPath="$runTmp/osbuild"
cp -p "$osbuildPath" "$destPath"
chcon "$installType" "$destPath"

mount --bind "$destPath" "$osbuildPath"

cd $(workspaces.shared-workspace.path)

# Determine file extension
if [ "$(params.export-format)" = "image" ]; then
  file_extension=".raw"
elif [ "$(params.export-format)" = "qcow2" ]; then
  file_extension=".qcow2"
else
  file_extension=".$(params.export-format)"
fi

cleanName=$(params.distro)-$(params.target)
exportFile=${cleanName}${file_extension}

mode_param=""
if [ -n "$(params.mode)" ]; then
  mode_param="--mode $(params.mode)"
fi

CUSTOM_DEFS=""
CUSTOM_DEFS_FILE="$(workspaces.manifest-config-workspace.path)/custom-definitions.env"
if [ -f "$CUSTOM_DEFS_FILE" ]; then
  echo "Processing custom definitions from $CUSTOM_DEFS_FILE"
  while read -r line || [[ -n "$line" ]]; do
    for def in $line; do
      CUSTOM_DEFS+=" --define $def"
    done
  done < "$CUSTOM_DEFS_FILE"
else
  echo "No custom-definitions.env file found"
fi

arch="$(params.target-architecture)"
case "$arch" in
  "arm64")
    arch="aarch64"
    ;;
  "amd64")
    arch="x86_64"
    ;;
esac

build_command="automotive-image-builder --verbose \
  build \
  $CUSTOM_DEFS \
  --distro $(params.distro) \
  --target $(params.target) \
  --arch=${arch} \
  --build-dir=/output/_build \
  --export $(params.export-format) \
  --osbuild-manifest=/output/image.json \
  $mode_param \
  $MANIFEST_FILE \
  /output/${exportFile}"

echo "contents of shared workspace before build:"
ls -la $(workspaces.shared-workspace.path)/
echo "contents of working manifest:"
cat "$MANIFEST_FILE"


echo "Running the build command: $build_command"
$build_command

pushd /output
ln -sf ./${exportFile} ./disk.img

echo "copying build artifacts to shared workspace..."

mkdir -p $(workspaces.shared-workspace.path)

if [ -d "/output/${exportFile}" ]; then
    echo "${exportFile} is a directory, copying recursively..."
    cp -rv "/output/${exportFile}" $(workspaces.shared-workspace.path)/ || echo "Failed to copy ${exportFile}"
else
    echo "${exportFile} is a regular file, copying..."
    cp -v "/output/${exportFile}" $(workspaces.shared-workspace.path)/ || echo "Failed to copy ${exportFile}"
fi

if [ -d "/output/disk.img" ]; then
    echo "disk.img is a directory, copying recursively..."
    cp -rv "/output/disk.img" $(workspaces.shared-workspace.path)/${cleanName}${file_extension} || echo "Failed to copy disk.img"
elif [ -L "/output/disk.img" ]; then
    echo "disk.img is a symlink, copying with -L to follow symlink..."
    cp -vL "/output/disk.img" $(workspaces.shared-workspace.path)/${cleanName}${file_extension} || echo "Failed to copy disk.img"
elif [ -f "/output/disk.img" ]; then
    echo "disk.img is a file, copying..."
    cp -v "/output/disk.img" $(workspaces.shared-workspace.path)/${cleanName}${file_extension} || echo "Failed to copy disk.img"
else
    echo "Warning: disk.img is neither a file, directory, nor symlink"
fi

pushd $(workspaces.shared-workspace.path)
if [ -d "${exportFile}" ]; then
    echo "Creating symlink to directory ${exportFile}"
    ln -sf ${exportFile} disk.img
elif [ -f "${exportFile}" ]; then
    echo "Creating symlink to file ${exportFile}"
    ln -sf ${exportFile} disk.img
else
    echo "Warning: ${exportFile} not found in workspace, cannot create symlink"
fi
popd

# Copy the image manifest
cp -v /output/image.json $(workspaces.shared-workspace.path)/image.json || echo "Failed to copy image.json"

echo "Contents of shared workspace:"
ls -la $(workspaces.shared-workspace.path)/
