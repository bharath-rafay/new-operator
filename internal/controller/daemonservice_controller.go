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

package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"crypto/sha256"
	"encoding/hex"

	monitoringv1alpha1 "github.com/bharath-rafay/security-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

// DaemonServiceReconciler reconciles a DaemonService object
type DaemonServiceReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// logger func(request *reconcile.Request) logr.Logger
}

//+kubebuilder:rbac:groups=monitoring.example.com,resources=daemonservices,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=monitoring.example.com,resources=daemonservices/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=monitoring.example.com,resources=daemonservices/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the DaemonService object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.16.3/pkg/reconcile
func (r *DaemonServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var daemonService monitoringv1alpha1.DaemonService
	if err := r.Get(ctx, req.NamespacedName, &daemonService); err != nil {
		//r.logger(&reconcile.Request{}).Error(err, "daeomon service error")
		return ctrl.Result{RequeueAfter: time.Minute * 1}, client.IgnoreNotFound(err)
	}

	// Step 1: Check for configuration changes in core component pods
	configStatuses, err := r.checkK8sCorePodChanges(ctx, &daemonService)
	if err != nil {
		//r.logger(&reconcile.Request{}).Error(err, "daeomon service error")
		return ctrl.Result{RequeueAfter: time.Minute * 1}, err
	}

	// Step 2: Monitor Kubernetes core components if enabled
	nodeStatuses := []monitoringv1alpha1.NodeServiceStatus{}
	if daemonService.Spec.MonitorK8sCore {
		coreStatuses, err := r.checkK8sCoreComponents(ctx)
		if err != nil {
			//r.logger(&reconcile.Request{}).Error(err, "daeomon service error")
			return ctrl.Result{RequeueAfter: time.Minute * 1}, err
		}
		nodeStatuses = append(nodeStatuses, coreStatuses...)
	}

	// Step 3: Deploy the DaemonSet to monitor custom daemon services if needed
	if len(daemonService.Spec.ServiceNames) > 0 {
		if err := r.deployDaemonSet(ctx, daemonService); err != nil {
			//r.logger(&reconcile.Request{}).Error(err, "daeomon service error")
			return ctrl.Result{}, err
		}

		// if err := r.waitForDaemonSetReady(ctx); err != nil {
		// 	//r.logger(&reconcile.Request{}).Error(err, "daeomon service error")
		// 	return ctrl.Result{}, err
		// }

		podList := &corev1.PodList{}
		labelSelector := labels.SelectorFromSet(labels.Set{"app": "service-monitor"})
		if err := r.List(ctx, podList, &client.ListOptions{LabelSelector: labelSelector}); err != nil {
			//r.logger(&reconcile.Request{}).Error(err, "daeomon service error")
			return ctrl.Result{RequeueAfter: time.Second * 10}, err
		}

		for _, pod := range podList.Items {
			nodeStatus, err := r.getMultiServiceStatusFromPod(ctx, pod)
			if err != nil {
				//r.logger(&reconcile.Request{}).Error(err, "daeomon service error")
				return ctrl.Result{RequeueAfter: time.Second * 10}, err
			}
			nodeStatuses = append(nodeStatuses, nodeStatus)
		}
		time.Sleep(time.Second * 10)
		// Delete the DaemonSet after collecting the status
		if err := r.deleteDaemonSet(ctx); err != nil {
			//r.logger(&reconcile.Request{}).Error(err, "daeomon service error")
			return ctrl.Result{RequeueAfter: time.Second * 10}, err
		}
	}

	// Step 4: Update the status with results from custom services, core components, and config changes
	daemonService.Status.Nodes = nodeStatuses
	daemonService.Status.Configurations = configStatuses
	if err := r.Status().Update(ctx, &daemonService); err != nil {
		//r.logger(&reconcile.Request{}).Error(err, "daeomon service error")
		return ctrl.Result{RequeueAfter: time.Minute * 1}, err
	}

	// Requeue after a specified interval
	return ctrl.Result{RequeueAfter: time.Minute * 1}, nil
}

