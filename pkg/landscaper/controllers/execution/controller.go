// SPDX-FileCopyrightText: 2020 SAP SE or an SAP affiliate company and Gardener contributors.
//
// SPDX-License-Identifier: Apache-2.0

package execution

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/gardener/landscaper/controller-utils/pkg/kubernetes"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	lserrors "github.com/gardener/landscaper/apis/errors"

	lsv1alpha1 "github.com/gardener/landscaper/apis/core/v1alpha1"
	lsv1alpha1helper "github.com/gardener/landscaper/apis/core/v1alpha1/helper"
	"github.com/gardener/landscaper/pkg/landscaper/execution"
	"github.com/gardener/landscaper/pkg/landscaper/operation"
	"github.com/gardener/landscaper/pkg/utils/read_write_layer"
)

// NewController creates a new execution controller that reconcile Execution resources.
func NewController(log logr.Logger, kubeClient client.Client, scheme *runtime.Scheme, eventRecorder record.EventRecorder) (reconcile.Reconciler, error) {
	return &controller{
		log:           log,
		client:        kubeClient,
		scheme:        scheme,
		eventRecorder: eventRecorder,
	}, nil
}

type controller struct {
	log           logr.Logger
	client        client.Client
	eventRecorder record.EventRecorder
	scheme        *runtime.Scheme
}

