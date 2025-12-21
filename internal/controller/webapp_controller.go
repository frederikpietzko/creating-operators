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
	"errors"
	"fmt"
	"slices"
	"strconv"

	appsV1 "k8s.io/api/apps/v1"
	coreV1 "k8s.io/api/core/v1"
	networkingV1 "k8s.io/api/networking/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	webappv1alpha1 "github.com/frederikpietzko/operator.git/api/v1alpha1"
)

// WebappReconciler reconciles a Webapp object
type WebappReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

const (
	MANAGED_LABEL           = "webapp.kops.io/managed"
	REVISION_LABEL          = "webapp.kops.io/revision-hash"
	GLOBAL_WEBAPP_NAMESPACE = "webbapps-global"
	GLOBAL_INGRESS_NAME     = "global-webapp-ingress"
	WEBAPP_FINALIZER        = "webapp.kops.io/finalizer"
)

// +kubebuilder:rbac:groups=webapp.kops.io,resources=webapps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=webapp.kops.io,resources=webapps/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=webapp.kops.io,resources=webapps/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=namespaces,verbs=get;list;watch;update;patch;create;delete

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
	hash, err := calcHash(webapp)
	if err != nil {
		return ctrl.Result{}, err
	}

	if webapp.Labels == nil {
		webapp.Labels = make(map[string]string)
	}

	if webapp.Labels[REVISION_LABEL] != hash {
		webapp.Labels[MANAGED_LABEL] = "true"
		webapp.Labels[REVISION_LABEL] = hash
		if err := r.Update(ctx, webapp); err != nil {
			l.Error(err, "Failed Updating Webapp Labels")
			return ctrl.Result{}, err
		}
		l.Info("Updated Webapp Labels", "webapp", webapp)
		return ctrl.Result{}, err
	}

	if !controllerutil.ContainsFinalizer(webapp, WEBAPP_FINALIZER) {
		controllerutil.AddFinalizer(webapp, WEBAPP_FINALIZER)
		if err := r.Client.Update(ctx, webapp); err != nil {
			return ctrl.Result{}, err
		}
	}

	if err := r.cerateWebbappGlobalNamespace(ctx); err != nil {
		l.Error(err, "Unable to create Global Namespace")
		return ctrl.Result{}, err
	}

	ingress, created, err := r.createIngress(ctx, hash)
	if err != nil {
		l.Error(err, "Unable to create Global Ingress")
		return ctrl.Result{}, err
	}

	// FINALIZER LOGIC
	if isWebappMarkedToBeDeleted := webapp.GetDeletionTimestamp() != nil; isWebappMarkedToBeDeleted {
		if controllerutil.ContainsFinalizer(webapp, WEBAPP_FINALIZER) {
			if err := r.finalizeWebapp(ctx, webapp, ingress); err != nil {
				return ctrl.Result{}, err
			}
			controllerutil.RemoveFinalizer(webapp, WEBAPP_FINALIZER)
			if err := r.Update(ctx, webapp); err != nil {
				return ctrl.Result{}, err
			}
			// Exit if this was a deletion event
			return ctrl.Result{}, nil
		}
	}

	deployment := &appsV1.Deployment{}
	if err := r.upsertDeployment(ctx, req, deployment, webapp, hash); err != nil {
		return ctrl.Result{}, err
	}

	service := &coreV1.Service{}
	if err := r.upsertService(ctx, service, webapp, hash); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.upsertProxyService(ctx, service, webapp, hash); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.updateIngress(ctx, ingress, !created, hash); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *WebappReconciler) finalizeWebapp(ctx context.Context, webapp *webappv1alpha1.Webapp, ingress *networkingV1.Ingress) error {
	l := logf.FromContext(ctx)
	proxyServiceName := proxyServiceNameFromWebapp(webapp)
	namespace := GLOBAL_WEBAPP_NAMESPACE
	prxyService := &coreV1.Service{}
	if err := r.Client.Get(ctx, client.ObjectKey{Name: proxyServiceName, Namespace: namespace}, prxyService); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if err := r.Client.Delete(ctx, prxyService); err != nil {
		l.Info("Deleting Local Proxy Service", "serivce", prxyService)
		return err
	}
	count := 0

	for j, rule := range ingress.Spec.Rules {
		for i, path := range rule.HTTP.Paths {
			if path.Backend.Service.Name != proxyServiceName {
				count += 1
			}
			if path.Backend.Service.Name == proxyServiceName {
				rule.HTTP.Paths = slices.Delete(rule.HTTP.Paths, i, i+1)
			}
		}
		if len(rule.HTTP.Paths) == 0 {
			ingress.Spec.Rules = slices.Delete(ingress.Spec.Rules, j, j+1)
		}
	}
	if len(ingress.Spec.Rules) == 0 {
		l.Info("Ingress would be empty after update, deleting...", "ingress", ingress)
		return r.Client.Delete(ctx, ingress)
	}
	l.Info("Updating Ingress", "ingress", ingress)
	return r.Client.Update(ctx, ingress)
}

