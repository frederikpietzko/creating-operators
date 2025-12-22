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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	webappv1alpha1 "github.com/frederikpietzko/operator.git/api/v1alpha1"
	buildv1alpha2 "github.com/pivotal/kpack/pkg/apis/build/v1alpha2"
	buildv1alpha1 "github.com/pivotal/kpack/pkg/apis/core/v1alpha1"
)

// WebappRepositoryReconciler reconciles a WebappRepository object
type WebappRepositoryReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=webapp.kops.io,resources=webapprepositories,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=webapp.kops.io,resources=webapprepositories/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=webapp.kops.io,resources=webapprepositories/finalizers,verbs=update
// +kubebuilder:rbac:groups=kpack.io,resources=images,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kpack.io,resources=images/status,verbs=get

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the WebappRepository object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.22.4/pkg/reconcile
func (r *WebappRepositoryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// TODO: Add logging, something doesn't seem right
	l := logf.FromContext(ctx)
	image := &buildv1alpha2.Image{}

	repo := &webappv1alpha1.WebappRepository{}
	err := r.Client.Get(ctx, req.NamespacedName, repo)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	repoHash, err := calcRepoHash(repo)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = r.Client.Get(ctx, imageObjectKey(repo), image)
	if apierrors.IsNotFound(err) {
		_, err := r.upsertImage(ctx, image, repo, repoHash)
		if err != nil {
			return ctrl.Result{}, err
		}
	}
	if err != nil {
		return ctrl.Result{}, err
	}

	isReady := image.Status.GetCondition(buildv1alpha1.ConditionReady).IsTrue()
	latestImage := image.Status.LatestImage
	if isReady && latestImage != "" {
		l.Info("Upserting webapp with image", "image", image)
		return ctrl.Result{}, r.upsertWebappWithImage(ctx, image, repo)
	}
	l.Info("Waiting for image to become Ready", "image", image)
	return ctrl.Result{}, nil
}

func (r *WebappRepositoryReconciler) upsertWebappWithImage(ctx context.Context, image *buildv1alpha2.Image, repo *webappv1alpha1.WebappRepository) error {
	webapp := &webappv1alpha1.Webapp{}
	webapp.Name = repo.Name + "-webapp"
	webapp.Namespace = repo.Namespace
	webapp.Spec = webappv1alpha1.WebappSpec{
		Image: webappv1alpha1.Image{
			Name: image.Status.LatestImage,
		},
		Ports:   repo.Spec.Tempalte.Spec.Ports,
		Ingress: repo.Spec.Tempalte.Spec.Ingress,
	}
	hash, err := calcHash(webapp)
	if err != nil {
		return err
	}
	webapp.Labels = map[string]string{
		REVISION_LABEL: hash,
		MANAGED_LABEL:  "true",
	}
	if err := ctrl.SetControllerReference(repo, webapp, r.Scheme); err != nil {
		return err
	}

	originalWebapp := &webappv1alpha1.Webapp{}
	if err := r.Client.Get(ctx, webapp.Key(), originalWebapp); apierrors.IsNotFound(err) {
		return r.Client.Create(ctx, webapp)
	} else if err != nil {
		return err
	}
	webapp.ResourceVersion = originalWebapp.ResourceVersion
	return r.Client.Update(ctx, webapp)
}

func (r *WebappRepositoryReconciler) upsertImage(ctx context.Context, image *buildv1alpha2.Image, repo *webappv1alpha1.WebappRepository, repoHash string) (ctrl.Result, error) {
	err := r.Client.Get(ctx, imageObjectKey(repo), image)
	if apierrors.IsNotFound(err) {
		if err := r.constructImage(ctx, image, repo, repoHash); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Client.Create(ctx, image); err != nil {
			return ctrl.Result{}, err
		}
	}
	if image.Labels[REVISION_LABEL] != repoHash {
		if err := r.Client.Delete(ctx, image); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.constructImage(ctx, image, repo, repoHash); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Client.Create(ctx, image); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *WebappRepositoryReconciler) constructImage(ctx context.Context, image *buildv1alpha2.Image, repo *webappv1alpha1.WebappRepository, repoHash string) error {
	image.Name = imageName(repo)
	image.Namespace = repo.Namespace
	image.Labels = map[string]string{
		MANAGED_LABEL:  "true",
		REVISION_LABEL: repoHash,
	}

	envVars := []corev1.EnvVar{}
	// Build Env should probably use core v1, so that we can assign normally instead of mapping
	if repo.Spec.BuildEnv != nil && len(repo.Spec.BuildEnv) > 0 {
		for _, buildEnv := range repo.Spec.BuildEnv {
			envVars = append(envVars, corev1.EnvVar{
				Name:  buildEnv.Name,
				Value: buildEnv.Value,
			})
		}
	}
	image.Spec = buildv1alpha2.ImageSpec{
		Tag:                repo.Spec.ImageSpec.Tag,
		ServiceAccountName: repo.Spec.ServiceAccountName,
		Builder: corev1.ObjectReference{
			// TODO: Should Come from configuration maybe??
			Name: "builder",
			Kind: "Builder",
		},
		Source: buildv1alpha1.SourceConfig{
			Git: &buildv1alpha1.Git{
				URL:      repo.Spec.Source.GitUrl,
				Revision: repo.Spec.Source.Revision,
			},
		},
		Build: &buildv1alpha2.ImageBuild{
			Env: envVars,
		},
	}
	return nil
}

func calcRepoHash(repo *webappv1alpha1.WebappRepository) (string, error) {
	bytes, err := json.Marshal(repo.Spec)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(bytes)
	return hex.EncodeToString(hash[:])[:8], nil
}

func imageName(repo *webappv1alpha1.WebappRepository) string {
	return repo.Name + "-image"
}

func imageObjectKey(repo *webappv1alpha1.WebappRepository) client.ObjectKey {
	return client.ObjectKey{Name: imageName(repo), Namespace: repo.Namespace}
}

// SetupWithManager sets up the controller with the Manager.
func (r *WebappRepositoryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&webappv1alpha1.WebappRepository{}).
		Owns(&buildv1alpha2.Image{}).
		Named("webapprepository").
		Complete(r)
}
