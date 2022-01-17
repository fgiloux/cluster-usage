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

package usage

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	// "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
)

type EventType int

func (e EventType) String() string {
	switch e {
	case AddEvent:
		return "add"
	case UpdateEvent:
		return "update"
	case DeleteEvent:
		return "delete"
	default:
		return fmt.Sprintf("unknown(%d)", int(e))
	}
}

const (
	AddEvent EventType = iota
	UpdateEvent
	DeleteEvent
)

// FGI: Not sure it is needed
type Event struct {
	EventType EventType
	Obj       interface{}
	OldObj    interface{}
	GR        schema.GroupResource
}

// Measure is the measure of resources use in the system
// The keys are either the name of a namespace,
// metav1.NamespaceAll or metav1.NamespaceNone
type Measure struct {
	// Used maps resources (CPU, memory, etc) to quantity used
	Used map[string]corev1.ResourceList
	// Count tracks the number of objects
	Counts map[string]int32
}

// UsageRequest is a request for tracking some specific usage
type UsageRequest struct {
	// GroupResource to track
	GroupResource schema.GroupResource
	// PerNamespace specifies whether the accounting needs to be done per namespace
	PerNamespace bool
	// Filter is a filter which is a metav1.ListOptions compatible expression
	Filter string
	// FilterType is the type of filter: either LabelSelector or FieldSelector (nil if no filter)
	FilterType SelectorType
	// Threshold is the percentage of increase or decrease triggering a notification
	Threshold int16
	// Notifier is a channel, where the new result gets notified
	Notifier chan *UsageResponse
}

// UsageResponse is a response for a specific GroupResource
type UsageResponse struct {
	// GroupResource of the measures
	GroupResource schema.GroupResource
	// Measures is a map of the measures for each namespace
	// metav1.NamespaceAll is used as key for cluster scoped resources
	Measures map[string]Measure
}

type SelectorType int8

const (
	LabelSelector SelectorType = iota
	// FGI TODO: not implementing FieldSelector for now.
	// the impact of it on the use of metadata vs type informers would need to be considered
	// FieldSelector
)

// FGI: Compared to quotas the main difference is that this implementation stores the values
// (communicated through notification) in memory instead of using a quota resource for it.
// Evaluator knows how to track usage for a particular group resource
type Evaluator interface {
	// GroupResource returns the evaluated resource
	GroupResource() schema.GroupResource
	// Usage returns either the cluster scoped usage or the aggregated measures for the resource
	Usage() Measure
	// NamespacedUsage returns the usage of the resource for the specified namespace. Same as Usage for cluster scoped resources.
	NamespacedUsage(namespace string) Measure
	// Recalculate provides the current usage state for the specified namespace
	Recalculate(namespace string) map[string]Measure
	// FGI: Not necessary as it states close to the controller philosophie and does not work on deltas
	// ProcessChange updates the usage with the change event and returns the new state (a map of measures indexed by namespaces)
	// ProcessChange(event *Event) map[string]Measure
}

// Registry maintains a list of evaluators
type Registry interface {
	// Add to registry
	Add(e Evaluator)
	// Remove from registry
	Remove(e Evaluator)
	// Get by group resource
	Get(gr schema.GroupResource) Evaluator
	// List from registry
	List() []Evaluator
}

// ListerForResourceFunc knows how to get a lister for a specific resource
type ListerForResourceFunc func(schema.GroupVersionResource) (cache.GenericLister, error)
