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
	"fmt"
	"sync/atomic"

	"fgiloux/controller-tools/pkg/cluster/usage"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	// "k8s.io/apiserver/pkg/admission"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"

	"k8s.io/klog/v2"
)

// InformerForResourceFunc knows how to provision an informer
type InformerForResourceFunc func(schema.GroupVersionResource) (informers.GenericInformer, error)

// ListerFuncForResourceFunc knows how to provision a lister from an informer func.
// The lister returns errors until the informer has synced.
func ListerFuncForResourceFunc(f InformerForResourceFunc) usage.ListerForResourceFunc {
	return func(gvr schema.GroupVersionResource) (cache.GenericLister, error) {
		informer, err := f(gvr)
		if err != nil {
			return nil, err
		}
		return &protectedLister{
			hasSynced:   cachedHasSynced(informer.Informer().HasSynced),
			notReadyErr: fmt.Errorf("%v not yet synced", gvr),
			delegate:    informer.Lister(),
		}, nil
	}
}

// cachedHasSynced returns a function that calls hasSynced() until it returns true once, then returns true
func cachedHasSynced(hasSynced func() bool) func() bool {
	cache := &atomic.Value{}
	cache.Store(false)
	return func() bool {
		if cache.Load().(bool) {
			// short-circuit if already synced
			return true
		}
		if hasSynced() {
			// remember we synced
			cache.Store(true)
			return true
		}
		return false
	}
}

// protectedLister returns notReadyError if hasSynced returns false, otherwise delegates to delegate
type protectedLister struct {
	hasSynced   func() bool
	notReadyErr error
	delegate    cache.GenericLister
}

func (p *protectedLister) List(selector labels.Selector) (ret []runtime.Object, err error) {
	if !p.hasSynced() {
		return nil, p.notReadyErr
	}
	return p.delegate.List(selector)
}
func (p *protectedLister) Get(name string) (runtime.Object, error) {
	if !p.hasSynced() {
		return nil, p.notReadyErr
	}
	return p.delegate.Get(name)
}
func (p *protectedLister) ByNamespace(namespace string) cache.GenericNamespaceLister {
	return &protectedNamespaceLister{p.hasSynced, p.notReadyErr, p.delegate.ByNamespace(namespace)}
}

// protectedNamespaceLister returns notReadyError if hasSynced returns false, otherwise delegates to delegate
type protectedNamespaceLister struct {
	hasSynced   func() bool
	notReadyErr error
	delegate    cache.GenericNamespaceLister
}

func (p *protectedNamespaceLister) List(selector labels.Selector) (ret []runtime.Object, err error) {
	if !p.hasSynced() {
		return nil, p.notReadyErr
	}
	return p.delegate.List(selector)
}
func (p *protectedNamespaceLister) Get(name string) (runtime.Object, error) {
	if !p.hasSynced() {
		return nil, p.notReadyErr
	}
	return p.delegate.Get(name)
}

// ListResourceUsingListerFunc returns a listing function based on the shared informer factory for the specified resource.
func ListResourceUsingListerFunc(l usage.ListerForResourceFunc, resource schema.GroupVersionResource, request usage.UsageRequest) ListFunc {
	labelFilter := labels.Everything()
	if request.FilterType == usage.LabelSelector {
		var err error
		if labelFilter, err = labels.Parse(request.Filter); err != nil {
			labelFilter = labels.Everything()
			klog.Errorf("Couldn't  parse the label selector %s: %v", request.Filter, err)
		}
	}
	if request.PerNamespace {
		return func(namespace string) ([]runtime.Object, error) {
			lister, err := l(resource)
			if err != nil {
				return nil, err
			}
			return lister.ByNamespace(namespace).List(labelFilter)
		}
	} else {
		return func(namespace string) ([]runtime.Object, error) {
			lister, err := l(resource)
			if err != nil {
				return nil, err
			}
			return lister.List(labelFilter)
		}
	}
}

