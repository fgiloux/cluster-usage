/*
Copyright 2021 The Kubernetes Authors.

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

package generic

import (
	"fgiloux/controller-tools/pkg/cluster/usage"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

type ServiceProcessor struct {
	groupResource schema.GroupResource
}

func NewServiceProcessor(groupResource schema.GroupResource) usage.Processor {
	processor := ServiceProcessor{
		groupResource: groupResource,
	}
	return processor
}

// Recalculate refreshes the measures for the specified resource and namespace
func (ServiceProcessor) Recalculate(measures usage.Measure, namespace string, groupResource schema.GroupResource) usage.Measure {
	// FGI TODO
	return measures
}

// ProcessChange processes the measures with the change event and returns the new state
func (ServiceProcessor) ProcessChange(measures usage.Measure, event *usage.Event) usage.Measure {
	// FGI TODO
	return measures
}