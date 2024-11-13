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

// MonitoringServiceSpec defines the desired state of MonitoringService
// MonitoringServiceSpec defines the desired state of MonitoringService
type MonitoringServiceSpec struct {
	// ConfigDrift specifies the config drift detection for custom services
	ConfigDrift ConfigDriftSpec `json:"configDrift,omitempty"`

	// Velero specifies the Velero backup monitoring configuration
	Velero VeleroSpec `json:"velero,omitempty"`

	// K8sMonitor specifies the Kubernetes services monitoring configuration
	K8sMonitor K8sMonitorSpec `json:"k8sMonitor,omitempty"`

	// K8sConfigDrift specifies the config drift detection for Kubernetes services
	K8sConfigDrift K8sConfigDriftSpec `json:"k8sConfigDrift,omitempty"`

	// NodeServiceMonitor specifies node-level service monitoring
	NodeServiceMonitor NodeServiceMonitorSpec `json:"nodeServiceMonitor,omitempty"`
}

// ConfigDriftSpec defines the config drift detection settings for services
type ConfigDriftSpec struct {
	// Enabled enables or disables config drift detection
	Enabled bool `json:"enabled"`

	// Services is the list of services to monitor for config drift
	Services []string `json:"services,omitempty"`
}

// VeleroSpec defines the Velero backup monitoring settings
type VeleroSpec struct {
	// MonitorBackups enables monitoring of Velero backups
	MonitorBackups bool `json:"monitorBackups"`

	// EnforceImmutability ensures backups are immutable
	EnforceImmutability bool `json:"enforceImmutability"`
}

// K8sMonitorSpec defines the monitoring settings for Kubernetes services
type K8sMonitorSpec struct {
	// Enabled enables or disables Kubernetes services monitoring
	Enabled bool `json:"enabled"`

	// Services is the list of Kubernetes services to monitor
	Services []string `json:"services,omitempty"`
}

// K8sConfigDriftSpec defines the config drift detection for Kubernetes services
type K8sConfigDriftSpec struct {
	// Enabled enables or disables config drift detection for Kubernetes services
	Enabled bool `json:"enabled"`

	// Services is the list of Kubernetes services to monitor for config drift
	Services []string `json:"services,omitempty"`
}

// NodeServiceMonitorSpec defines the monitoring settings for node-level services
type NodeServiceMonitorSpec struct {
	// Enabled enables or disables node service monitoring
	Enabled bool `json:"enabled"`

	// Services is the list of node services to monitor
	Services []string `json:"services,omitempty"`
}

type ConfigDriftStatus struct {
}

type VeleroStatus struct {
	Backups         []Backup              `json:"backups,omitempty"`
	BackupCount     int                   `json:"backupCount"`
	ImmutableStatus []NodeImmutableStatus `json:"immutableStatus,omitempty"`
}

type Backup struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type K8sMonitorStatus struct {
	NodeName       string          `json:"nodeName"`
	ServicesStatus []ServiceStatus `json:"servicesStatus,omitempty"`
}

type K8sConfigDriftStatus struct {
	ComponentName        string `json:"componentName"`
	ConfigChanged        bool   `json:"configChanged"`
	OldChecksum          string `json:"oldChecksum,omitempty"`
	NewChecksum          string `json:"newChecksum,omitempty"`
	ChangedConfiguration string `json:"changedConfiguration,omitempty"`
}

type NodeServiceMonitorStatus struct {
	NodeName       string          `json:"nodeName"`
	ServicesStatus []ServiceStatus `json:"servicesStatus,omitempty"`
}

type NodeImmutableStatus struct {
	NodeName string `json:"nodeName"`
	Status   string `json:"status"`
}

type ServiceStatus struct {
	ServiceName   string `json:"serviceName"`
	ServiceStatus string `json:"serviceStatus"` // e.g., "Running" or "Not Running"
}

// MonitoringServiceStatus defines the observed state of MonitoringService
type MonitoringServiceStatus struct {
	// A map of service names to their status (e.g., "active", "inactive")
	ConfigDriftStatus ConfigDriftStatus `json:"configDriftStatus,omitempty"`

	VeleroStatus VeleroStatus `json:"veleroStatus,omitempty"`

	K8sMonitorStatus []K8sMonitorStatus `json:"k8sMonitorStatus,omitempty"`

	K8sConfigDriftStatus []K8sConfigDriftStatus `json:"k8sConfigDriftStatus,omitempty"`

	NodeServiceMonitorStatus []NodeServiceMonitorStatus `json:"nodeServiceMonitorStatus,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// MonitoringService is the Schema for the monitoringservices API
type MonitoringService struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MonitoringServiceSpec   `json:"spec,omitempty"`
	Status MonitoringServiceStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// MonitoringServiceList contains a list of MonitoringService
type MonitoringServiceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MonitoringService `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MonitoringService{}, &MonitoringServiceList{})
}