// ObjectCountUsageResourceNameFor returns the object count name for specified groupResource
func ObjectCountUsageResourceNameFor(groupResource schema.GroupResource) corev1.ResourceName {
	if len(groupResource.Group) == 0 {
		return corev1.ResourceName("count/" + groupResource.Resource)
	}
	return corev1.ResourceName("count/" + groupResource.Resource + "." + groupResource.Group)
}

// ListFunc knows how to list resources
type ListFunc func(namespace string) ([]runtime.Object, error)

// MatchesScopeFunc knows how to evaluate if an object matches a scope
// type MatchesScopeFunc func(scope corev1.ScopedResourceSelectorRequirement, object runtime.Object) (bool, error)

// UsageFunc knows how to measure usage associated with an object
type UsageFunc func(object runtime.Object) (corev1.ResourceList, error)

// MatchingResourceNamesFunc is a function that returns the list of resources matched
// type MatchingResourceNamesFunc func(input []corev1.ResourceName) []corev1.ResourceName

// MatchesNoScopeFunc returns false on all match checks
/*func MatchesNoScopeFunc(scope corev1.ScopedResourceSelectorRequirement, object runtime.Object) (bool, error) {
	return false, nil
}*/

// Matches returns true if the quota matches the specified item.
/*func Matches(
	resourceQuota *corev1.ResourceQuota, item runtime.Object,
	matchFunc MatchingResourceNamesFunc, scopeFunc MatchesScopeFunc) (bool, error) {
	if resourceQuota == nil {
		return false, fmt.Errorf("expected non-nil quota")
	}
	// verify the quota matches on at least one resource
	matchResource := len(matchFunc(quota.ResourceNames(resourceQuota.Status.Hard))) > 0
	// by default, no scopes matches all
	matchScope := true
	for _, scope := range getScopeSelectorsFromQuota(resourceQuota) {
		innerMatch, err := scopeFunc(scope, item)
		if err != nil {
			return false, err
		}
		matchScope = matchScope && innerMatch
	}
	return matchResource && matchScope, nil
}*/

/*func getScopeSelectorsFromQuota(quota *corev1.ResourceQuota) []corev1.ScopedResourceSelectorRequirement {
	selectors := []corev1.ScopedResourceSelectorRequirement{}
	for _, scope := range quota.Spec.Scopes {
		selectors = append(selectors, corev1.ScopedResourceSelectorRequirement{
			ScopeName: scope,
			Operator:  corev1.ScopeSelectorOpExists})
	}
	if quota.Spec.ScopeSelector != nil {
		selectors = append(selectors, quota.Spec.ScopeSelector.MatchExpressions...)
	}
	return selectors
}*/

// CalculateUsageStats is a utility function that knows how to calculate aggregate usage.
/* func CalculateUsageStats(options usage.UsageStatsOptions,
	listFunc ListFunc,
	scopeFunc MatchesScopeFunc,
	usageFunc UsageFunc) (usage.UsageStats, error) {
	// default each tracked resource to zero
	result := usage.UsageStats{Used: corev1.ResourceList{}}
	for _, resourceName := range options.Resources {
		result.Used[resourceName] = resource.Quantity{Format: resource.DecimalSI}
	}
	items, err := listFunc(options.Namespace)
	if err != nil {
		return result, fmt.Errorf("failed to list content: %v", err)
	}
	for _, item := range items {
		// need to verify that the item matches the set of scopes
		matchesScopes := true
		for _, scope := range options.Scopes {
			innerMatch, err := scopeFunc(corev1.ScopedResourceSelectorRequirement{ScopeName: scope}, item)
			if err != nil {
				return result, nil
			}
			if !innerMatch {
				matchesScopes = false
			}
		}
		if options.ScopeSelector != nil {
			for _, selector := range options.ScopeSelector.MatchExpressions {
				innerMatch, err := scopeFunc(selector, item)
				if err != nil {
					return result, nil
				}
				matchesScopes = matchesScopes && innerMatch
			}
		}
		// only count usage if there was a match
		if matchesScopes {
			usageCount, err := usageFunc(item)
			if err != nil {
				return result, err
			}
			result.Used = usage.Add(result.Used, usageCount)
		}
	}
	return result, nil
}*/