func (c *controller) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := c.log.WithValues("resource", req.NamespacedName)
	logger.V(5).Info("reconcile")

	exec := &lsv1alpha1.Execution{}
	if err := read_write_layer.GetExecution(ctx, c.client, req.NamespacedName, exec); err != nil {
		if apierrors.IsNotFound(err) {
			logger.V(5).Info(err.Error())
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	// don't reconcile if ignore annotation is set and execution is not currently running
	if lsv1alpha1helper.HasIgnoreAnnotation(exec.ObjectMeta) && lsv1alpha1helper.IsCompletedExecutionPhase(exec.Status.Phase) {
		logger.V(7).Info("skipping reconcile due to ignore annotation")
		return reconcile.Result{}, nil
	}

	oldExec := exec.DeepCopy()

	lsError := c.Ensure(ctx, logger, exec)

	lsErr2 := c.removeForceReconcileAnnotation(ctx, exec)
	if lsError == nil {
		// lsError is more important than lsErr2
		lsError = lsErr2
	}

	isDelete := !exec.DeletionTimestamp.IsZero()
	return reconcile.Result{}, handleError(ctx, lsError, logger, c.client, c.eventRecorder, oldExec, exec, isDelete)
}

func (c *controller) Ensure(ctx context.Context, log logr.Logger, exec *lsv1alpha1.Execution) lserrors.LsError {
	if err := HandleAnnotationsAndGeneration(ctx, log, c.client, exec); err != nil {
		return err
	}

	if exec.DeletionTimestamp.IsZero() && !kubernetes.HasFinalizer(exec, lsv1alpha1.LandscaperFinalizer) {
		controllerutil.AddFinalizer(exec, lsv1alpha1.LandscaperFinalizer)
		if err := c.Writer().UpdateExecution(ctx, read_write_layer.W000025, exec); err != nil {
			return lserrors.NewError("Reconcile", "AddFinalizer", err.Error())
		}
	}

	forceReconcile := lsv1alpha1helper.HasOperation(exec.ObjectMeta, lsv1alpha1.ForceReconcileOperation)
	op := execution.NewOperation(operation.NewOperation(log, c.client, c.scheme, c.eventRecorder), exec,
		forceReconcile)

	if !exec.DeletionTimestamp.IsZero() {
		return op.Delete(ctx)
	}

	if lsv1alpha1helper.IsCompletedExecutionPhase(exec.Status.Phase) {
		err := op.HandleDeployItemPhaseAndGenerationChanges(ctx, log)
		if err != nil {
			return lserrors.NewWrappedError(err, "Reconcile", "HandleDeployItemPhaseAndGenerationChanges", err.Error())
		}
		if lsv1alpha1helper.IsCompletedExecutionPhase(exec.Status.Phase) {
			return nil
		}
	}

	return op.Reconcile(ctx)
}

func (c *controller) removeForceReconcileAnnotation(ctx context.Context, exec *lsv1alpha1.Execution) lserrors.LsError {
	if lsv1alpha1helper.HasOperation(exec.ObjectMeta, lsv1alpha1.ForceReconcileOperation) {
		old := exec.DeepCopy()
		delete(exec.Annotations, lsv1alpha1.OperationAnnotation)
		writer := read_write_layer.NewWriter(c.log, c.client)
		if err := writer.PatchExecution(ctx, read_write_layer.W000029, exec, old); err != nil {
			c.eventRecorder.Event(exec, corev1.EventTypeWarning, "RemoveForceReconcileAnnotation", err.Error())
			return lserrors.NewWrappedError(err, "Reconcile", "RemoveForceReconcileAnnotation", err.Error())
		}
	}
	return nil
}

func (c *controller) Writer() *read_write_layer.Writer {
	return read_write_layer.NewWriter(c.log, c.client)
}

// HandleAnnotationsAndGeneration is meant to be called at the beginning of the reconcile loop.
// If a reconcile is needed due to the reconcile annotation or a change in the generation, it will set the phase to Init and remove the reconcile annotation.
// Returns: an error, if updating the execution failed, nil otherwise
func HandleAnnotationsAndGeneration(ctx context.Context, log logr.Logger, c client.Client, exec *lsv1alpha1.Execution) lserrors.LsError {
	operation := "HandleAnnotationsAndGeneration"
	hasReconcileAnnotation := lsv1alpha1helper.HasOperation(exec.ObjectMeta, lsv1alpha1.ReconcileOperation)
	hasForceReconcileAnnotation := lsv1alpha1helper.HasOperation(exec.ObjectMeta, lsv1alpha1.ForceReconcileOperation)
	if hasReconcileAnnotation || hasForceReconcileAnnotation || exec.Status.ObservedGeneration != exec.Generation {
		// reconcile necessary due to one of
		// - reconcile annotation
		// - force-reconcile annotation
		// - outdated generation
		opAnn := lsv1alpha1helper.GetOperation(exec.ObjectMeta)
		log.V(5).Info("reconcile required, setting observed generation and phase", "operationAnnotation", opAnn, "observedGeneration", exec.Status.ObservedGeneration, "generation", exec.Generation)
		exec.Status.ObservedGeneration = exec.Generation
		exec.Status.Phase = lsv1alpha1.ExecutionPhaseInit

		log.V(7).Info("updating status")
		writer := read_write_layer.NewWriter(log, c)
		if err := writer.UpdateExecutionStatus(ctx, read_write_layer.W000033, exec); err != nil {
			return lserrors.NewWrappedError(err, operation, "update execution status", err.Error())
		}
		log.V(7).Info("successfully updated status")
	}
	if hasReconcileAnnotation {
		log.V(5).Info("removing reconcile annotation")
		delete(exec.ObjectMeta.Annotations, lsv1alpha1.OperationAnnotation)
		log.V(7).Info("updating metadata")
		writer := read_write_layer.NewWriter(log, c)
		if err := writer.UpdateExecution(ctx, read_write_layer.W000027, exec); err != nil {
			return lserrors.NewWrappedError(err, operation, "update execution", err.Error())
		}
		log.V(7).Info("successfully updated metadata")
	}

	return nil
}

func handleError(ctx context.Context, err lserrors.LsError, log logr.Logger, c client.Client,
	eventRecorder record.EventRecorder, oldExec, exec *lsv1alpha1.Execution, isDelete bool) error {
	// if successfully deleted we could not update the object
	if isDelete && err == nil {
		return nil
	}

	// There are two kind of errors: err != nil and exec.Status.LastError != nil
	// If err != nil this error is set and returned such that a retry is initiated.
	// If err == nil and exec.Status.LastError != nil another object must change its state and initiate a new event
	// for the execution exec.
	if err != nil {
		log.Error(err, "handleError")
		exec.Status.LastError = lserrors.TryUpdateLsError(exec.Status.LastError, err)
	}

	exec.Status.Phase = lsv1alpha1.ExecutionPhase(lserrors.GetPhaseForLastError(
		lsv1alpha1.ComponentInstallationPhase(exec.Status.Phase),
		exec.Status.LastError,
		5*time.Minute),
	)

	if exec.Status.LastError != nil {
		lastErr := exec.Status.LastError
		eventRecorder.Event(exec, corev1.EventTypeWarning, lastErr.Reason, lastErr.Message)
	}

	if !reflect.DeepEqual(oldExec.Status, exec.Status) {
		writer := read_write_layer.NewWriter(log, c)
		if updateErr := writer.UpdateExecutionStatus(ctx, read_write_layer.W000031, exec); updateErr != nil {
			if apierrors.IsConflict(updateErr) { // reduce logging
				log.V(5).Info(fmt.Sprintf("unable to update status: %s", updateErr.Error()))
			} else {
				log.Error(updateErr, "unable to update status")
			}
			return updateErr
		}
	}
	return err
}
