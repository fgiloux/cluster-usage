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

package controller

import (
	// "context"
	"fmt"
	// "reflect"
	"sync"
	"time"

	"fgiloux/controller-tools/pkg/cluster/usage"
	"fgiloux/controller-tools/pkg/cluster/usage/informerfactory"

	"k8s.io/klog/v2"

	// "k8s.io/api/core/v1"
	// apiequality "k8s.io/apimachinery/pkg/api/equality"
	// "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	// "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	// utilerrors "k8s.io/apimachinery/pkg/util/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"

	// "k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/discovery"
	// coreinformers "k8s.io/client-go/informers/core/v1"
	// corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	// corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	// "k8s.io/kubernetes/pkg/controller"
)

// ResyncPeriodFunc generates a duration each time it is invoked; this is so that
// multiple controllers don't get into lock-step and all hammer the apiserver
// with list requests simultaneously.
type ResyncPeriodFunc func() time.Duration

// ResourcesFunc knows how to discover resources.
type ResourcesFunc func() ([]*metav1.APIResourceList, error)

// type NamespacedResourcesFunc func() ([]*metav1.APIResourceList, error)

// ReplenishmentFunc is a signal that a resource changed in specified namespace
// that may require usage to be recalculated.
type ReplenishmentFunc func(groupResource schema.GroupResource, namespace string)

// ControllerOptions holds options for creating a usage controller
type ControllerOptions struct {
	// Must have authority to list all quotas, and update quota status
	// QuotaClient corev1client.ResourceQuotasGetter
	// Shared informer for resource quotas
	// ResourceQuotaInformer coreinformers.ResourceQuotaInformer
	// Controls full recalculation of quota usage
	ResyncPeriod ResyncPeriodFunc
	// Maintains evaluators that know how to calculate usage for group resource
	Registry usage.Registry
	// Discover the list of supported resources on the server, whose usage may get tracked
	// FGI TODO: I need to check what is passed through the controller options. It will need to be amended. At the end of the day what is required
	// - Get a list of resource group, kind (statically configurable unlike quota)
	// - Check that the APIs exist and return the matches
	// - Unlike quota I may not want to limit to namespaced resources
	// This is initialised here: https://github.com/kubernetes/kubernetes/blob/master/cmd/kube-controller-manager/app/core.go#L419
	// resourceQuotaControllerDiscoveryClient.ServerPreferredNamespacedResources
	// DiscoveryFunc NamespacedResourcesFunc
	DiscoveryFunc ResourcesFunc
	// A function that returns the list of resources to ignore
	// IgnoredResourcesFunc func() map[schema.GroupResource]struct{}
	// InformersStarted knows if informers were started.
	InformersStarted <-chan struct{}
	// InformerFactory interfaces with informers.
	InformerFactory informerfactory.InformerFactory
	// Controls full resync of objects monitored for replenishment.
	ReplenishmentResyncPeriod ResyncPeriodFunc
	// UsageRequests contains the usage tracking requests with related filters and notifications
	UsageRequests []usage.UsageRequest
}

// Controller is responsible for tracking usage in the system
type Controller struct {
	// Must have authority to list all resources in the system
	// rqClient corev1client.ResourceQuotasGetter
	// A lister/getter of resource quota objects
	// rqLister corelisters.ResourceQuotaLister
	// A list of functions that return true when their caches have synced
	informerSyncedFuncs []cache.InformerSynced
	// Resource objects that need to be synchronized
	queue workqueue.RateLimitingInterface
	// missingUsageQueue holds objects that are missing the initial usage information
	// FGI TODO: we may not need it. This is called when a quota is added but our list is static
	// missingUsageQueue workqueue.RateLimitingInterface
	// To allow injection of syncUsage for testing.
	// FGI TODO
	syncHandler func(key string) error
	// function that controls full recalculation of quota usage
	resyncPeriod ResyncPeriodFunc
	// knows how to calculate usage
	registry usage.Registry
	// knows how to monitor all the resources tracked and trigger replenishment
	usageMonitor *UsageMonitor
	// controls the workers that process usage
	// this lock is acquired to control write access to the monitors and ensures that all
	// monitors are synced before the controller can report usage.
	workerLock sync.RWMutex
}