// objectCountEvaluator provides an implementation for usage.Evaluator
// that associates usage of the specified resource based on the number of items
// returned by the specified listing function.
type objectCountEvaluator struct {
	// GroupResource that this evaluator tracks.
	// It is used to construct a generic object count name
	groupResource schema.GroupResource
	// A function that knows how to list resources
	// TODO move to dynamic client in future
	listFunc ListFunc
	// Names associated with this resource in the usage for generic counting.
	resourceNames []corev1.ResourceName
}

// Constraints returns an error if the configured resource name is not in the required set.
func (o *objectCountEvaluator) Constraints(required []corev1.ResourceName, item runtime.Object) error {
	// no-op for object counting
	return nil
}

// Handles returns true if the object count evaluator needs to track this attributes.
/*func (o *objectCountEvaluator) Handles(a admission.Attributes) bool {
	operation := a.GetOperation()
	return operation == admission.Create
}*/

// Matches returns true if the evaluator matches the specified quota with the provided input item
/*func (o *objectCountEvaluator) Matches(resourceQuota *corev1.ResourceQuota, item runtime.Object) (bool, error) {
	return Matches(resourceQuota, item, o.MatchingResources, MatchesNoScopeFunc)
}*/

// MatchingResources takes the input specified list of resources and returns the set of resources it matches.
func (o *objectCountEvaluator) MatchingResources(input []corev1.ResourceName) []corev1.ResourceName {
	return usage.Intersection(input, o.resourceNames)
}

// MatchingScopes takes the input specified list of scopes and input object. Returns the set of scopes resource matches.
func (o *objectCountEvaluator) MatchingScopes(item runtime.Object, scopes []corev1.ScopedResourceSelectorRequirement) ([]corev1.ScopedResourceSelectorRequirement, error) {
	return []corev1.ScopedResourceSelectorRequirement{}, nil
}

// UncoveredQuotaScopes takes the input matched scopes which are limited by configuration and the matched quota scopes.
// It returns the scopes which are in limited scopes but don't have a corresponding covering quota scope
/*func (o *objectCountEvaluator) UncoveredQuotaScopes(limitedScopes []corev1.ScopedResourceSelectorRequirement, matchedQuotaScopes []corev1.ScopedResourceSelectorRequirement) ([]corev1.ScopedResourceSelectorRequirement, error) {
	return []corev1.ScopedResourceSelectorRequirement{}, nil
}*/

// Usage returns the resource usage for the specified object
func (o *objectCountEvaluator) Usage(object runtime.Object) (corev1.ResourceList, error) {
	quantity := resource.NewQuantity(1, resource.DecimalSI)
	resourceList := corev1.ResourceList{}
	for _, resourceName := range o.resourceNames {
		resourceList[resourceName] = *quantity
	}
	return resourceList, nil
}

// GroupResource tracked by this evaluator
func (o *objectCountEvaluator) GroupResource() schema.GroupResource {
	return o.groupResource
}

// UsageStats calculates aggregate usage for the object.
/* func (o *objectCountEvaluator) UsageStats(options usage.UsageStatsOptions) (usage.UsageStats, error) {
	return CalculateUsageStats(options, o.listFunc, MatchesNoScopeFunc, o.Usage)
}*/

// Verify implementation of interface at compile time.
var _ usage.Evaluator = &simpleEvaluator{}
var _ usage.Evaluator = &namespacedEvaluator{}

