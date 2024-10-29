package controller

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"time"

	"crypto/sha256"
	"encoding/hex"

	monitoringv1alpha1 "github.com/bharath-rafay/security-operator/api/v1alpha1"
	difflib "github.com/sergi/go-diff/diffmatchpatch"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

// MonitoringServiceReconciler reconciles a MonitoringService object
type MonitoringServiceReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// logger func(request *reconcile.Request) logr.Logger
}

//+kubebuilder:rbac:groups=monitoring.example.com,resources=MonitoringServices,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=monitoring.example.com,resources=MonitoringServices/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=monitoring.example.com,resources=MonitoringServices/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the MonitoringService object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.16.3/pkg/reconcile
func (r *MonitoringServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var MonitoringService monitoringv1alpha1.MonitoringService
	if err := r.Get(ctx, req.NamespacedName, &MonitoringService); err != nil {
		//r.logger(&reconcile.Request{}).Error(err, "daeomon service error")
		return ctrl.Result{RequeueAfter: time.Minute * 5}, client.IgnoreNotFound(err)
	}

	// Step 2: Monitor Kubernetes core components if enabled
	k8sNodeStatuses := []monitoringv1alpha1.K8sMonitorStatus{}
	NodeServiceStatus := []monitoringv1alpha1.NodeServiceMonitorStatus{}
	if MonitoringService.Spec.K8sMonitor.Enabled {
		coreStatuses, err := r.checkK8sCoreComponents(ctx)
		if err != nil {
			//r.logger(&reconcile.Request{}).Error(err, "daeomon service error")
			return ctrl.Result{RequeueAfter: time.Minute * 5}, err
		}
		k8sNodeStatuses = append(k8sNodeStatuses, coreStatuses...)
	}

	// Step 3: Deploy the DaemonSet to monitor custom daemon services if needed
	if MonitoringService.Spec.NodeServiceMonitor.Enabled && len(MonitoringService.Spec.NodeServiceMonitor.Services) > 0 {
		if err := r.deployDaemonSet(ctx, MonitoringService); err != nil {
			//r.logger(&reconcile.Request{}).Error(err, "daeomon service error")
			return ctrl.Result{RequeueAfter: time.Minute * 5}, err
		}

		podList := &corev1.PodList{}
		labelSelector := labels.SelectorFromSet(labels.Set{"app": "nsenter-daemon"})
		if err := r.List(ctx, podList, &client.ListOptions{LabelSelector: labelSelector}); err != nil {
			//r.logger(&reconcile.Request{}).Error(err, "daeomon service error")
			return ctrl.Result{RequeueAfter: time.Minute * 5}, err
		}

		for _, pod := range podList.Items {
			nodeStatus, err := r.getMultiServiceStatusFromPod(ctx, pod, MonitoringService.Spec.NodeServiceMonitor.Services)
			if err != nil {
				//r.logger(&reconcile.Request{}).Error(err, "daeomon service error")
				return ctrl.Result{RequeueAfter: time.Minute * 5}, err
			}
			NodeServiceStatus = append(NodeServiceStatus, nodeStatus)
		}
	}

	configStatuses, err := r.checkK8sCorePodChanges(ctx, &MonitoringService)
	if err != nil {
		//r.logger(&reconcile.Request{}).Error(err, "daeomon service error")
		return ctrl.Result{RequeueAfter: time.Minute * 5}, err
	}

	// Step 4: Update the status with results from custom services, core components, and config changes
	MonitoringService.Status.K8sMonitorStatus = k8sNodeStatuses
	MonitoringService.Status.NodeServiceMonitorStatus = NodeServiceStatus
	MonitoringService.Status.K8sConfigDriftStatus = configStatuses
	if err := r.Status().Update(ctx, &MonitoringService); err != nil {
		//r.logger(&reconcile.Request{}).Error(err, "daeomon service error")
		return ctrl.Result{RequeueAfter: time.Minute * 5}, err
	}

	// Requeue after a specified interval
	return ctrl.Result{RequeueAfter: time.Minute * 5}, nil
}