// New creates a usage controller with specified options
func New(options *ControllerOptions) (*Controller, error) {
	// build the resource usage controller
	rc := &Controller{
		// rqClient:            options.QuotaClient,
		// rqLister:            options.ResourceQuotaInformer.Lister(),
		// informerSyncedFuncs: []cache.InformerSynced{options.ResourceQuotaInformer.Informer().HasSynced},
		informerSyncedFuncs: []cache.InformerSynced{},
		queue:               workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "resource_primary"),
		// missingUsageQueue:   workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "resource_priority"),
		resyncPeriod: options.ResyncPeriod,
		registry:     options.Registry,
	}
	// set the synchronization handler
	rc.syncHandler = rc.syncResourceUsageFromKey

	// FGI: not reconciliating any resource
	// TODO: may need to be removed
	/* options.ResourceQuotaInformer.Informer().AddEventHandlerWithResyncPeriod(
		cache.ResourceEventHandlerFuncs{
			AddFunc: rc.addQuota,
			UpdateFunc: func(old, cur interface{}) {
				// We are only interested in observing updates to quota.spec to drive updates to quota.status.
				// We ignore all updates to quota.Status because they are all driven by this controller.
				// IMPORTANT:
				// We do not use this function to queue up a full quota recalculation.  To do so, would require
				// us to enqueue all quota.Status updates, and since quota.Status updates involve additional queries
				// that cannot be backed by a cache and result in a full query of a namespace's content, we do not
				// want to pay the price on spurious status updates.  As a result, we have a separate routine that is
				// responsible for enqueue of all resource quotas when doing a full resync (enqueueAll)
				oldResourceQuota := old.(*v1.ResourceQuota)
				curResourceQuota := cur.(*v1.ResourceQuota)
				if quota.Equals(oldResourceQuota.Spec.Hard, curResourceQuota.Spec.Hard) {
					return
				}
				rq.addQuota(curResourceQuota)
			},
			// This will enter the sync loop and no-op, because the controller has been deleted from the store.
			// Note that deleting a controller immediately after scaling it to 0 will not work. The recommended
			// way of achieving this is by performing a `stop` operation on the controller.
			DeleteFunc: rq.enqueueResourceQuota,
		},
		rq.resyncPeriod(),
	)*/

	if options.DiscoveryFunc != nil {
		um := &UsageMonitor{
			informersStarted: options.InformersStarted,
			informerFactory:  options.InformerFactory,
			// ignoredResources: options.IgnoredResourcesFunc(),
			resourceChanges: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "resource_usage_controller_resource_changes"),
			resyncPeriod:    options.ReplenishmentResyncPeriod,
			replenishment:   rc.replenishUsage,
			registry:        rc.registry,
		}
		rc.usageMonitor = um

		// do initial quota monitor setup.  If we have a discovery failure here, it's ok. We'll discover more resources when a later sync happens.
		// FGI TODO: I need to look at the resync aspect
		resSubset, mappings, err := GetMonitorableResources(options.DiscoveryFunc, options.UsageRequests)
		if discovery.IsGroupDiscoveryFailedError(err) {
			utilruntime.HandleError(fmt.Errorf("discovery check failure, continuing and counting on future sync update: %v", err))
		} else if err != nil {
			return nil, err
		}
		um.usageRequests = resSubset
		um.grgvrMapping = mappings
		// um.measurers = map[schema.GroupResource]usage.Measurer{}
		if err = um.SyncMonitors(); err != nil {
			utilruntime.HandleError(fmt.Errorf("initial monitor sync has error: %v", err))
		}

		// only start quota once all informers synced
		// FGI TODO: we don't have a quota resource
		//rq.informerSyncedFuncs = append(rq.informerSyncedFuncs, qm.IsSynced)
	}

	return rc, nil
}

