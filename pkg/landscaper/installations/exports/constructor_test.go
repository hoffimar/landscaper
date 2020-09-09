// Copyright 2020 Copyright (c) 2020 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package exports_test

import (
	"context"
	"sync"

	"github.com/go-logr/logr/testing"
	g "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lsv1alpha1 "github.com/gardener/landscaper/pkg/apis/core/v1alpha1"
	"github.com/gardener/landscaper/pkg/kubernetes"
	"github.com/gardener/landscaper/pkg/landscaper/dataobjects"
	"github.com/gardener/landscaper/pkg/landscaper/datatype"
	"github.com/gardener/landscaper/pkg/landscaper/installations"
	"github.com/gardener/landscaper/pkg/landscaper/installations/exports"
	lsoperation "github.com/gardener/landscaper/pkg/landscaper/operation"
	"github.com/gardener/landscaper/pkg/landscaper/registry/blueprints"
	componentsregistry "github.com/gardener/landscaper/pkg/landscaper/registry/components"
	"github.com/gardener/landscaper/test/utils/fake_client"
)

var _ = g.Describe("Constructor", func() {

	var (
		op *installations.Operation

		fakeInstallations map[string]*lsv1alpha1.Installation
		fakeDataTypes     map[string]*lsv1alpha1.DataType
		fakeClient        client.Client
		fakeRegistry      blueprintsregistry.Registry
		fakeCompRepo      componentsregistry.Registry

		once sync.Once
	)

	g.BeforeEach(func() {
		once.Do(func() {
			var (
				err   error
				state *fake_client.State
			)
			fakeClient, state, err = fake_client.NewFakeClientFromPath("./testdata/state")
			Expect(err).ToNot(HaveOccurred())

			fakeInstallations = state.Installations
			fakeDataTypes = state.DataTypes
		})

		dtArr := make([]lsv1alpha1.DataType, 0)
		for _, dt := range fakeDataTypes {
			dtArr = append(dtArr, *dt)
		}
		internalDataTypes, err := datatype.CreateDatatypesMap(dtArr)
		Expect(err).ToNot(HaveOccurred())

		fakeRegistry, err = blueprintsregistry.NewLocalRegistry(testing.NullLogger{}, "../testdata/registry")
		Expect(err).ToNot(HaveOccurred())
		fakeCompRepo, err = componentsregistry.NewLocalClient(testing.NullLogger{}, "../testdata/registry")
		Expect(err).ToNot(HaveOccurred())

		op = &installations.Operation{
			Interface: lsoperation.NewOperation(testing.NullLogger{}, fakeClient, kubernetes.LandscaperScheme, fakeRegistry, fakeCompRepo),
			Datatypes: internalDataTypes,
		}
	})

	g.It("should construct the exported config from its execution", func() {
		inInstRoot, err := installations.CreateInternalInstallation(context.TODO(), op, fakeInstallations["test2/root"])
		Expect(err).ToNot(HaveOccurred())

		c := exports.NewConstructor(op)
		res, err := c.Construct(context.TODO(), inInstRoot)
		Expect(err).ToNot(HaveOccurred())
		Expect(res).ToNot(BeNil())
		Expect(res).To(HaveLen(2), "should export 2 data object for 2 exports")

		Expect(res[0].Data).To(Equal("val-exec"))
		Expect(res[0].Metadata.SourceType).To(Equal(lsv1alpha1.ExportDataObjectSourceType))
		Expect(res[0].Metadata.Key).To(Equal("root.y"))
		Expect(res[1].Data).To(Equal("val-exec"))
		Expect(res[1].Metadata.SourceType).To(Equal(lsv1alpha1.ExportDataObjectSourceType))
		Expect(res[1].Metadata.Key).To(Equal("root.z"))
	})

	g.It("should construct the exported config from a siblings", func() {
		inInstRoot, err := installations.CreateInternalInstallation(context.TODO(), op, fakeInstallations["test1/root"])
		Expect(err).ToNot(HaveOccurred())

		c := exports.NewConstructor(op)
		res, err := c.Construct(context.TODO(), inInstRoot)
		Expect(err).ToNot(HaveOccurred())
		Expect(res).ToNot(BeNil())
		Expect(res).To(HaveLen(2), "should export 2 data object from b and c")

		id := func(element interface{}) string {
			do := element.(*dataobjects.DataObject)
			return do.FieldValue.Key
		}
		Expect(res).To(MatchElements(id, IgnoreExtras, Elements{
			"root.z": PointTo(MatchFields(IgnoreExtras, Fields{
				"Data": Equal("val-b"),
				"Metadata": MatchFields(IgnoreExtras, Fields{
					"SourceType": Equal(lsv1alpha1.ExportDataObjectSourceType),
					"Key":        Equal("root.z"),
				}),
			})),
			"root.y": PointTo(MatchFields(IgnoreExtras, Fields{
				"Data": Equal("val-c"),
				"Metadata": MatchFields(IgnoreExtras, Fields{
					"SourceType": Equal(lsv1alpha1.ExportDataObjectSourceType),
					"Key":        Equal("root.y"),
				}),
			})),
		}))
	})

	g.It("should construct the exported config from a siblings and the execution config", func() {
		inInstRoot, err := installations.CreateInternalInstallation(context.TODO(), op, fakeInstallations["test3/root"])
		Expect(err).ToNot(HaveOccurred())

		c := exports.NewConstructor(op)
		res, err := c.Construct(context.TODO(), inInstRoot)
		Expect(err).ToNot(HaveOccurred())
		Expect(res).ToNot(BeNil())
		Expect(res).To(HaveLen(2), "should export 2 data object from execution and a")

		id := func(element interface{}) string {
			do := element.(*dataobjects.DataObject)
			return do.FieldValue.Key
		}
		Expect(res).To(MatchElements(id, IgnoreExtras, Elements{
			"root.y": PointTo(MatchFields(IgnoreExtras, Fields{
				"Data": Equal("val-exec"),
				"Metadata": MatchFields(IgnoreExtras, Fields{
					"SourceType": Equal(lsv1alpha1.ExportDataObjectSourceType),
					"Key":        Equal("root.y"),
				}),
			})),
			"root.z": PointTo(MatchFields(IgnoreExtras, Fields{
				"Data": Equal("val-a"),
				"Metadata": MatchFields(IgnoreExtras, Fields{
					"SourceType": Equal(lsv1alpha1.ExportDataObjectSourceType),
					"Key":        Equal("root.z"),
				}),
			})),
		}))
	})

})
