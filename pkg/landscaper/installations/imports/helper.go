// SPDX-FileCopyrightText: 2020 SAP SE or an SAP affiliate company and Gardener contributors.
//
// SPDX-License-Identifier: Apache-2.0

package imports

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lsv1alpha1 "github.com/gardener/landscaper/apis/core/v1alpha1"
	lsv1alpha1helper "github.com/gardener/landscaper/apis/core/v1alpha1/helper"
	"github.com/gardener/landscaper/pkg/landscaper/installations"
)

// CheckCompletedSiblingDependentsOfParent checks if siblings and siblings of the parent's parents that the parent depends on (imports data) are completed.
func CheckCompletedSiblingDependentsOfParent(ctx context.Context, kubeClient client.Client, parent *installations.InstallationBase) (bool, error) {
	if parent == nil {
		return true, nil
	}
	parentCtxName := installations.GetInstallationContextName(parent.Info)
	siblingsCompleted, err := CheckCompletedSiblingDependents(ctx, kubeClient, parentCtxName, parent)
	if err != nil {
		return false, err
	}
	if !siblingsCompleted {
		return siblingsCompleted, nil
	}

	// check its own parent
	parentsParentInst, err := installations.GetParent(ctx, kubeClient, parent.Info)
	if err != nil {
		return false, fmt.Errorf("unable to get parent of parent: %w", err)
	}

	if parentsParentInst == nil {
		return true, nil
	}
	return CheckCompletedSiblingDependentsOfParent(ctx, kubeClient, installations.NewInstallationBase(parentsParentInst))
}

// CheckCompletedSiblingDependents checks if siblings that the installation depends on (imports data) are completed
func CheckCompletedSiblingDependents(ctx context.Context,
	kubeClient client.Client,
	contextName string,
	inst *installations.InstallationBase) (bool, error) {
	if inst == nil {
		return true, nil
	}

	log := logr.FromContextOrDiscard(ctx)

	// todo: add target support
	for _, dataImport := range inst.Info.Spec.Imports.Data {
		sourceRef, err := getImportSource(ctx, kubeClient, contextName, inst, dataImport)
		if err != nil {
			return false, err
		}
		if sourceRef == nil {
			continue
		}
		// check if the import is imported from myself or the parent
		// and continue if so as we have a different check for the parent
		if lsv1alpha1helper.ReferenceIsObject(*sourceRef, inst.Info) {
			continue
		}

		parent, err := installations.GetParent(ctx, kubeClient, inst.Info)
		if err != nil {
			return false, err
		}
		if parent != nil && lsv1alpha1helper.ReferenceIsObject(*sourceRef, parent) {
			continue
		}

		// we expect that the source ref is always a installation
		inst := &lsv1alpha1.Installation{}
		if err := kubeClient.Get(ctx, sourceRef.NamespacedName(), inst); err != nil {
			return false, err
		}

		if !lsv1alpha1helper.IsCompletedInstallationPhase(inst.Status.Phase) {
			log.V(3).Info("dependent installation not completed", "inst", sourceRef.NamespacedName().String())
			return false, nil
		}

		intInst := installations.CreateInternalInstallationBase(inst)

		isCompleted, err := CheckCompletedSiblingDependents(ctx, kubeClient, contextName, intInst)
		if err != nil {
			return false, err
		}
		if !isCompleted {
			return false, nil
		}
	}

	return true, nil
}

// getImportSource returns a reference to the owner of a data import.
func getImportSource(ctx context.Context,
	kubeClient client.Client,
	contextName string,
	inst *installations.InstallationBase,
	dataImport lsv1alpha1.DataImport) (*lsv1alpha1.ObjectReference, error) {
	status, err := inst.ImportStatus().GetData(dataImport.Name)
	if err == nil && status.SourceRef != nil {
		return status.SourceRef, nil
	}

	// we have to get the corresponding installation from the the cluster
	_, owner, err := installations.GetDataImport(ctx, kubeClient, contextName, inst, dataImport)
	if err != nil {
		return nil, err
	}

	// we cannot validate if the source is not an installation
	if owner == nil || owner.Kind != "Installation" {
		return nil, nil
	}
	return &lsv1alpha1.ObjectReference{Name: owner.Name, Namespace: inst.Info.Namespace}, nil
}