func (r *DaemonServiceReconciler) checkK8sCorePodChanges(ctx context.Context, daemonService *monitoringv1alpha1.DaemonService) ([]monitoringv1alpha1.ConfigurationStatus, error) {
	var configStatuses []monitoringv1alpha1.ConfigurationStatus

	// List of core Kubernetes components to monitor
	coreComponents := []string{
		"kube-apiserver",
		"kube-controller-manager",
		"kube-scheduler",
	}

	for _, componentName := range coreComponents {
		// Get the pod for each component
		pod, err := r.getCoreComponentPod(ctx, componentName)
		if err != nil {
			//r.logger(&reconcile.Request{}).Error(err, "daeomon service error")
			return nil, err
		}

		// Compute the checksum of the current pod spec
		currentChecksum, err := computePodSpecChecksum(pod)
		if err != nil {
			//r.logger(&reconcile.Request{}).Error(err, "daeomon service error")
			return nil, err
		}

		var oldChecksum string
		var configChanged bool

		// Check the previously stored checksum in the status
		for _, storedConfig := range daemonService.Status.Configurations {
			if storedConfig.ComponentName == componentName {
				oldChecksum = storedConfig.NewChecksum
				configChanged = (currentChecksum != oldChecksum)
				break
			}
		}

		// If configuration changed, store the new checksum and mark as changed
		if configChanged {
			diff, err := r.generatePodConfigDiff(pod)
			if err != nil {
				//r.logger(&reconcile.Request{}).Error(err, "daeomon service error")
				return nil, err
			}
			configStatuses = append(configStatuses, monitoringv1alpha1.ConfigurationStatus{
				ComponentName:        componentName,
				ConfigChanged:        true,
				OldChecksum:          oldChecksum,
				NewChecksum:          currentChecksum,
				ChangedConfiguration: diff, // Include the diff or relevant changed fields
			})
		} else {
			configStatuses = append(configStatuses, monitoringv1alpha1.ConfigurationStatus{
				ComponentName: componentName,
				ConfigChanged: false,
				NewChecksum:   currentChecksum,
			})
		}
	}

	return configStatuses, nil
}

// Generate a diff for the pod spec (or just include relevant fields for simplicity)
func (r *DaemonServiceReconciler) generatePodConfigDiff(pod *corev1.Pod) (string, error) {
	relevantSpec := struct {
		Containers []corev1.Container `json:"containers"`
		Volumes    []corev1.Volume    `json:"volumes"`
	}{
		Containers: pod.Spec.Containers,
		Volumes:    pod.Spec.Volumes,
	}

	// Return the relevant parts of the pod spec as a diff (could be more sophisticated)
	podSpecBytes, err := json.MarshalIndent(relevantSpec, "", "  ")
	if err != nil {
		//r.logger(&reconcile.Request{}).Error(err, "daeomon service error")
		return "", err
	}
	return string(podSpecBytes), nil
}