// enqueueAll is called at the fullResyncPeriod interval to force a full recalculation of usage statistics
// FGI TODO: this is not doing anything currently. The enqueue would need to be done based on the resources tracked
// rather than quotas
func (rc *Controller) enqueueAll() {
	defer klog.V(4).Infof("Resource usage controller queued all resource checks for full calculation of usage")
	// FGI the key should contain: namespace & GroupResource
	// rqs, err := rc.rqLister.List(labels.Everything())
	// if err != nil {
	//	utilruntime.HandleError(fmt.Errorf("unable to enqueue all - error listing resource quotas: %v", err))
	//	return
	// }
	// for i := range rqs {
	//	key, err := controller.KeyFunc(rqs[i])
	//	if err != nil {
	//		utilruntime.HandleError(fmt.Errorf("couldn't get key for object %+v: %v", rqs[i], err))
	//		continue
	//	}
	//	rq.queue.Add(key)
	//}
}

// obj could be an *v1.ResourceQuota, or a DeletionFinalStateUnknown marker item.
// FGI: TODO  some processing may be needed here but I cannot rely on the quota resource I need to have my own component: note the quota object is namespaced
// I need to have a structure that carries this information as well
// FGI: TODO my controller is not a real controller. There is no reconciliation. Hence this would not get called
/* func (rc *Controller) enqueueResource(obj interface{}) {
	key, err := controller.KeyFunc(obj)
	if err != nil {
		klog.Errorf("Couldn't get key for object %+v: %v", obj, err)
		return
	}
	rq.queue.Add(key)
}*/

// FGI TODO: This may not be needed if we have an initial mechanism to setup all the monitors. This should not change aftewards
/* func (rq *Controller) addQuota(obj interface{}) {
	key, err := controller.KeyFunc(obj)
	if err != nil {
		klog.Errorf("Couldn't get key for object %+v: %v", obj, err)
		return
	}

	resourceQuota := obj.(*v1.ResourceQuota)

	// if we declared an intent that is not yet captured in status (prioritize it)
	if !apiequality.Semantic.DeepEqual(resourceQuota.Spec.Hard, resourceQuota.Status.Hard) {
		rq.missingUsageQueue.Add(key)
		return
	}

	// if we declared a constraint that has no usage (which this controller can calculate, prioritize it)
	for constraint := range resourceQuota.Status.Hard {
		if _, usageFound := resourceQuota.Status.Used[constraint]; !usageFound {
			matchedResources := []v1.ResourceName{constraint}
			for _, evaluator := range rq.registry.List() {
				if intersection := evaluator.MatchingResources(matchedResources); len(intersection) > 0 {
					rq.missingUsageQueue.Add(key)
					return
				}
			}
		}
	}

	// no special priority, go in normal recalc queue
	rq.queue.Add(key)
} */

// worker runs a worker thread that just dequeues items, processes them, and marks them done.
//FGI 09.11 TODO
/* func (rq *Controller) worker(queue workqueue.RateLimitingInterface) func() {
	workFunc := func() bool {
		key, quit := queue.Get()
		if quit {
			return true
		}
		defer queue.Done(key)
		rq.workerLock.RLock()
		defer rq.workerLock.RUnlock()
		err := rq.syncHandler(key.(string))
		if err == nil {
			queue.Forget(key)
			return false
		}
		utilruntime.HandleError(err)
		queue.AddRateLimited(key)
		return false
	}

	return func() {
		for {
			if quit := workFunc(); quit {
				klog.Infof("resource quota controller worker shutting down")
				return
			}
		}
	}
}
*/

// Run begins usage controller using the specified number of workers
//FGI 09.11 TODO
/* func (ru *Controller) Run(workers int, stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	defer ru.queue.ShutDown()

	klog.Infof("Starting resource usage controller")
	defer klog.Infof("Shutting down resource usage controller")

	if ru.usageMonitor != nil {
		go ru.usageMonitor.Run(stopCh)
	}

	if !cache.WaitForNamedCacheSync("resource usage", stopCh, ru.informerSyncedFuncs...) {
		return
	}

	// the workers that chug through the usage calculation backlog
	for i := 0; i < workers; i++ {
		go wait.Until(ru.worker(ru.queue), time.Second, stopCh)
		go wait.Until(ru.worker(ru.missingUsageQueue), time.Second, stopCh)
	}
	// the timer for how often we do a full recalculation across all quotas
	if rq.resyncPeriod() > 0 {
		go wait.Until(func() { ru.enqueueAll() }, ru.resyncPeriod(), stopCh)
	} else {
		klog.Warningf("periodic quota controller resync disabled")
	}
	<-stopCh
}
*/

