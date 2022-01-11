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

// NewProcessor knows how to create a processor
type NewProcessor func(groupResource schema.GroupResource) usage.Processor

// StdProcessor knows how to create a standard processor able to deal with the provided GroupResource
func StdProcessor(groupResource schema.GroupResource) usage.Processor {
	switch groupResource {
	case schema.GroupResource{Resource: "pods"}:
		return NewPodProcessor(groupResource)
	case schema.GroupResource{Resource: "services"}:
		return NewServiceProcessor(groupResource)
	case schema.GroupResource{Resource: "persistentvolumeclaims"}:
		return NewPVCProcessor(groupResource)
	default:
		return NewObjectCountProcessor(groupResource)
	}
}

type ObjectCountProcessor struct {
	groupResource schema.GroupResource
}

func NewObjectCountProcessor(groupResource schema.GroupResource) usage.Processor {
	processor := ObjectCountProcessor{
		groupResource: groupResource,
	}
	return processor
}

// Recalculate refreshes the measures for the specified resource and namespace
func (ObjectCountProcessor) Recalculate(measures usage.Measure, namespace string, groupResource schema.GroupResource) usage.Measure {
	// FGI TODO
	return measures
}

// ProcessChange processes the measures with the change event and returns the new state
func (ObjectCountProcessor) ProcessChange(measures usage.Measure, event *usage.Event) usage.Measure {
	switch event.EventType {
	case usage.AddEvent:
		measures.Count++
	case usage.DeleteEvent:
		measures.Count--
		if measures.Count < 0 {
			measures.Count = 0
		}
	default:
	}
	return measures
}
