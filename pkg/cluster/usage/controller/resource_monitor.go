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
	"fmt"
	"sync"
	"time"

	"k8s.io/klog/v2"

	"fgiloux/controller-tools/pkg/cluster/usage"
	"fgiloux/controller-tools/pkg/cluster/usage/generic"
	"fgiloux/controller-tools/pkg/cluster/usage/informerfactory"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	// k8scontroller "k8s.io/kubernetes/pkg/controller"
	// "k8s.io/controller-manager/app"
	// "k8s.io/utils/clock"
)

// ResyncPeriodFunc generates a duration each time it is invoked; this is so that
// multiple controllers don't get into lock-step and all hammer the apiserver
// with list requests simultaneously.
// type ResyncPeriodFunc func() time.Duration

// ReplenishmentFunc is a signal that a resource changed in specified namespace
// that may require quota to be recalculated.
// type ReplenishmentFunc func(groupResource schema.GroupResource, namespace string)

// UsageMonitor contains all necessary information to track usage and trigger replenishments
type UsageMonitor struct {
	// each monitor list/watches a resource and determines if we should replenish usage
	monitors    monitors
	monitorLock sync.RWMutex
	// informersStarted is closed after after all of the controllers have been initialized and are running.
	// After that it is safe to start them here, before that it is not.
	informersStarted <-chan struct{}

	// stopCh drives shutdown. When a receive from it unblocks, monitors will shut down.
	// This channel is also protected by monitorLock.
	stopCh <-chan struct{}

	// running tracks whether Run() has been called.
	// it is protected by monitorLock.
	running bool

	// monitors are the producer of the resourceChanges queue
	resourceChanges workqueue.RateLimitingInterface

	// interfaces with informers
	informerFactory informerfactory.InformerFactory

	// list of resources to ignore
	// ignoredResources map[schema.GroupResource]struct{}

	// The period that should be used to re-sync the monitored resource
	resyncPeriod ResyncPeriodFunc

	// callback to alert that a change may require usage recalculation
	replenishment ReplenishmentFunc

	// maintains list of evaluators
	registry usage.Registry

	// usageRequests contains a map of the monitored resources with the usage tracking details
	usageRequests map[schema.GroupResource]usage.UsageRequest

	// measurers contains a map of the Measurers
	// FGI TODO: Currently using the registry above
	// evaluators map[schema.GroupResource]usage.Evaluator

	// lastNotifications contains a map with the last results that have been notified.
	// This is tracked for the threshold based notification mechanism.
	lastNotifications map[schema.GroupResource]usage.UsageResponse

	// grgvrMapping offers quick lookup of the cluster preferred GroupVersionResource for a given GroupResource
	grgvrMapping map[schema.GroupResource]schema.GroupVersionResource
}

// NewMonitor creates a new instance of a UsageMonitor
// FGI: TODO I have not seen that called anywhere
/* func NewMonitor(informersStarted <-chan struct{}, informerFactory informerfactory.InformerFactory, ignoredResources map[schema.GroupResource]struct{}, resyncPeriod usage.ResyncPeriodFunc, replenishment usage.ReplenishmentFunc, registry usage.Registry) *UsageMonitor {
return &UsageMonitor{
	informersStarted: informersStarted,
	informerFactory:  informerFactory,
	ignoredResources: ignoredResources,
	resourceChanges:  workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "resource_controller_resource_changes"),
	resyncPeriod:     resyncPeriod,
	replenishment:    replenishment,
	registry:         registry,
}
}*/

// monitor runs a Controller with a local stop channel.
type monitor struct {
	controller cache.Controller

	// stopCh stops Controller. If stopCh is nil, the monitor is considered to be
	// not yet started.
	stopCh chan struct{}
}

// Run is intended to be called in a goroutine. Multiple calls of this is an
// error.
func (m *monitor) Run() {
	m.controller.Run(m.stopCh)
}

type monitors map[schema.GroupResource]*monitor