// syncResourceUsageFromKey syncs a usage key
// FGI TODO: this is based on the quota entity. I need to amend it so that my own structure is used instead
// I should be able to use a similar key: namespace + name.
// FGI HERE!!!
func (rc *Controller) syncResourceUsageFromKey(key string) (err error) {
	startTime := time.Now()
	defer func() {
		klog.V(4).Infof("Finished syncing resource usage %q (%v)", key, time.Since(startTime))
	}()

	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		return err
	}
	// I will replace that with my own structure
	// also depending on the resource I may want to report it at cluster or namespace level
	// lastNotifiedValue =  <<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<=================================================

	// FGI TODO It will be the recalculation of what I have called a Measurer. A measurer is specific to a resource
	// and may or may not be namespaced so that I will need to pass the namespace as a parameter
	// I will merge the code of syncResourceQuota here
	return rc.syncResourceQuota(resourceQuota)
}

// syncResourceQuota runs a complete sync of resource quota status across all known kinds
//FGI 09.11 TODO merge with syncResourceUsageFromKey
/* func (rq *Controller) syncResourceQuota(resourceQuota *v1.ResourceQuota) (err error) {
	// quota is dirty if any part of spec hard limits differs from the status hard limits
	statusLimitsDirty := !apiequality.Semantic.DeepEqual(resourceQuota.Spec.Hard, resourceQuota.Status.Hard)

	// dirty tracks if the usage status differs from the previous sync,
	// if so, we send a new usage with latest status
	// if this is our first sync, it will be dirty by default, since we need track usage
	dirty := statusLimitsDirty || resourceQuota.Status.Hard == nil || resourceQuota.Status.Used == nil

	used := v1.ResourceList{}
	if resourceQuota.Status.Used != nil {
		used = quota.Add(v1.ResourceList{}, resourceQuota.Status.Used)
	}
	hardLimits := quota.Add(v1.ResourceList{}, resourceQuota.Spec.Hard)

	var errs []error
	// FGI TODO: Here is the new usage calculated based on the namespace
	// My entity replacing resourceQuota shoud carry additional information like quota carries the scope
	// I should be able to carry whether the calculation should be cluster wide or namespace limited.
	// HERE!!!
	newUsage, err := quota.CalculateUsage(resourceQuota.Namespace, resourceQuota.Spec.Scopes, hardLimits, rq.registry, resourceQuota.Spec.ScopeSelector)
	if err != nil {	// I will split the processing. I am keeping replenishUsage for what it is: a replenishment for a specific resource. Recalculation will
		// be triggered in UsageMonitor.processResourceChanges and processed somewhere else.
		// if err is non-nil, remember it to return, but continue updating status with any resources in newUsage
		errs = append(errs, err)
	}
	for key, value := range newUsage {
		used[key] = value
	}

	// ensure set of used values match those that have hard constraints
	hardResources := quota.ResourceNames(hardLimits)
	used = quota.Mask(used, hardResources)

	// Create a usage object that is based on the quota resource version that will handle updates
	// by default, we preserve the past usage observation, and set hard to the current spec
	usage := resourceQuota.DeepCopy()
	usage.Status = v1.ResourceQuotaStatus{
		Hard: hardLimits,
		Used: used,
	}

	dirty = dirty || !quota.Equals(usage.Status.Used, resourceQuota.Status.Used)

	// there was a change observed by this controller that requires we update quota
	if dirty {
		_, err = rq.rqClient.ResourceQuotas(usage.Namespace).UpdateStatus(context.TODO(), usage, metav1.UpdateOptions{})
		if err != nil {
			errs = append(errs, err)
		}
	}
	return utilerrors.NewAggregate(errs)
}
*/

