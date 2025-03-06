#!/bin/sh
set -e

osbuildPath="/usr/bin/osbuild"
storePath="/_build"
runTmp="/run/osbuild/"

mkdir -p "$storePath"
mkdir -p "$runTmp"

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

if [ "$(params.export-format)" = "image" ]; then
  file_extension=".raw"
elif [ "$(params.export-format)" = "qcow2" ]; then
  file_extension=".qcow2"
else
  file_extension="$(params.export-format)"
fi

exportFile=$(params.distro)-$(params.target)-$(params.export-format)${file_extension}

mode_param=""
if [ -n "$(params.mode)" ]; then
  mode_param="--mode $(params.mode)"
fi

MPP_FILE=$(cat /tekton/results/mpp-file-path)

CUSTOM_DEFS=""
CUSTOM_DEFS_FILE="$(workspaces.mpp-config-workspace.path)/custom-definitions.env"
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
  $MPP_FILE \
  /output/${exportFile}"

echo "Running the build command: $build_command"
$build_command

pushd /output
ln -s ./${exportFile} ./disk.img
echo "Build command completed. Listing output directory:"
ls -l
