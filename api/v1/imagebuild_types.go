/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ImageBuildSpec defines the desired state of ImageBuild
// +kubebuilder:printcolumn:name="StorageClass",type=string,JSONPath=`.spec.storageClass`
type ImageBuildSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// StorageClass is the name of the storage class to use for the build PVC
	StorageClass string `json:"storageClass,omitempty"`

	// OSBuildImage is the name of the image to use for the OS build (default: quay.io/automotive/automotive-osbuild:latest)
	// +kubebuilder:default="quay.io/centos-sig-automotive/automotive-osbuild:latest"
	OSBuildImage string `json:"osBuildImage,omitempty"`

	// Publishers contains configurations for different types of publishers
	// of where to publish the image
	// +optional
	Publishers []PublisherSpec `json:"publishers,omitempty"`
}

// PublisherSpec defines the configuration for a publisher
type PublisherSpec struct {
	// Type specifies the type of publisher (registry, s3, azure)
	// +kubebuilder:validation:Enum=registry;s3;azure
	Type string `json:"type"`

	// Registry configuration for container registry type
	// +optional
	Registry *RegistryConfig `json:"registry,omitempty"`
}

// RegistryConfig defines the configuration for publishing to a container registry
type RegistryConfig struct {
	// Secret is the name of the secret containing registry authentication
	Secret string `json:"secret"`

	// RepositoryURL is the target repository URL where images will be published
	RepositoryURL string `json:"repository_url"`
}

// ImageBuildStatus defines the observed state of ImageBuild
type ImageBuildStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ImageBuild is the Schema for the imagebuilds API
type ImageBuild struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ImageBuildSpec   `json:"spec,omitempty"`
	Status ImageBuildStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ImageBuildList contains a list of ImageBuild
type ImageBuildList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ImageBuild `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ImageBuild{}, &ImageBuildList{})
}