// NewObjectCountEvaluator returns an evaluator that can perform generic
// object counting.  It allows an optional alias for backwards compatibility
// purposes for the legacy object counting names.  Unless its supporting
// backward compatibility, alias should not be used.
// FGI TODO to be removed when I have completed the implementation
// of simpleEvaluator and namespacedEvaluator
/* func NewObjectCountEvaluator(
	groupResource schema.GroupResource, listFunc ListFunc,
	alias corev1.ResourceName) usage.Evaluator {

	resourceNames := []corev1.ResourceName{ObjectCountUsageResourceNameFor(groupResource)}
	if len(alias) > 0 {
		resourceNames = append(resourceNames, alias)
	}

	return &objectCountEvaluator{
		groupResource: groupResource,
		listFunc:      listFunc,
		resourceNames: resourceNames,
	}
}*/

// SimpleEvaluator provides an implementation for usage.Evaluator
// it is thought for cluster scoped resources or resources whose usage is not tracked by namespace
type simpleEvaluator struct {
	// groupResource contains the Kubernetes resource the measure is for
	groupResource schema.GroupResource
	// A function that knows how to list resources
	listFunc ListFunc
	// resourceNames are the set of resources to include in the measurement: (CPU, memory, etc)
	resourceNames []corev1.ResourceName
	// measures contains the current measures
	measures usage.Measure
	// Processor knows how to calculate the new usage
	processor usage.Processor
}

// NamespacedEvaluator provides an implementation for usage.Evaluator
// it is thought for resources whose usage is tracked by namespace
type namespacedEvaluator struct {
	// groupResource contains the Kubernetes resource the measure is for
	groupResource schema.GroupResource
	// A function that knows how to list resources by namespace.
	listFunc ListFunc
	// resourceNames are the set of resources to include in the measurement: (CPU, memory, etc)
	resourceNames []corev1.ResourceName
	// measures contains the current measures
	measures map[string]usage.Measure
	// Processor knows how to calculate the new usage
	processor usage.Processor
}

// GroupResource returns the Kubernetes resource the measurer is for
func (e *simpleEvaluator) GroupResource() schema.GroupResource {
	return e.groupResource
}

// Usage returns the current Measure
func (e *simpleEvaluator) Usage() usage.Measure {
	return usage.Measure{
		Used:  e.measures.Used,
		Count: e.measures.Count,
	}
}

// NamespacedUsage returns the current Measure, which is not namespace
// dependant for a simpleEvaluator
func (e *simpleEvaluator) NamespacedUsage(namespace string) usage.Measure {
	return usage.Measure{
		Used:  e.measures.Used,
		Count: e.measures.Count,
	}
}

// Recalculate refreshes the measures for the specified resource
func (e *simpleEvaluator) Recalculate(namespace string, groupResource schema.GroupResource) map[string]usage.Measure {
	newMeasures := e.processor.Recalculate(e.measures, namespace, groupResource)
	return map[string]usage.Measure{metav1.NamespaceAll: newMeasures}
}

// ProcessChange updates the usage with the change event and returns the new status (a map of measures indexed by namespaces)
func (e *simpleEvaluator) ProcessChange(event *usage.Event) map[string]usage.Measure {
	newMeasures := e.processor.ProcessChange(e.measures, event)
	e.measures = newMeasures
	return map[string]usage.Measure{metav1.NamespaceAll: newMeasures}
}

// SetProcessor configures the processor
// func (e *simpleEvaluator) SetProcessor(processor usage.Processor) {
//	e.processor = processor
//}

