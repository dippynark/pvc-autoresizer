package controllers

import (
	"context"
	"errors"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const resizeEnableIndexKey = ".metadata.annotations[resize.topolvm.io/enabled]"
const storageClassNameIndexKey = ".spec.storageClassName"

// PersistentVolumeClaimReconciler reconciles a PersistentVolumeClaim object
type PersistentVolumeClaimReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=storage.k8s.io,resources=storageclasses,verbs=get;list;watch

func (r *PersistentVolumeClaimReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	_ = r.Log.WithValues("persistentvolumeclaim", req.NamespacedName)

	// your logic here
	var pvc corev1.PersistentVolumeClaim
	err := r.Get(ctx, req.NamespacedName, &pvc)
	if err != nil {
		return ctrl.Result{}, err
	}
	if pvc.Spec.StorageClassName == nil {
		return ctrl.Result{}, errors.New("`pvc.spec.StorageClassName` should not be empty")
	}

	var sc storagev1.StorageClass
	err = r.Get(ctx, client.ObjectKey{Name: *pvc.Spec.StorageClassName}, &sc)
	if err != nil {
		return ctrl.Result{}, err
	}
	if val, ok := sc.Annotations[AutoResizeEnabledKey]; !ok || val != "true" {
		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, nil
}

func indexByResizeEnableAnnotation(obj runtime.Object) []string {
	sc := obj.(*storagev1.StorageClass)
	if val, ok := sc.Annotations[AutoResizeEnabledKey]; ok {
		return []string{val}
	}

	return []string{}
}

func indexByStorageClassName(obj runtime.Object) []string {
	pvc := obj.(*corev1.PersistentVolumeClaim)
	scName := pvc.Spec.StorageClassName
	if scName == nil {
		return []string{}
	}
	return []string{*scName}
}

func (r *PersistentVolumeClaimReconciler) SetupWithManager(mgr ctrl.Manager, interval time.Duration) error {
	pred := predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return true },
		DeleteFunc:  func(event.DeleteEvent) bool { return false },
		UpdateFunc:  func(e event.UpdateEvent) bool { return true },
		GenericFunc: func(event.GenericEvent) bool { return true },
	}

	err := mgr.GetFieldIndexer().IndexField(context.Background(), &storagev1.StorageClass{}, resizeEnableIndexKey, indexByResizeEnableAnnotation)
	if err != nil {
		return err
	}

	err = mgr.GetFieldIndexer().IndexField(context.Background(), &corev1.PersistentVolumeClaim{}, storageClassNameIndexKey, indexByStorageClassName)
	if err != nil {
		return err
	}

	external := newPVCWatcher(interval)
	err = mgr.Add(external)
	if err != nil {
		return err
	}
	src := source.Channel{
		Source: external.channel,
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.PersistentVolumeClaim{}).
		Watches(&src, &handler.EnqueueRequestForObject{}).
		WithEventFilter(pred).
		Complete(r)
}