// replenishUsage is a replenishment function invoked by a controller to notify that a usage should be recalculated
// FGI: TODO some work to be done here
//FGI 13.11 TODO
func (rc *Controller) replenishUsage(groupResource schema.GroupResource, namespace string) {
	// check if the usage controller can evaluate this groupResource, if not, ignore it altogether...
	evaluator := rc.registry.Get(groupResource)
	if evaluator == nil {
		return
	}
	// FGI TODO: HERE!!!! The usage processing needs to be triggered
	// FGI: added custom logic for tracking usage in memory instead of in the quota resource
	// 2 options:
	// - Do I want to recalculate the complete resource as it was the case with quota?
	// - Do I want to get more information from the caller of replenishUsage, which is UsageMonitor.processResourceChanges in resource_monitor.go?
	// Having the event type (update, delete, etc), old object, new object would allow to do the maths rather than recalculating everything
	// which could be kept for the resyncs. This would be straightforward for Count: +1/+1. For Used, the diff would need to be made
	// Next: I need to look at the calculation done later in the call chain to see what can be reused.
	// I will split the processing. I am keeping replenishUsage for what it is: a replenishment for a specific resource. Recalculation will
	// be triggered in UsageMonitor.processResourceChanges and processed somewhere else.
	// check if this namespace even has a quota...
	/*resourceQuotas, err := rq.rqLister.ResourceQuotas(namespace).List(labels.Everything())
	if errors.IsNotFound(err) {
		utilruntime.HandleError(fmt.Errorf("quota controller could not find ResourceQuota associated with namespace: %s, could take up to %v before a quota replenishes", namespace, rq.resyncPeriod()))
		return
	}
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("error checking to see if namespace %s has any ResourceQuota associated with it: %v", namespace, err))
		return
	}
	if len(resourceQuotas) == 0 {
		return
	}

	// only queue those quotas that are tracking a resource associated with this kind.
	for i := range resourceQuotas {
		resourceQuota := resourceQuotas[i]
		resourceQuotaResources := quota.ResourceNames(resourceQuota.Status.Hard)
		if intersection := evaluator.MatchingResources(resourceQuotaResources); len(intersection) > 0 {
			// TODO: make this support targeted replenishment to a specific kind, right now it does a full recalc on that quota.
			rq.enqueueResourceQuota(resourceQuota)
		}
	}*/
}

// Sync periodically resyncs the controller when new resources are observed from discovery.
// FGI TODO: we should not need that as the resources should be pretty static.
/* func (rq *Controller) Sync(discoveryFunc ResourcesFunc, period time.Duration, stopCh <-chan struct{}) {
	// Something has changed, so track the new state and perform a sync.
	oldResources := make(map[schema.GroupVersionResource]struct{})
	wait.Until(func() {
		// Get the current resource list from discovery.
		newResources, err := GetMonitorableResources(discoveryFunc)
		if err != nil {
			utilruntime.HandleError(err)

			if discovery.IsGroupDiscoveryFailedError(err) && len(newResources) > 0 {
				// In partial discovery cases, don't remove any existing informers, just add new ones
				for k, v := range oldResources {
					newResources[k] = v
				}
			} else {
				// short circuit in non-discovery error cases or if discovery returned zero resources
				return
			}
		}

		// Decide whether discovery has reported a change.
		if reflect.DeepEqual(oldResources, newResources) {
			klog.V(4).Infof("no resource updates from discovery, skipping resource quota sync")
			return
		}

		// Ensure workers are paused to avoid processing events before informers
		// have resynced.
		rq.workerLock.Lock()
		defer rq.workerLock.Unlock()

		// Something has changed, so track the new state and perform a sync.
		if klog.V(2).Enabled() {
			klog.Infof("syncing resource quota controller with updated resources from discovery: %s", printDiff(oldResources, newResources))
		}

		// Perform the monitor resync and wait for controllers to report cache sync.
		if err := rq.resyncMonitors(newResources); err != nil {
			utilruntime.HandleError(fmt.Errorf("failed to sync resource monitors: %v", err))
			return
		}
		// wait for caches to fill for a while (our sync period).
		// this protects us from deadlocks where available resources changed and one of our informer caches will never fill.
		// informers keep attempting to sync in the background, so retrying doesn't interrupt them.
		// the call to resyncMonitors on the reattempt will no-op for resources that still exist.
		if rq.quotaMonitor != nil && !cache.WaitForNamedCacheSync("resource quota", waitForStopOrTimeout(stopCh, period), rq.quotaMonitor.IsSynced) {
			utilruntime.HandleError(fmt.Errorf("timed out waiting for quota monitor sync"))
			return
		}

		// success, remember newly synced resources
		oldResources = newResources
		klog.V(2).Infof("synced quota controller")
	}, period, stopCh)
}*/