// NewSimpleEvaluator instantiates a simpleEvaluator
// FGI TODO DONE: Evaluators have a ListerForFunc parameter, populated by New, see: pkg/cluster/usage/evaluator/core/registry.go
// I would need to get that and store it in my Measurer / new Evaluator
// In SyncMonitors in pkg/cluster/usage/controller/resource_monitor.go Evaluators are created
// but listerFuncs are retrieved and passed as arguments. I would need to keep that. I may be able to keep the code as is
// something like:
// listerFunc := generic.ListerFuncForResourceFunc(um.informerFactory.ForResource)
//                listResourceFunc := generic.ListResourceUsingListerFunc(listerFunc, resource)
//                evaluator = generic.NewObjectCountEvaluator(resource.GroupResource(), listResourceFunc, "")
// if a processor factory is provided the evaluator will use a processor from it
func NewSimpleEvaluator(groupResource schema.GroupResource, listFunc ListFunc, processorFunc NewProcessor) usage.Evaluator {
	// FGI TODO: I need to implement how to retrieve resourceNames and the associated logic to process usage
	// depending on the GroupResource and to inject it in the Evaluator
	// resourceNames []corev1.ResourceName
	measures := usage.Measure{
		Used:  corev1.ResourceList{},
		Count: 0,
	}
	evaluator := &simpleEvaluator{
		groupResource: groupResource,
		listFunc:      listFunc,
		// resourceNames: resourceNames,
		measures: measures,
	}
	if processorFunc != nil {
		evaluator.processor = processorFunc(groupResource)
	} else {
		evaluator.processor = StdProcessor(groupResource)
	}
	return evaluator
}

// GroupResource returns the Kubernetes resource the evaluator is for
func (e *namespacedEvaluator) GroupResource() schema.GroupResource {
	return e.groupResource
}

// Usage provides the aggregated usage for all namespaces.
// Aggregation inside a namespace has already taken place before.
func (e *namespacedEvaluator) Usage() usage.Measure {
	result := usage.Measure{
		Used:  corev1.ResourceList{},
		Count: 0,
	}
	// Initialisation
	// default each tracked resource to zero
	for _, resourceName := range e.resourceNames {
		result.Used[resourceName] = resource.Quantity{Format: resource.DecimalSI}
	}
	for _, measure := range e.measures {
		result.Used = usage.Add(result.Used, measure.Used)
		result.Count = result.Count + measure.Count
	}
	return result
}

// NamespacedUsage returns the usage for a specific namespace
func (e *namespacedEvaluator) NamespacedUsage(namespace string) usage.Measure {
	return usage.Measure{
		Used:  e.measures[namespace].Used,
		Count: e.measures[namespace].Count,
	}
}

// Recalculate refreshes the measures for the specified resource
func (e *namespacedEvaluator) Recalculate(namespace string, groupResource schema.GroupResource) map[string]usage.Measure {
	newMeasures := e.processor.Recalculate(e.measures[namespace], namespace, groupResource)
	e.measures[namespace] = newMeasures
	return e.measures
}

// ProcessChange updates the usage with the change event and returns the new status (a map of measures indexed by namespaces)
func (e *namespacedEvaluator) ProcessChange(event *usage.Event) map[string]usage.Measure {
	accessor, err := meta.Accessor(event.Obj)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("cannot access obj: %v", err))
		return e.measures
	}
	ns := accessor.GetNamespace()
	newMeasures := e.processor.ProcessChange(e.measures[ns], event)
	e.measures[ns] = newMeasures
	return e.measures
}

// SetProcessor configures the processor
//func (e *namespacedEvaluator) SetProcessor(processor usage.Processor) {
//	e.Processor = processor
//}

// NewNamespaceEvaluator instantiate a namespaced evaluator
// if a processor factory is provided the evaluator will use a processor from it
func NewNamespacedEvaluator(groupResource schema.GroupResource, listFunc ListFunc, processorFunc NewProcessor) usage.Evaluator {
	// FGI TODO: I need to implement how to retrieve resourceNames and the associated logic to process usage
	// depending on the GroupResource and to inject it in the Evaluator
	// resourceNames []corev1.ResourceName
	evaluator := &namespacedEvaluator{
		groupResource: groupResource,
		listFunc:      listFunc,
		// resourceNames: resourceNames,
		measures: map[string]usage.Measure{},
	}
	if processorFunc != nil {
		evaluator.processor = processorFunc(groupResource)
	} else {
		evaluator.processor = StdProcessor(groupResource)
	}
	return evaluator
}