func (r *MonitoringServiceReconciler) checkK8sCorePodChanges(ctx context.Context, MonitoringService *monitoringv1alpha1.MonitoringService) ([]monitoringv1alpha1.K8sConfigDriftStatus, error) {
	var configStatuses []monitoringv1alpha1.K8sConfigDriftStatus

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
		currentChecksum, err := computePodSpecChecksum(pod, componentName)
		if err != nil {
			//r.logger(&reconcile.Request{}).Error(err, "daeomon service error")
			return nil, err
		}

		var oldChecksum string
		var configChanged bool

		// Check the previously stored checksum in the status
		for _, storedConfig := range MonitoringService.Status.K8sConfigDriftStatus {
			if storedConfig.ComponentName == componentName && storedConfig.OldChecksum == "" {
				oldChecksum = storedConfig.NewChecksum
				configChanged = (currentChecksum != oldChecksum)
				break
			} else if storedConfig.ComponentName == componentName && storedConfig.OldChecksum != "" {
				oldChecksum = storedConfig.OldChecksum
				configChanged = (currentChecksum != storedConfig.OldChecksum)
				break
			}
		}

		// If configuration changed, store the new checksum and mark as changed
		if configChanged {
			diff, err := r.generatePodConfigDiff(pod, componentName)
			if err != nil {
				//r.logger(&reconcile.Request{}).Error(err, "daeomon service error")
				return nil, err
			}
			configStatuses = append(configStatuses, monitoringv1alpha1.K8sConfigDriftStatus{
				ComponentName:        componentName,
				ConfigChanged:        true,
				OldChecksum:          oldChecksum,
				NewChecksum:          currentChecksum,
				ChangedConfiguration: diff, // Include the diff or relevant changed fields
			})
		} else {
			configStatuses = append(configStatuses, monitoringv1alpha1.K8sConfigDriftStatus{
				ComponentName: componentName,
				ConfigChanged: false,
				NewChecksum:   currentChecksum,
			})
		}
	}

	return configStatuses, nil
}

// Generate a diff for the pod spec (or just include relevant fields for simplicity)
func (r *MonitoringServiceReconciler) generatePodConfigDiff(pod *corev1.Pod, name string) (string, error) {
	// relevantSpec := struct {
	// 	Containers []corev1.Container `json:"containers"`
	// 	Volumes    []corev1.Volume    `json:"volumes"`
	// }{
	// 	Containers: pod.Spec.Containers,
	// 	Volumes:    pod.Spec.Volumes,
	// }

	// Return the relevant parts of the pod spec as a diff (could be more sophisticated)
	podSpecBytes, err := json.Marshal(pod)
	if err != nil {
		//r.logger(&reconcile.Request{}).Error(err, "daeomon service error")
		return "", err
	}
	oldSpecBytes, err := getOldData(name)
	if err != nil {
		//r.logger(&reconcile.Request{}).Error(err, "daeomon service error")
		return "", err
	}

	return getDiff(string(oldSpecBytes), string(podSpecBytes))
}

func (r *MonitoringServiceReconciler) deployDaemonSet(ctx context.Context, MonitoringService monitoringv1alpha1.MonitoringService) error {
	// serviceList := convertServiceNames(MonitoringService.Spec.NodeServiceMonitor.Services)
	daemonSet := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nsenter-daemon",
			Namespace: "default",
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "nsenter-daemon",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "nsenter-daemon",
					},
				},
				Spec: corev1.PodSpec{
					HostPID:     true,
					HostNetwork: true,
					Tolerations: []corev1.Toleration{
						{
							Operator: corev1.TolerationOpExists,
						},
					},
					Containers: []corev1.Container{
						{
							Name:    "nsenter",
							Image:   "alexeiled/nsenter",
							Command: []string{"/nsenter", "--all", "--target=1", "--", "su", "-"},
							Stdin:   true,
							TTY:     true,
							SecurityContext: &corev1.SecurityContext{
								Privileged: pointer.Bool(true),
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("10m"),
								},
							},
						},
					},
				},
			},
		},
	}

	if err := r.Client.Create(ctx, daemonSet); err != nil && !errors.IsAlreadyExists(err) {
		return err
	}

	return nil
}

// Helper function to compute checksum from pod spec (relevant fields)
func computePodSpecChecksum(pod *corev1.Pod, name string) (string, error) {
	// Extract relevant fields from the pod spec (command, args, env, etc.)
	file := "/data/" + name + ".yaml"
	// Serialize the relevant fields to JSON
	podSpecBytes, err := json.Marshal(pod)
	if err != nil {
		return "", err
	}
	if !fileExists(file) {
		err = writeYAMLToFile(file, podSpecBytes)
		if err != nil {
			return "", err
		}
	}
	// Calculate the checksum (hash)
	hash := sha256.Sum256(podSpecBytes)
	return hex.EncodeToString(hash[:]), nil
}