func (r *WebappReconciler) upsertDeployment(ctx context.Context, req ctrl.Request, deployment *appsV1.Deployment, webapp *webappv1alpha1.Webapp, hash string) error {
	l := logf.FromContext(ctx)
	err := r.Client.Get(ctx, toDeploymentObjectKey(webapp), deployment)

	if apierrors.IsNotFound(err) {
		if err := r.constructDeployment(deployment, webapp, hash); err != nil {
			l.Error(err, "Failed to convert webapp to deployment!")
			return err
		}
		if err := ctrl.SetControllerReference(webapp, deployment, r.Scheme); err != nil {
			return err
		}
		if err := r.Client.Create(ctx, deployment); err != nil {
			l.Error(err, "Failed creating Webapp deployment!")
			return err
		}
		l.Info("Created webapp deployment", "webapp", webapp, "deployment", deployment)
		return nil

	}
	if err != nil {
		return err
	}
	if deployment.Labels[REVISION_LABEL] != hash {
		if err := r.updateDeploymentFields(deployment, webapp, hash); err != nil {
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

func (r *WebappReconciler) constructDeployment(deployment *appsV1.Deployment, webapp *webappv1alpha1.Webapp, hash string) error {
	deployment.ObjectMeta = metaV1.ObjectMeta{
		Name:      deploymentName(webapp),
		Namespace: webapp.Namespace,
		Labels: map[string]string{
			MANAGED_LABEL:  "true",
			REVISION_LABEL: hash,
		},
	}

	selectorLabels := map[string]string{
		"app": webapp.Name,
	}

	deployment.Spec = appsV1.DeploymentSpec{
		Selector: &metaV1.LabelSelector{
			MatchLabels: selectorLabels,
		},
		Template: coreV1.PodTemplateSpec{
			ObjectMeta: metaV1.ObjectMeta{},
		},
	}

	return r.updateDeploymentFields(deployment, webapp, hash)
}

func (r *WebappReconciler) updateDeploymentFields(deployment *appsV1.Deployment, webapp *webappv1alpha1.Webapp, hash string) error {
	if deployment.Labels == nil {
		deployment.Labels = make(map[string]string)
	}
	deployment.Labels[REVISION_LABEL] = hash
	deployment.Labels[MANAGED_LABEL] = "true"

	podLabels := map[string]string{
		MANAGED_LABEL:  "true",
		REVISION_LABEL: string(hash[:]),
		"app":          webapp.Name,
	}
	deployment.Spec.Template.ObjectMeta.Labels = podLabels

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

func (r *WebappReconciler) upsertService(ctx context.Context, service *coreV1.Service, webapp *webappv1alpha1.Webapp, hash string) error {
	service.Name = serviceName(webapp)
	service.Namespace = webapp.Namespace
	service.Labels = map[string]string{
		"app":          webapp.Name,
		MANAGED_LABEL:  "true",
		REVISION_LABEL: hash,
	}

	ports, err := mapPorts(webapp)
	if err != nil {
		return err
	}

	service.Spec = coreV1.ServiceSpec{
		Selector: map[string]string{
			"app": webapp.Name,
		},
		Ports: ports,
		Type:  coreV1.ServiceTypeClusterIP,
	}
	if err := ctrl.SetControllerReference(webapp, service, r.Scheme); err != nil {
		return err
	}

	existingService := &coreV1.Service{}
	err = r.Client.Get(ctx, client.ObjectKey{Namespace: webapp.Namespace, Name: service.Name}, existingService)
	if apierrors.IsNotFound(err) {
		return r.Client.Create(ctx, service)
	}
	if existingService.Spec.Type != service.Spec.Type {
		return errors.New("changing service.spec.type is not possible")
	}
	service.ResourceVersion = existingService.ResourceVersion
	return r.Client.Update(ctx, service)
}

func mapPorts(webapp *webappv1alpha1.Webapp) ([]coreV1.ServicePort, error) {
	ports := []coreV1.ServicePort{}
	for _, port := range webapp.Spec.Ports {
		literalPort, err := strconv.ParseInt(port, 10, 32)
		if err != nil {
			return ports, err
		}
		p := coreV1.ServicePort{
			Name:       "http",
			Port:       int32(literalPort),
			TargetPort: intstr.FromInt(int(literalPort)),
			Protocol:   coreV1.ProtocolTCP,
		}
		ports = append(ports, p)
	}
	return ports, nil
}

func proxyServiceNameFromServiceName(serviceName string) string {
	return serviceName + "-proxy"
}

func proxyServiceNameFromWebapp(webapp *webappv1alpha1.Webapp) string {
	return proxyServiceNameFromServiceName(serviceName(webapp))
}

func (r *WebappReconciler) upsertProxyService(ctx context.Context, serivce *coreV1.Service, webapp *webappv1alpha1.Webapp, hash string) error {
	proxyService := &coreV1.Service{}
	proxyService.Labels = map[string]string{
		MANAGED_LABEL:  "true",
		REVISION_LABEL: hash,
	}
	proxyService.Name = proxyServiceNameFromServiceName(serivce.Name)
	proxyService.Namespace = GLOBAL_WEBAPP_NAMESPACE
	ports, err := mapPorts(webapp)
	if err != nil {
		return err
	}

	proxyService.Spec = coreV1.ServiceSpec{
		Type:         coreV1.ServiceTypeExternalName,
		ExternalName: fmt.Sprintf("%s.%s.svc.cluster.local", serivce.Name, serivce.Namespace),
		Ports:        ports,
	}

	existingProxyService := &coreV1.Service{}
	err = r.Client.Get(ctx, client.ObjectKey{Name: proxyService.Name, Namespace: proxyService.Namespace}, existingProxyService)
	if apierrors.IsNotFound(err) {
		return r.Client.Create(ctx, proxyService)
	}
	proxyService.ResourceVersion = existingProxyService.ResourceVersion
	return r.Client.Update(ctx, proxyService)
}

func (r *WebappReconciler) cerateWebbappGlobalNamespace(ctx context.Context) error {
	name := GLOBAL_WEBAPP_NAMESPACE
	namespace := &coreV1.Namespace{}
	err := r.Client.Get(ctx, client.ObjectKey{Name: name}, namespace)
	if apierrors.IsNotFound(err) {
		namespace.APIVersion = "v1"
		namespace.Kind = "Namespace"
		namespace.Name = name
		if err := r.Client.Create(ctx, namespace); err != nil {
			return err
		}
		return nil
	}
	if err != nil {
		return err
	}
	return nil
}

func (r *WebappReconciler) createIngress(ctx context.Context, hash string) (*networkingV1.Ingress, bool, error) {
	name := GLOBAL_INGRESS_NAME
	namespace := GLOBAL_WEBAPP_NAMESPACE

	ingress := &networkingV1.Ingress{}
	err := r.Client.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, ingress)
	if apierrors.IsNotFound(err) {
		ingress.APIVersion = "v1"
		ingress.Name = name
		ingress.Namespace = namespace
		ingress.Labels = map[string]string{
			MANAGED_LABEL:  "true",
			REVISION_LABEL: hash,
		}
		ingress.Spec = networkingV1.IngressSpec{
			Rules: []networkingV1.IngressRule{},
		}
		return ingress, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	return ingress, false, nil
}

func (r *WebappReconciler) updateIngress(ctx context.Context, ingress *networkingV1.Ingress, present bool, hash string) error {
	l := logf.FromContext(ctx)
	ingress.Labels[REVISION_LABEL] = hash
	webappsList := &webappv1alpha1.WebappList{}
	if err := r.Client.List(ctx, webappsList); err != nil {
		return err
	}
	webappsByHost := map[string][]webappv1alpha1.Webapp{}

	for _, webapp := range webappsList.Items {
		if webapp.Spec.Ingress == nil || webapp.DeletionTimestamp != nil {
			continue
		}
		host := webapp.Spec.Ingress.Host
		if _, ok := webappsByHost[host]; !ok {
			webappsByHost[host] = []webappv1alpha1.Webapp{}
		}
		webappsByHost[host] = append(webappsByHost[host], webapp)
	}

	pathType := networkingV1.PathTypePrefix

	rules := []networkingV1.IngressRule{}
	for host, webapps := range webappsByHost {
		rule := networkingV1.IngressRule{
			Host: host,
			IngressRuleValue: networkingV1.IngressRuleValue{
				HTTP: &networkingV1.HTTPIngressRuleValue{
					Paths: []networkingV1.HTTPIngressPath{},
				},
			},
		}
		for _, webapp := range webapps {
			firstPort, err := strconv.ParseInt(webapp.Spec.Ports[0], 10, 32)
			if err != nil {
				return err
			}
			path := networkingV1.HTTPIngressPath{
				Path:     webapp.Spec.Ingress.Path,
				PathType: &pathType,
				Backend: networkingV1.IngressBackend{
					Service: &networkingV1.IngressServiceBackend{
						Name: proxyServiceNameFromWebapp(&webapp),
						Port: networkingV1.ServiceBackendPort{
							Number: int32(firstPort),
						},
					},
				},
			}
			rule.IngressRuleValue.HTTP.Paths = append(rule.IngressRuleValue.HTTP.Paths, path)
		}
		rules = append(rules, rule)
	}
	ingress.Spec.Rules = rules
	if present {
		l.Info("Updating Ingress", "ingress", ingress)
		return r.Client.Update(ctx, ingress)
	}

	l.Info("Deleting Ingress", "ingress", ingress)
	return r.Client.Create(ctx, ingress)
}

func calcHash(webapp *webappv1alpha1.Webapp) (string, error) {
	bytes, err := json.Marshal(webapp.Spec)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(bytes)
	return hex.EncodeToString(hash[:])[:8], nil
}

func toSerivceObjectKey(w *webappv1alpha1.Webapp) client.ObjectKey {
	return client.ObjectKey{Name: serviceName(w), Namespace: w.Namespace}
}

func toDeploymentObjectKey(w *webappv1alpha1.Webapp) client.ObjectKey {
	return client.ObjectKey{Name: deploymentName(w), Namespace: w.Namespace}
}

func serviceName(webapp *webappv1alpha1.Webapp) string {
	return fmt.Sprintf("%s-service", webapp.Name)
}

func deploymentName(webapp *webappv1alpha1.Webapp) string {
	return fmt.Sprintf("%s-deployment", webapp.Name)
}

// SetupWithManager sets up the controller with the Manager.
func (r *WebappReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&webappv1alpha1.Webapp{}).
		Named("webapp").
		Complete(r)
}
