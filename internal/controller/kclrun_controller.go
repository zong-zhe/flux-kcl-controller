/*
Copyright 2023 The Flux authors

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
	"fmt"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"kcl-lang.io/kcl-go/pkg/kcl"
	"kcl-lang.io/kpm/pkg/opt"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	// "sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/fluxcd/pkg/http/fetch"
	"github.com/fluxcd/pkg/runtime/predicates"

	// "github.com/fluxcd/pkg/runtime/predicates"
	"github.com/fluxcd/pkg/ssa"
	"github.com/fluxcd/pkg/tar"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	sw "github.com/fluxcd/source-watcher/controllers"
	"github.com/kcl-lang/kcl-controller/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	kclapi "kcl-lang.io/kpm/pkg/api"
)

// KCLRunReconciler reconciles a KCLRun object
type KCLRunReconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	artifactFetcher *fetch.ArchiveFetcher
	HttpRetry       int
}

//+kubebuilder:rbac:groups=krm.kcl.dev.fluxcd,resources=kclruns,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=krm.kcl.dev.fluxcd,resources=kclruns/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=krm.kcl.dev.fluxcd,resources=kclruns/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the KCLRun object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.16.3/pkg/reconcile
func (r *KCLRunReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	// get source object
	var kclRun v1alpha1.KCLRun
	if err := r.Get(ctx, req.NamespacedName, &kclRun); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	source, err := r.getSource(ctx, &kclRun)
	if err != nil {
		return ctrl.Result{}, err
	}

	artifact := source.GetArtifact()
	log.Info(fmt.Sprintf("new revision detected %s", artifact.Revision))

	// create tmp dir
	tmpDir, err := os.MkdirTemp("", kclRun.Name)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create temp dir, error: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// download and extract artifact
	if err := r.artifactFetcher.Fetch(artifact.URL, artifact.Digest, tmpDir); err != nil {
		log.Error(err, "unable to fetch artifact")
		return ctrl.Result{}, err
	}

	// compile the KCL source code into the kubenretes manifests
	res, err := kclapi.RunWithOpts(
		opt.WithNoSumCheck(true),
		opt.WithKclOption(kcl.WithWorkDir(tmpDir)),
	)

	if err != nil {
		log.Error(err, "failed to compile the KCL source code")
		return ctrl.Result{}, err
	}

	u, err := ssa.ReadObjects(bytes.NewReader(([]byte(res.GetRawYamlResult()))))
	if err != nil {
		log.Error(err, "failed to compile the yaml str into kubernetes manifests")
		return ctrl.Result{}, err
	}

	rm := ssa.NewResourceManager(r.Client, nil, ssa.Owner{
		Field: "kcl-controler",
		Group: kclRun.GroupVersionKind().Group,
	})
	rm.SetOwnerLabels(u, kclRun.GetName(), kclRun.GetNamespace())

	// apply the manifests
	log.Info(fmt.Sprintf("applying %s", kclRun.GetName()))

	_, err = rm.ApplyAll(ctx, u, ssa.DefaultApplyOptions())
	if err != nil {
		err = fmt.Errorf("failed to run server-side apply: %w", err)
		return ctrl.Result{}, err
	}

	log.Info("successfully applied kcl resources")

	return ctrl.Result{}, nil
}

func (r *KCLRunReconciler) getSource(ctx context.Context,
	obj *v1alpha1.KCLRun) (sourcev1.Source, error) {
	var src sourcev1.Source
	sourceNamespace := obj.GetNamespace()
	if obj.Spec.SourceRef.Namespace != "" {
		sourceNamespace = obj.Spec.SourceRef.Namespace
	}
	namespacedName := types.NamespacedName{
		Namespace: sourceNamespace,
		Name:      obj.Spec.SourceRef.Name,
	}

	switch obj.Spec.SourceRef.Kind {
	case sourcev1.GitRepositoryKind:
		var repository sourcev1.GitRepository
		err := r.Client.Get(ctx, namespacedName, &repository)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return src, err
			}
			return src, fmt.Errorf("unable to get source '%s': %w", namespacedName, err)
		}
		src = &repository
		// TODO: get OCI registry Source
	default:
		return src, fmt.Errorf("source `%s` kind '%s' not supported",
			obj.Spec.SourceRef.Name, obj.Spec.SourceRef.Kind)
	}
	return src, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *KCLRunReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.artifactFetcher = fetch.NewArchiveFetcher(
		r.HttpRetry,
		tar.UnlimitedUntarSize,
		tar.UnlimitedUntarSize,
		os.Getenv("SOURCE_CONTROLLER_LOCALHOST"),
	)

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.KCLRun{}, builder.WithPredicates(
			predicate.Or(predicate.GenerationChangedPredicate{}, predicates.ReconcileRequestedPredicate{}),
		)).
		Watches(
			&sourcev1.GitRepository{},
			handler.EnqueueRequestsFromMapFunc(r.requestsForRevisionChangeOf()),
			builder.WithPredicates(sw.GitRepositoryRevisionChangePredicate{}),
		).
		Complete(r)
}

func (r *KCLRunReconciler) requestsForRevisionChangeOf() handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		log := ctrl.LoggerFrom(ctx)
		repo, ok := obj.(*sourcev1.GitRepository)

		if !ok {
			log.Error(fmt.Errorf("expected an object conformed with GetArtifact() method, but got a %T", obj),
				"failed to get reconcile requests for revision change")
			return nil
		}
		// If we do not have an artifact, we have no requests to make
		if repo.GetArtifact() == nil {
			return nil
		}

		// TODO: Only the same name and namespace is supported for now
		reqs := make([]reconcile.Request, 1)
		reqs[0].NamespacedName.Name = obj.GetName()
		reqs[0].NamespacedName.Namespace = obj.GetNamespace()
		return reqs
	}
}