func (um *UsageMonitor) controllerFor(resource schema.GroupResource) (cache.Controller, error) {
	handlers := cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			event := &usage.Event{
				EventType: usage.AddEvent,
				Obj:       obj,
				GR:        resource,
			}
			um.resourceChanges.Add(event)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			// TODO: leaky abstraction!  live w/ it for now, but should pass down an update filter func.
			// we only want to queue the updates we care about though as too much noise will overwhelm queue.
			notifyUpdate := false
			switch resource {
			case schema.GroupResource{Resource: "pods"}:
				oldPod := oldObj.(*v1.Pod)
				newPod := newObj.(*v1.Pod)
				// FGI TODO: the logic is needed to discreminate terminating pods but we should not have a dependency on quota
				// the code should be replicated here:
				// https://github.com/kubernetes/kubernetes/blob/v1.22.2/pkg/quota/v1/evaluator/core/pods.go#L444-L463
				// notifyUpdate = core.QuotaV1Pod(oldPod, clock.RealClock{}) && !core.QuotaV1Pod(newPod, clock.RealClock{})
				notifyUpdate = (oldPod.Status.Phase != v1.PodFailed && oldPod.Status.Phase != v1.PodSucceeded) &&
					(newPod.Status.Phase == v1.PodFailed || newPod.Status.Phase == v1.PodSucceeded)
			case schema.GroupResource{Resource: "services"}:
				oldService := oldObj.(*v1.Service)
				newService := newObj.(*v1.Service)
				// FGI TODO: as above
				// https://github.com/kubernetes/kubernetes/blob/v1.22.2/pkg/quota/v1/evaluator/core/services.go#L167-L176
				// notifyUpdate = core.GetQuotaServiceType(oldService) != core.GetQuotaServiceType(newService)
				notifyUpdate = oldService.Spec.Type != newService.Spec.Type
			}
			if notifyUpdate {
				event := &usage.Event{
					EventType: usage.UpdateEvent,
					Obj:       newObj,
					OldObj:    oldObj,
					GR:        resource,
				}
				um.resourceChanges.Add(event)
			}
		},
		DeleteFunc: func(obj interface{}) {
			// delta fifo may wrap the object in a cache.DeletedFinalStateUnknown, unwrap it
			if deletedFinalStateUnknown, ok := obj.(cache.DeletedFinalStateUnknown); ok {
				obj = deletedFinalStateUnknown.Obj
			}
			event := &usage.Event{
				EventType: usage.DeleteEvent,
				Obj:       obj,
				GR:        resource,
			}
			um.resourceChanges.Add(event)
		},
	}
	shared, err := um.informerFactory.ForResource(um.grgvrMapping[resource])
	if err == nil {
		klog.V(4).Infof("UsageMonitor using a shared informer for resource %q", resource.String())
		shared.Informer().AddEventHandlerWithResyncPeriod(handlers, um.resyncPeriod())
		return shared.Informer().GetController(), nil
	}
	klog.V(4).Infof("UsageMonitor unable to use a shared informer for resource %q: %v", resource.String(), err)

	// TODO: currently not covering resources managed by aggregated api servers.
	return nil, fmt.Errorf("unable to monitor usage for resource %q", resource.String())
}

// SyncMonitors rebuilds the monitor set according to the supplied resources,
// creating or deleting monitors as necessary. It will return any error
// encountered, but will make an attempt to create a monitor for each resource
// instead of immediately exiting on an error. It may be called before or after
// Run. Monitors are NOT started as part of the sync. To ensure all existing
// monitors are started, call StartMonitors.
// FGI TODO: This should be more initMonitors as I may not intend to resync once the monitors have been created
// I will keep it for now as is. This would provide the option to the operator authors to dynamically change
// the monitors and evaluators
func (um *UsageMonitor) SyncMonitors() error {
	um.monitorLock.Lock()
	defer um.monitorLock.Unlock()

	toRemove := um.monitors
	if toRemove == nil {
		toRemove = monitors{}
	}
	current := monitors{}
	var errs []error
	kept := 0
	added := 0
	for resource, request := range um.usageRequests {
		// if _, ok := um.ignoredResources[resource.GroupResource()]; ok {
		//	continue
		// }
		///resource := um.grgvrMapping[grResource]
		if m, ok := toRemove[resource]; ok {
			current[resource] = m
			delete(toRemove, resource)
			kept++
			continue
		}
		c, err := um.controllerFor(resource)
		if err != nil {
			errs = append(errs, fmt.Errorf("couldn't start monitor for resource %q: %v", resource, err))
			continue
		}

		// check if we need to create an evaluator for this resource (if none previously registered)
		// note that it means that only a single request per resource is supported
		// supporting multiple requests per resource (with different filters) could be considered
		evaluator := um.registry.Get(resource)
		if evaluator == nil {
			listerFunc := generic.ListerFuncForResourceFunc(um.informerFactory.ForResource)
			listResourceFunc := generic.ListResourceUsingListerFunc(listerFunc, um.grgvrMapping[resource], um.usageRequests[resource])
			// evaluator = generic.NewObjectCountEvaluator(resource, listResourceFunc, "")
			// FGI TODO: the configuration of an external processor is not implemented yet
			if request.PerNamespace {
				evaluator = generic.NewNamespacedEvaluator(resource, listResourceFunc, nil)
			} else {
				evaluator = generic.NewSimpleEvaluator(resource, listResourceFunc, nil)
			}
			um.registry.Add(evaluator)
			klog.Infof("UsageMonitor created object count evaluator for %s", resource)
		}

		// track the monitor
		current[resource] = &monitor{controller: c}
		added++
	}
	um.monitors = current

	for _, monitor := range toRemove {
		if monitor.stopCh != nil {
			close(monitor.stopCh)
		}
	}

	klog.V(4).Infof(" synced monitors; added %d, kept %d, removed %d", added, kept, len(toRemove))
	// NewAggregate returns nil if errs is 0-length
	return utilerrors.NewAggregate(errs)
}

