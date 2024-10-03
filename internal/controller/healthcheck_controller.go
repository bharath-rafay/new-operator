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
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	monitoringv1alpha1 "github.com/myuser/health-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

// HealthCheckReconciler reconciles a HealthCheck object
type HealthCheckReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=monitoring.mydomain.com,resources=healthchecks,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=monitoring.mydomain.com,resources=healthchecks/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=monitoring.mydomain.com,resources=healthchecks/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the HealthCheck object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.16.3/pkg/reconcile
func (r *HealthCheckReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var healthCheck monitoringv1alpha1.HealthCheck
	if err := r.Get(ctx, req.NamespacedName, &healthCheck); err != nil {
		logger.Error(err, "unable to fetch HealthCheck")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Initialize status updates
	updatedStatus := healthCheck.Status

	// Check Kubernetes Scheduler
	if healthCheck.Spec.Scheduler {
		if err := r.checkSchedulerHealth(ctx); err != nil {
			logger.Error(err, "Scheduler is unhealthy")
			updatedStatus.SchedulerStatus = "Unhealthy"
		} else {
			updatedStatus.SchedulerStatus = "Healthy"
			logger.Info("Scheduler is healthy")
		}
	}

	// Check Kubernetes Controller
	if healthCheck.Spec.Controller {
		if err := r.checkControllerHealth(ctx); err != nil {
			logger.Error(err, "Controller Manager is unhealthy")
			updatedStatus.ControllerStatus = "Unhealthy"
		} else {
			updatedStatus.ControllerStatus = "Healthy"
			logger.Info("Controller Manager is healthy")
		}
	}

	// Check custom services
	var serviceStatuses []monitoringv1alpha1.ServiceHealthStatus
	for _, service := range healthCheck.Spec.Services {
		var status string
		if err := r.checkServiceHealth(service); err != nil {
			logger.Error(err, "Service is unhealthy", "service", service.Name)
			status = "Unhealthy"
		} else {
			logger.Info("Service is healthy", "service", service.Name)
			status = "Healthy"
		}
		serviceStatuses = append(serviceStatuses, monitoringv1alpha1.ServiceHealthStatus{
			Name:   service.Name,
			Status: status,
		})
	}
	updatedStatus.ServicesStatus = serviceStatuses

	// Update status if it has changed
	if !equalStatuses(healthCheck.Status, updatedStatus) {
		healthCheck.Status = updatedStatus
		if err := r.Status().Update(ctx, &healthCheck); err != nil {
			logger.Error(err, "failed to update HealthCheck status")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// Helper function to compare two statuses for equality
func equalStatuses(a, b monitoringv1alpha1.HealthCheckStatus) bool {
	if a.SchedulerStatus != b.SchedulerStatus || a.ControllerStatus != b.ControllerStatus {
		return false
	}
	if len(a.ServicesStatus) != len(b.ServicesStatus) {
		return false
	}
	for i, svc := range a.ServicesStatus {
		if svc.Status != b.ServicesStatus[i].Status {
			return false
		}
	}
	return true
}

// checkSchedulerHealth checks the health of the Kubernetes scheduler by querying its healthz endpoint
func (r *HealthCheckReconciler) checkSchedulerHealth(ctx context.Context) error {
	// Get the list of scheduler pods in the kube-system namespace
	var podList corev1.PodList
	if err := r.List(ctx, &podList, client.InNamespace("kube-system"), client.MatchingLabels{"component": "kube-scheduler"}); err != nil {
		return fmt.Errorf("unable to list scheduler pods: %v", err)
	}

	// Ensure that there's at least one scheduler pod and check its status
	for _, pod := range podList.Items {
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
				return nil // Pod is ready and healthy
			}
		}
		return fmt.Errorf("scheduler pod %s is not ready", pod.Name)
	}

	return fmt.Errorf("no scheduler pod found")
}

// checkControllerHealth checks the health of the Kubernetes controller manager by querying its healthz endpoint
func (r *HealthCheckReconciler) checkControllerHealth(ctx context.Context) error {
	// Get the list of controller-manager pods in the kube-system namespace
	var podList corev1.PodList
	if err := r.List(ctx, &podList, client.InNamespace("kube-system"), client.MatchingLabels{"component": "kube-controller-manager"}); err != nil {
		return fmt.Errorf("unable to list controller-manager pods: %v", err)
	}

	// Ensure that there's at least one controller-manager pod and check its status
	for _, pod := range podList.Items {
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
				return nil // Pod is ready and healthy
			}
		}
		return fmt.Errorf("controller-manager pod %s is not ready", pod.Name)
	}

	return fmt.Errorf("no controller-manager pod found")
}

// checkComponentHealth checks the health of a Kubernetes component by sending an HTTP GET request to its health endpoint
func (r *HealthCheckReconciler) checkComponentHealth(url string, component string) error {
	client := http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("%s health check failed: %v", component, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s is unhealthy, status code: %d", component, resp.StatusCode)
	}

	return nil
}

// checkServiceHealth checks the health of custom services specified in the HealthCheck CR
func (r *HealthCheckReconciler) checkServiceHealth(service monitoringv1alpha1.ServiceHealth) error {
	client := http.Client{
		Timeout: 5 * time.Second,
	}
	resp, err := client.Get(service.URL)
	if err != nil {
		return fmt.Errorf("service %s health check failed: %v", service.Name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("service %s is unhealthy, status code: %d", service.Name, resp.StatusCode)
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HealthCheckReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&monitoringv1alpha1.HealthCheck{}).
		Complete(r)
}
