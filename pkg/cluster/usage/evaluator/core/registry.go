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

package core

// FGI TODO: I may not need that as tracked resources may be explicitely requested
// I may still need to deal with aliases for legacy objects
import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	// "k8s.io/utils/clock"
	// "fgiloux/controller-tools/pkg/cluster/usage"
	// "fgiloux/controller-tools/pkg/cluster/usage/generic"
)

// legacyObjectCountAliases are what we used to do simple object counting with mapped to alias
var legacyObjectCountAliases = map[schema.GroupVersionResource]corev1.ResourceName{
	corev1.SchemeGroupVersion.WithResource("configmaps"):             corev1.ResourceConfigMaps,
	corev1.SchemeGroupVersion.WithResource("resourcequotas"):         corev1.ResourceQuotas,
	corev1.SchemeGroupVersion.WithResource("replicationcontrollers"): corev1.ResourceReplicationControllers,
	corev1.SchemeGroupVersion.WithResource("secrets"):                corev1.ResourceSecrets,
}

// NewEvaluators returns the list of static evaluators
// FGI TODO: that may manage more than counts as per the quota implementation
/* func NewEvaluators(f usage.ListerForResourceFunc) []usage.Evaluator {
	// these evaluators have special logic
	// result := []quota.Evaluator{
	//	NewPodEvaluator(f, clock.RealClock{}),
	//	NewServiceEvaluator(f),
	//	NewPersistentVolumeClaimEvaluator(f),
	//}
	result := []usage.Evaluator{}
	// these evaluators require an alias for backwards compatibility
	for gvr, alias := range legacyObjectCountAliases {
		result = append(result,
			generic.NewObjectCountEvaluator(gvr.GroupResource(), generic.ListResourceUsingListerFunc(f, gvr), alias))
	}
	return result
}
*/
