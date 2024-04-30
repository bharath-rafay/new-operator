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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	mygroupv1 "github.com/bharath-rafay/security-operator/api/v1"
)

// SecurityPolicyReconciler reconciles a SecurityPolicy object
type SecurityPolicyReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=mygroup.example.com,resources=securitypolicies,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=mygroup.example.com,resources=securitypolicies/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=mygroup.example.com,resources=securitypolicies/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the SecurityPolicy object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.16.3/pkg/reconcile
func (r *SecurityPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_log := log.FromContext(ctx)
	// Fetch the SecurityPolicy instance
	securityPolicy := &mygroupv1.SecurityPolicy{}
	err := r.Get(ctx, req.NamespacedName, securityPolicy)
	if err != nil {
		if errors.IsNotFound(err) {
			// Object not found, likely deleted, return and don't requeue
			_log.Info("SecurityPolicy resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		_log.Error(err, "Failed to get SecurityPolicy")
		return ctrl.Result{}, err
	}

	// Implement your reconcile logic here.
	// For simplicity, let's delete any pods created.

	podList := &corev1.PodList{}
	err = r.List(ctx, podList, client.InNamespace(req.Namespace))
	if err != nil {
		_log.Error(err, "Failed to list pods")
		return ctrl.Result{}, err
	}

	for _, pod := range podList.Items {
		err := r.Delete(ctx, &pod)
		if err != nil {
			_log.Error(err, "Failed to delete pod", "pod", pod.Name)
			return ctrl.Result{}, err
		}
		_log.Info("Deleted pod", "pod", pod.Name)
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SecurityPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&mygroupv1.SecurityPolicy{}).
		Complete(r)
}
