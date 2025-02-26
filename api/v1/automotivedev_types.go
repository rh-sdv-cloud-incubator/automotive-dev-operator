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

// AutomotiveDevSpec defines the desired state of AutomotiveDev
type AutomotiveDevSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Foo is an example field of AutomotiveDev. Edit automotivedev_types.go to remove/update
	Foo string `json:"foo,omitempty"`
}

// AutomotiveDevStatus defines the observed state of AutomotiveDev
type AutomotiveDevStatus struct {
	// Phase represents the current phase of the AutomotiveDev environment (Ready, Pending, Failed)
	Phase string `json:"phase,omitempty"`

	// Message provides more detail about the current phase
	Message string `json:"message,omitempty"`

	// LastUpdated is when the status was last updated
	LastUpdated *metav1.Time `json:"lastUpdated,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// AutomotiveDev is the Schema for the automotivedevs API
type AutomotiveDev struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AutomotiveDevSpec   `json:"spec,omitempty"`
	Status AutomotiveDevStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AutomotiveDevList contains a list of AutomotiveDev
type AutomotiveDevList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AutomotiveDev `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AutomotiveDev{}, &AutomotiveDevList{})
}
