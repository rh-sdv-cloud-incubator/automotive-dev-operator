package tasks

import (
	_ "embed"
)

//go:embed scripts/find_manifest.sh
var findManifestScriptFile []byte

//go:embed scripts/build_image.sh
var buildImageScriptFile []byte

//go:embed scripts/push_artifact.sh
var pushArtifactScriptFile []byte

// FindManifestScript is the script for finding manifest files
var FindManifestScript string

// BuildImageScript is the script for building automotive images
var BuildImageScript string

// PushArtifactScript is the script for pushing artifacts to a registry
var PushArtifactScript string

// Initialize script variables from embedded files
func init() {
	FindManifestScript = string(findManifestScriptFile)
	BuildImageScript = string(buildImageScriptFile)
	PushArtifactScript = string(pushArtifactScriptFile)
}
