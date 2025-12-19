/*
Copyright 2025 frederikpietzko.

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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"

	appsV1 "k8s.io/api/apps/v1"
	coreV1 "k8s.io/api/core/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	webappv1alpha1 "github.com/frederikpietzko/operator.git/api/v1alpha1"
)

// WebappReconciler reconciles a Webapp object
type WebappReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

const (
	MANAGED_LABEL  = "webapp.kops.io/managed"
	REVISION_LABEL = "webapp.kops.io/revision-hash"
)

// +kubebuilder:rbac:groups=webapp.kops.io,resources=webapps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=webapp.kops.io,resources=webapps/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=webapp.kops.io,resources=webapps/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Webapp object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.22.4/pkg/reconcile
func (r *WebappReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := logf.FromContext(ctx)

	webapp := &webappv1alpha1.Webapp{}
	if err := r.Client.Get(ctx, req.NamespacedName, webapp); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	l.Info("webapp", "Name", webapp.Name, "Namespace", webapp.Namespace)
	stringHash, err := calcHash(webapp)
	if err != nil {
		return ctrl.Result{}, err
	}

	if value, ok := webapp.GetLabels()[MANAGED_LABEL]; !ok || value == "false" {
		webapp.Labels[MANAGED_LABEL] = "true"
		webapp.Labels[REVISION_LABEL] = *stringHash
	}

	if value, _ := webapp.GetLabels()[REVISION_LABEL]; value != *stringHash {
		if err := r.Update(ctx, webapp); err != nil {
			l.Error(err, "Failed Updating Webapp Labels")
			return ctrl.Result{}, err
		}
		l.Info("Updated Webapp Labels", "webapp", webapp)
	}

	deployment := &appsV1.Deployment{}
	if err := r.upsertDeployment(ctx, req, deployment, webapp, *stringHash); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *WebappReconciler) upsertDeployment(ctx context.Context, req ctrl.Request, deployment *appsV1.Deployment, webapp *webappv1alpha1.Webapp, stringHash string) error {
	l := logf.FromContext(ctx)
	if err := r.Client.Get(ctx, req.NamespacedName, deployment); apierrors.IsNotFound(err) {
		if err := update(deployment, webapp, stringHash); err != nil {
			l.Error(err, "Failed to convert webapp to deployment!")
			return err
		}
		if err := r.Client.Create(ctx, deployment); err != nil {
			l.Error(err, "Failed creating Webapp deployment!")
			return err
		}
		l.Info("Created webapp deployment", "webapp", webapp, "deployment", deployment)

	} else if err != nil {
		return err
	} else if deployment.Labels[REVISION_LABEL] != stringHash {
		if err := update(deployment, webapp, stringHash); err != nil {
			l.Error(err, "Failed to convert webapp to deployment!")
			return err
		}
		if err := r.Client.Update(ctx, deployment); err != nil {
			l.Error(err, "Failed updating Webapp deployment!")
			return err
		}
		l.Info("Updated webapp deployment", "webapp", webapp, "deployment", deployment)
	}

	return nil
}

func update(deployment *appsV1.Deployment, webapp *webappv1alpha1.Webapp, hash string) error {
	deployment.APIVersion = "apps/v1"
	deployment.Kind = "Deployment"
	deployment.Name = webapp.Name
	selectorLabels := map[string]string{
		"webapp.kops.io/managed": "true",
		"app":                    webapp.Name,
	}
	podLabels := map[string]string{
		"webapp.kops.io/managed":       "true",
		"webapp.kops.io/revision-hash": string(hash[:]),
		"app":                          webapp.Name,
	}
	deployment.Labels = podLabels
	deployment.Namespace = webapp.Namespace
	deployment.Spec = appsV1.DeploymentSpec{
		Selector: &metaV1.LabelSelector{
			MatchLabels: selectorLabels,
		},
		Template: coreV1.PodTemplateSpec{
			ObjectMeta: metaV1.ObjectMeta{
				Labels: podLabels,
			},
		},
	}
	var ports []coreV1.ContainerPort
	for _, port := range webapp.Spec.Ports {
		port, err := strconv.ParseInt(port, 10, 32)
		if err != nil {
			return err
		}
		ports = append(ports, coreV1.ContainerPort{
			ContainerPort: int32(port),
		})
	}
	deployment.Spec.Template.Spec = coreV1.PodSpec{
		Containers: []coreV1.Container{
			{
				Name:  webapp.Name,
				Image: webapp.Spec.Image.Name,
				Ports: ports,
			},
		},
	}
	return nil
}

func calcHash(webapp *webappv1alpha1.Webapp) (*string, error) {
	bytes, err := json.Marshal(webapp)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(bytes)
	stringHash := hex.EncodeToString(hash[:])[:8]
	return &stringHash, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *WebappReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&webappv1alpha1.Webapp{}).
		Named("webapp").
		Complete(r)
}