func (r *DaemonServiceReconciler) deployDaemonSet(ctx context.Context, daemonService monitoringv1alpha1.DaemonService) error {
	serviceList := convertServiceNames(daemonService.Spec.ServiceNames)
	daemonSet := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "service-monitor",
			Namespace: "default",
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "service-monitor"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "service-monitor"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:    "service-monitor",
							Image:   "ubuntu:latest",
							Command: []string{"/bin/bash", "-c"},
							Args: []string{
								fmt.Sprintf(`
                                    while true; do
                                        statuses=();
                                        for service in %s; do
                                            status=$(ps aux | grep -q '$service' && echo "Running" || echo "Not Running");
											cleaned_service=$(echo "$service" | sed 's/\[\(.\)\]\(.*\)/\1\2/');
                                            statuses+=("{\"serviceName\":\"$cleaned_service\", \"serviceStatus\":\"$status\"}")
                                        done;
										test=$(IFS=','; echo "${statuses[*]}");
                                        echo "{\"node\":\"$(hostname)\", \"services\":[$test]}";
                                        sleep 300;
                                    done
                                `, strings.Join(serviceList, " ")),
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									MountPath: "/host-root",
									Name:      "root",
									ReadOnly:  true,
								},
								// {
								// 	MountPath: "/run/systemd",
								// 	Name:      "systemd",
								// 	ReadOnly:  true,
								// },
							},
							SecurityContext: &corev1.SecurityContext{
								Privileged: pointer.BoolPtr(true),
								RunAsUser:  pointer.Int64Ptr(0),
							},
						},
					},
					HostNetwork: true,
					HostPID:     true,
					Volumes: []corev1.Volume{
						{
							Name: "root",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/",
									// Type: (*corev1.HostPathType)(pointer.StringPtr("Directory")),
								},
							},
						},
						// {
						// 	Name: "systemd",
						// 	VolumeSource: corev1.VolumeSource{
						// 		HostPath: &corev1.HostPathVolumeSource{
						// 			Path: "/run/systemd",
						// 			Type: (*corev1.HostPathType)(pointer.StringPtr("Directory")),
						// 		},
						// 	},
						// },
					},
				},
			},
		},
	}

	if err := r.Client.Create(ctx, daemonSet); err != nil && !errors.IsAlreadyExists(err) {
		//r.logger(&reconcile.Request{}).Error(err, "daeomon service error")
		return err
	}
	return nil
}

// Helper function to compute checksum from pod spec (relevant fields)
func computePodSpecChecksum(pod *corev1.Pod) (string, error) {
	// Extract relevant fields from the pod spec (command, args, env, etc.)
	relevantSpec := struct {
		Containers []corev1.Container `json:"containers"`
		Volumes    []corev1.Volume    `json:"volumes"`
	}{
		Containers: pod.Spec.Containers,
		Volumes:    pod.Spec.Volumes,
	}

	// Serialize the relevant fields to JSON
	podSpecBytes, err := json.Marshal(relevantSpec)
	if err != nil {
		return "", err
	}

	// Calculate the checksum (hash)
	hash := sha256.Sum256(podSpecBytes)
	return hex.EncodeToString(hash[:]), nil
}

// Function to retrieve the pod for a specific component (e.g., kube-apiserver)
func (r *DaemonServiceReconciler) getCoreComponentPod(ctx context.Context, componentName string) (*corev1.Pod, error) {
	podList := &corev1.PodList{}
	// Retrieve pods from the kube-system namespace with a label selector
	labelSelector := metav1.LabelSelector{
		MatchLabels: map[string]string{"component": componentName},
	}
	selector, err := metav1.LabelSelectorAsSelector(&labelSelector)
	if err != nil {
		//r.logger(&reconcile.Request{}).Error(err, "daeomon service error")
		return nil, err
	}
	if err := r.List(ctx, podList, &client.ListOptions{
		Namespace:     "kube-system",
		LabelSelector: selector,
	}); err != nil {
		//r.logger(&reconcile.Request{}).Error(err, "daeomon service error")
		return nil, err
	}

	if len(podList.Items) > 0 {
		return &podList.Items[0], nil
	}
	return nil, fmt.Errorf("no pod found for component %s", componentName)
}

func (r *DaemonServiceReconciler) checkK8sCoreComponents(ctx context.Context) ([]monitoringv1alpha1.NodeServiceStatus, error) {
	var coreStatuses []monitoringv1alpha1.NodeServiceStatus

	// List the Kubernetes core components in the kube-system namespace
	componentPods := []string{
		"kube-apiserver",
		"kube-controller-manager",
		"kube-scheduler",
	}

	for _, componentName := range componentPods {
		podList := &corev1.PodList{}
		if err := r.List(ctx, podList, &client.ListOptions{
			Namespace:     "kube-system",
			LabelSelector: labels.SelectorFromSet(labels.Set{"component": componentName}),
		}); err != nil {
			//r.logger(&reconcile.Request{}).Error(err, "daeomon service error")
			return nil, err
		}

		// Check the status of the pods
		for _, pod := range podList.Items {
			status := "Not Running"
			if pod.Status.Phase == corev1.PodRunning {
				status = "Running"
			}

			coreStatuses = append(coreStatuses, monitoringv1alpha1.NodeServiceStatus{
				NodeName: pod.Spec.NodeName,
				ServicesStatus: []monitoringv1alpha1.ServiceStatus{
					{
						ServiceName:   componentName,
						ServiceStatus: status,
					},
				},
			})
		}
	}

	return coreStatuses, nil
}

