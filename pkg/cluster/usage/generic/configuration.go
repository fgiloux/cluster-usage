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
	// "k8s.io/apimachinery/pkg/runtime/schema"
)

// implements a basic configuration
type simpleConfiguration struct {
	evaluators []usage.Evaluator
	// ignoredResources map[schema.GroupResource]struct{}
}

// NewConfiguration creates a usage configuration
// func NewConfiguration(evaluators []usage.Evaluator, ignoredResources map[schema.GroupResource]struct{}) usage.Configuration {
func NewConfiguration(evaluators []usage.Evaluator) usage.Configuration {
	return &simpleConfiguration{
		evaluators: evaluators,
		// ignoredResources: ignoredResources,
	}
}

// func (c *simpleConfiguration) IgnoredResources() map[schema.GroupResource]struct{} {
// 	return c.ignoredResources
// }

func (c *simpleConfiguration) Evaluators() []usage.Evaluator {
	return c.evaluators
}