// printDiff returns a human-readable summary of what resources were added and removed
func printDiff(oldResources, newResources map[schema.GroupVersionResource]struct{}) string {
	removed := sets.NewString()
	for oldResource := range oldResources {
		if _, ok := newResources[oldResource]; !ok {
			removed.Insert(fmt.Sprintf("%+v", oldResource))
		}
	}
	added := sets.NewString()
	for newResource := range newResources {
		if _, ok := oldResources[newResource]; !ok {
			added.Insert(fmt.Sprintf("%+v", newResource))
		}
	}
	return fmt.Sprintf("added: %v, removed: %v", added.List(), removed.List())
}

// waitForStopOrTimeout returns a stop channel that closes when the provided stop channel closes or when the specified timeout is reached
func waitForStopOrTimeout(stopCh <-chan struct{}, timeout time.Duration) <-chan struct{} {
	stopChWithTimeout := make(chan struct{})
	go func() {
		defer close(stopChWithTimeout)
		select {
		case <-stopCh:
		case <-time.After(timeout):
		}
	}()
	return stopChWithTimeout
}

// resyncMonitors starts or stops quota monitors as needed to ensure that all
// (and only) those resources present in the map are monitored.
// FGI TODO: we should not need that
//FGI 09.11 TODO
/*
func (rq *Controller) resyncMonitors(resources map[schema.GroupVersionResource]struct{}) error {
	if rq.quotaMonitor == nil {
		return nil
	}

	if err := rq.quotaMonitor.SyncMonitors(resources); err != nil {
		return err
	}
	rq.quotaMonitor.StartMonitors()
	return nil
}
*/

// GetMonitorableResources returns all resources that the monitor should recognize, filtered by what has been requested
// It requires a resource supports the following verbs: 'create','list','watch', 'delete'
// This function may return both results and an error.  If that happens, it means that the discovery calls were only
// partially successful.  A decision about whether to proceed or not is left to the caller.
// func GetMonitorableResources(discoveryFunc NamespacedResourcesFunc) (map[schema.GroupVersionResource]struct{}, error) {
func GetMonitorableResources(discoveryFunc ResourcesFunc, requests []usage.UsageRequest) (map[schema.GroupResource]usage.UsageRequest, map[schema.GroupResource]schema.GroupVersionResource, error) {
	possibleResources, discoveryErr := discoveryFunc()
	if discoveryErr != nil && len(possibleResources) == 0 {
		return nil, nil, fmt.Errorf("failed to discover resources: %v", discoveryErr)
	}
	monitorableResources := discovery.FilteredBy(discovery.SupportsAllVerbs{Verbs: []string{"create", "list", "watch", "delete"}}, possibleResources)
	monitorableGroupVersionResources, err := discovery.GroupVersionResources(monitorableResources)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse resources: %v", err)
	}
	filteredGRs, mappings := matchingGVR(monitorableGroupVersionResources, requests)
	// return the original discovery error (if any) in addition to the list
	return filteredGRs, mappings, discoveryErr
}

// matchingGVR returns the subset of the matches of discovered GVR with monitoring requests
func matchingGVR(resources map[schema.GroupVersionResource]struct{}, requests []usage.UsageRequest) (map[schema.GroupResource]usage.UsageRequest, map[schema.GroupResource]schema.GroupVersionResource) {
	matches := map[schema.GroupResource]usage.UsageRequest{}
	mappings := map[schema.GroupResource]schema.GroupVersionResource{}
	for _, request := range requests {
		for resource := range resources {
			if resource.GroupResource().String() == request.GroupResource.String() {
				matches[resource.GroupResource()] = request
				mappings[resource.GroupResource()] = resource
				klog.V(4).Infof("tracking usage of GroupResource: %s", resource.GroupResource().String())
			}
		}
	}
	return matches, mappings
}