func (r *DaemonServiceReconciler) waitForDaemonSetReady(ctx context.Context) error {
	daemonSet := &appsv1.DaemonSet{}
	if err := r.Get(ctx, client.ObjectKey{Name: "service-monitor", Namespace: "default"}, daemonSet); err != nil {
		//r.logger(&reconcile.Request{}).Error(err, "daeomon service error")
		return err
	}

	// Check if all desired pods are available
	if daemonSet.Status.NumberAvailable == daemonSet.Status.DesiredNumberScheduled {
		return nil // All pods are ready
	}

	return fmt.Errorf("daemonset not ready yet")
}

func (r *DaemonServiceReconciler) deleteDaemonSet(ctx context.Context) error {
	daemonSet := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "service-monitor",
			Namespace: "default",
		},
	}
	if err := r.Delete(ctx, daemonSet); err != nil && !errors.IsNotFound(err) {
		//r.logger(&reconcile.Request{}).Error(err, "daeomon service error")
		return err
	}
	return nil
}

// Fetch status of multiple services from pod logs
func (r *DaemonServiceReconciler) getMultiServiceStatusFromPod(ctx context.Context, pod corev1.Pod) (monitoringv1alpha1.NodeServiceStatus, error) {
	logs := &corev1.PodLogOptions{}
	cfg, err := config.GetConfig()
	if err != nil {
		//r.logger(&reconcile.Request{}).Error(err, "daeomon service error")
		return monitoringv1alpha1.NodeServiceStatus{}, err
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		//r.logger(&reconcile.Request{}).Error(err, "daeomon service error")
		return monitoringv1alpha1.NodeServiceStatus{}, err
	}
	req := clientset.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, logs)
	logStream, err := req.Stream(ctx)
	if err != nil {
		//r.logger(&reconcile.Request{}).Error(err, "daeomon service error")
		return monitoringv1alpha1.NodeServiceStatus{}, err
	}
	defer logStream.Close()

	buf := new(bytes.Buffer)
	if _, err := io.Copy(buf, logStream); err != nil {
		//r.logger(&reconcile.Request{}).Error(err, "daeomon service error")
		return monitoringv1alpha1.NodeServiceStatus{}, err
	}

	logOutput := buf.String()

	// Parse the JSON output from the pod log
	var parsedLog struct {
		Node     string                             `json:"node"`
		Services []monitoringv1alpha1.ServiceStatus `json:"services"`
	}

	if err := json.Unmarshal([]byte(logOutput), &parsedLog); err != nil {
		//r.logger(&reconcile.Request{}).Error(err, "daeomon service error")
		return monitoringv1alpha1.NodeServiceStatus{}, err
	}

	return monitoringv1alpha1.NodeServiceStatus{
		NodeName:       parsedLog.Node,
		ServicesStatus: parsedLog.Services,
	}, nil
}

func convertServiceNames(services []string) []string {
	converted := make([]string, len(services))
	for i, service := range services {
		// Surround the first character with square brackets
		if len(service) > 0 {
			converted[i] = fmt.Sprintf("[%c]%s", service[0], service[1:])
		}
	}
	return converted
}

// SetupWithManager sets up the controller with the Manager.
func (r *DaemonServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&monitoringv1alpha1.DaemonService{}).
		Complete(r)
}
