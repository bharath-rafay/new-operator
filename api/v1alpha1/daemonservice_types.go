/*
Copyright 2024.

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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// DaemonServiceSpec defines the desired state of DaemonService
type DaemonServiceSpec struct {
	ServiceNames   []string          `json:"serviceNames"`           // List of custom services to monitor
	MonitorK8sCore bool              `json:"monitorK8sCore"`         // Enable monitoring of Kubernetes core components
	NodeSelector   map[string]string `json:"nodeSelector,omitempty"` // Optional: filter nodes
}
type DaemonServiceStatus struct {
	Nodes          []NodeServiceStatus   `json:"nodes,omitempty"`
	Configurations []ConfigurationStatus `json:"configurations,omitempty"`
}

type NodeServiceStatus struct {
	NodeName       string          `json:"nodeName"`
	ServicesStatus []ServiceStatus `json:"servicesStatus"`
}

type ServiceStatus struct {
	ServiceName   string `json:"serviceName"`
	ServiceStatus string `json:"serviceStatus"` // e.g., "Running" or "Not Running"
}

type ConfigurationStatus struct {
	ComponentName        string `json:"componentName"`
	ConfigChanged        bool   `json:"configChanged"`
	OldChecksum          string `json:"oldChecksum,omitempty"`
	NewChecksum          string `json:"newChecksum,omitempty"`
	ChangedConfiguration string `json:"changedConfiguration,omitempty"` // Optional: the diff or full changed configuration
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// DaemonService is the Schema for the daemonservices API
type DaemonService struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DaemonServiceSpec   `json:"spec,omitempty"`
	Status DaemonServiceStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// DaemonServiceList contains a list of DaemonService
type DaemonServiceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DaemonService `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DaemonService{}, &DaemonServiceList{})
}
