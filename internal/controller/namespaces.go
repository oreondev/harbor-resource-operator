/*
Copyright 2026 Fahri Cap.

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
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
)

// ParseWatchNamespaces turns the --watch-namespaces flag value into a sorted,
// de-duplicated list. An empty value means every namespace and returns nil.
func ParseWatchNamespaces(v string) ([]string, error) {
	var out []string
	for part := range strings.SplitSeq(v, ",") {
		ns := strings.TrimSpace(part)
		if ns == "" {
			continue
		}
		if errs := validation.IsDNS1123Label(ns); len(errs) > 0 {
			return nil, fmt.Errorf("invalid namespace %q in --watch-namespaces: %s", ns, strings.Join(errs, "; "))
		}
		out = append(out, ns)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}