// Function to retrieve the pod for a specific component (e.g., kube-apiserver)
func (r *MonitoringServiceReconciler) getCoreComponentPod(ctx context.Context, componentName string) (*corev1.Pod, error) {
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

func (r *MonitoringServiceReconciler) checkK8sCoreComponents(ctx context.Context) ([]monitoringv1alpha1.K8sMonitorStatus, error) {
	var coreStatuses []monitoringv1alpha1.K8sMonitorStatus

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

			coreStatuses = append(coreStatuses, monitoringv1alpha1.K8sMonitorStatus{
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

func (r *MonitoringServiceReconciler) waitForDaemonSetReady(ctx context.Context) error {
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

// func (r *MonitoringServiceReconciler) deleteDaemonSet(ctx context.Context) error {
// 	daemonSet := &appsv1.DaemonSet{
// 		ObjectMeta: metav1.ObjectMeta{
// 			Name:      "service-monitor",
// 			Namespace: "default",
// 		},
// 	}
// 	if err := r.Delete(ctx, daemonSet); err != nil && !errors.IsNotFound(err) {
// 		//r.logger(&reconcile.Request{}).Error(err, "daeomon service error")
// 		return err
// 	}
// 	return nil
// }

// Fetch status of multiple services from pod logs
func (r *MonitoringServiceReconciler) getMultiServiceStatusFromPod(ctx context.Context, pod corev1.Pod, serviceList []string) (monitoringv1alpha1.NodeServiceMonitorStatus, error) {
	var status []monitoringv1alpha1.ServiceStatus
	var command []string
	cfg, err := config.GetConfig()
	if err != nil {
		//r.logger(&reconcile.Request{}).Error(err, "daeomon service error")
		return monitoringv1alpha1.NodeServiceMonitorStatus{}, err
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		//r.logger(&reconcile.Request{}).Error(err, "daeomon service error")
		return monitoringv1alpha1.NodeServiceMonitorStatus{}, err
	}
	for _, service := range serviceList {
		command = []string{"systemctl", "is-active", service}
		req := clientset.CoreV1().RESTClient().Post().Resource("pods").
			Namespace(pod.Namespace).
			Name(pod.Name).
			SubResource("exec").
			Param("container", "nsenter").
			Param("stdout", "true").
			Param("stderr", "true").
			Param("tty", "true")

		for _, cmd := range command {
			req.Param("command", cmd)
		}
		exec, err := remotecommand.NewSPDYExecutor(cfg, "POST", req.URL())
		if err != nil {
			return monitoringv1alpha1.NodeServiceMonitorStatus{}, err
		}
		var stdout, stderr bytes.Buffer
		err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
			Stdout: bufio.NewWriter(&stdout),
			Stderr: bufio.NewWriter(&stderr),
		})

		if err != nil {
			status = append(status, monitoringv1alpha1.ServiceStatus{
				ServiceName:   service,
				ServiceStatus: stdout.String(),
			})
		} else if stderr.Len() == 0 {
			status = append(status, monitoringv1alpha1.ServiceStatus{
				ServiceName:   service,
				ServiceStatus: stdout.String(),
			})
		}

	}
	return monitoringv1alpha1.NodeServiceMonitorStatus{
		NodeName:       pod.Spec.NodeName,
		ServicesStatus: status,
	}, nil

}

// SetupWithManager sets up the controller with the Manager.
func (r *MonitoringServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&monitoringv1alpha1.MonitoringService{}).
		Complete(r)

}

func fileExists(filename string) bool {
	_, err := os.Stat(filename)
	return !os.IsNotExist(err)
}

func writeYAMLToFile(filename string, data []byte) error {

	// Write the YAML data to the file
	err := ioutil.WriteFile(filename, data, 0644)
	if err != nil {
		return fmt.Errorf("error writing to file: %v", err)
	}

	fmt.Println("File created and data written successfully.")
	return nil
}

func getOldData(name string) ([]byte, error) {
	filename := "/data/" + name + ".yaml"

	fileBytes, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("error reading file: %v", err)
	}

	return fileBytes, nil

}

func getDiff(oldContent, newContent string) (string, error) {

	dmp := difflib.New()

	diffs := dmp.DiffMain(oldContent, newContent, false)
	diffText := dmp.DiffPrettyText(diffs)

	return diffText, nil
}
