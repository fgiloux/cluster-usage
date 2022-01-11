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
	"sync"

	"fgiloux/controller-tools/pkg/cluster/usage"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// simpleRegistry implements a basic registry.
// the registry contains a map of evaluators. The key is the matching resource.
type simpleRegistry struct {
	lock sync.RWMutex
	// evaluators tracked by the registry
	evaluators map[schema.GroupResource]usage.Evaluator
}

// NewRegistry creates a simple registry with initial list of evaluators
func NewRegistry(evaluators []usage.Evaluator) usage.Registry {
	return &simpleRegistry{
		evaluators: evaluatorsByGroupResource(evaluators),
	}
}

func (r *simpleRegistry) Add(e usage.Evaluator) {
	r.lock.Lock()
	defer r.lock.Unlock()
	r.evaluators[e.GroupResource()] = e
}

func (r *simpleRegistry) Remove(e usage.Evaluator) {
	r.lock.Lock()
	defer r.lock.Unlock()
	delete(r.evaluators, e.GroupResource())
}

func (r *simpleRegistry) Get(gr schema.GroupResource) usage.Evaluator {
	r.lock.RLock()
	defer r.lock.RUnlock()
	return r.evaluators[gr]
}

func (r *simpleRegistry) List() []usage.Evaluator {
	r.lock.RLock()
	defer r.lock.RUnlock()

	return evaluatorsList(r.evaluators)
}

// evaluatorsByGroupResource converts a list of evaluators to a map by group resource.
func evaluatorsByGroupResource(items []usage.Evaluator) map[schema.GroupResource]usage.Evaluator {
	result := map[schema.GroupResource]usage.Evaluator{}
	for _, item := range items {
		result[item.GroupResource()] = item
	}
	return result
}

// evaluatorsList converts a map of evaluators to list
func evaluatorsList(input map[schema.GroupResource]usage.Evaluator) []usage.Evaluator {
	var result []usage.Evaluator
	for _, item := range input {
		result = append(result, item)
	}
	return result
}