// StartMonitors ensures the current set of monitors are running. Any newly
// started monitors will also cause shared informers to be started.
//
// If called before Run, StartMonitors does nothing (as there is no stop channel
// to support monitor/informer execution).
func (um *UsageMonitor) StartMonitors() {
	um.monitorLock.Lock()
	defer um.monitorLock.Unlock()
	// FGI TODO: I should not need that as it should only be called by Run once
	if !um.running {
		return
	}

	// we're waiting until after the informer start that happens once all the controllers are initialized.  This ensures
	// that they don't get unexpected events on their work queues.
	<-um.informersStarted

	monitors := um.monitors
	started := 0
	for _, monitor := range monitors {
		// FGI TODO: I should not need the stopCh check as it should only be called once at startup
		// This also means that I should be able to take the informerFactory.Start out of the loop.
		if monitor.stopCh == nil {
			monitor.stopCh = make(chan struct{})
			um.informerFactory.Start(um.stopCh)
			go monitor.Run()
			started++
		}
	}
	klog.V(4).Infof("UsageMonitor started %d new monitors, %d currently running", started, len(monitors))
}

// IsSynced returns true if any monitors exist AND all those monitors'
// controllers HasSynced functions return true. This means IsSynced could return
// true at one time, and then later return false if all monitors were
// reconstructed.
func (um *UsageMonitor) IsSynced() bool {
	um.monitorLock.RLock()
	defer um.monitorLock.RUnlock()

	if len(um.monitors) == 0 {
		klog.V(4).Info("usage monitor not synced: no monitors")
		return false
	}

	for resource, monitor := range um.monitors {
		if !monitor.controller.HasSynced() {
			klog.V(4).Infof("usage monitor not synced: %v", resource)
			return false
		}
	}
	return true
}

// Run sets the stop channel and starts monitor execution until stopCh is
// closed. Any running monitors will be stopped before Run returns.
func (um *UsageMonitor) Run(stopCh <-chan struct{}) {
	klog.Infof("UsageMonitor running")
	defer klog.Infof("UsageMonitor stopping")

	// Set up the stop channel.
	um.monitorLock.Lock()
	um.stopCh = stopCh
	um.running = true
	um.monitorLock.Unlock()

	// Start monitors and begin change processing until the stop channel is
	// closed.
	um.StartMonitors()
	wait.Until(um.runProcessResourceChanges, 1*time.Second, stopCh)

	// Stop any running monitors.
	um.monitorLock.Lock()
	defer um.monitorLock.Unlock()
	monitors := um.monitors
	stopped := 0
	for _, monitor := range monitors {
		if monitor.stopCh != nil {
			stopped++
			close(monitor.stopCh)
		}
	}
	klog.Infof("UsageMonitor stopped %d of %d monitors", stopped, len(monitors))
}

func (um *UsageMonitor) runProcessResourceChanges() {
	for um.processResourceChanges() {
	}
}

// processResourceChange Dequeues an event from resourceChanges to process
func (um *UsageMonitor) processResourceChanges() bool {
	item, quit := um.resourceChanges.Get()
	if quit {
		return false
	}
	defer um.resourceChanges.Done(item)
	event, ok := item.(*usage.Event)
	if !ok {
		utilruntime.HandleError(fmt.Errorf("expect a *event, got %v", item))
		return true
	}
	obj := event.Obj
	accessor, err := meta.Accessor(obj)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("cannot access obj: %v", err))
		return true
	}
	klog.V(4).Infof("UsageMonitor process object: %s, namespace %s, name %s, uid %s, event type %v", event.GR.String(), accessor.GetNamespace(), accessor.GetName(), string(accessor.GetUID()), event.EventType)
	// um.replenishment(event.gr, accessor.GetNamespace())
	evaluator := um.registry.Get(event.GR)
	newMeasures := evaluator.ProcessChange(event)
	um.processNotification(event.GR, newMeasures)
	return true
}

// processNotification: checks whether the threshold has been reached since the last notification.
// If it is a new notification is sent and lastNotifications updated accordingly
// FGI TODO um.lastNotifications map[schema.GroupResource]usage.UsageResponse, thresholds and channels are in um.usageRequests map[schema.GroupResource]usage.UsageRequest
func (um *UsageMonitor) processNotification(groupResource schema.GroupResource, newMeasures map[string]usage.Measure) {

}
