/*
Copyright 2026 Oreon IT.

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
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	harborv1alpha1 "github.com/oreondev/harbor-resource-operator/api/v1alpha1"
	"github.com/oreondev/harbor-resource-operator/pkg/harbor"
)

// Immutable tag rules and retention rules share Harbor's rule shape. These
// helpers build that shape from spec and compare it with what Harbor returns.

const scopeRepository = "repository"

func patternOrAll(p string) string {
	if p == "" {
		return "**"
	}
	return p
}

// repositorySelectors renders the repository scope selector.
func repositorySelectors(sel harborv1alpha1.PatternSelector) map[string][]harbor.TagSelector {
	dec := harbor.DecorationRepoMatches
	if sel.Decoration == harborv1alpha1.SelectorExcludes {
		dec = harbor.DecorationRepoExcludes
	}
	return map[string][]harbor.TagSelector{scopeRepository: {{
		Kind: harbor.SelectorKindDoublestar, Decoration: dec, Pattern: patternOrAll(sel.Pattern),
	}}}
}

// tagSelectors renders the tag selector. untagged is only used by retention.
func tagSelectors(sel harborv1alpha1.PatternSelector, untagged *bool) []harbor.TagSelector {
	dec := harbor.DecorationMatches
	if sel.Decoration == harborv1alpha1.SelectorExcludes {
		dec = harbor.DecorationExcludes
	}
	ts := harbor.TagSelector{Kind: harbor.SelectorKindDoublestar, Decoration: dec, Pattern: patternOrAll(sel.Pattern)}
	if untagged != nil {
		ts.Extras = fmt.Sprintf(`{"untagged":%t}`, *untagged)
	}
	return []harbor.TagSelector{ts}
}

// ruleKey renders a rule's meaningful content in canonical form, ignoring
// IDs, priority and JSON number formatting.
func ruleKey(r harbor.TagRule) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s|%s|%t|", r.Action, r.Template, r.Disabled)
	keys := make([]string, 0, len(r.Params))
	for k := range r.Params {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%v;", k, normalizeNumber(r.Params[k]))
	}
	b.WriteString("|")
	for _, s := range r.TagSelectors {
		b.WriteString(selectorKey(s))
	}
	b.WriteString("|")
	for _, s := range r.ScopeSelectors[scopeRepository] {
		b.WriteString(selectorKey(s))
	}
	return b.String()
}

func selectorKey(s harbor.TagSelector) string {
	extras := ""
	if s.Extras != "" {
		var m map[string]any
		if json.Unmarshal([]byte(s.Extras), &m) == nil {
			if u, ok := m["untagged"].(bool); ok {
				extras = fmt.Sprintf("untagged=%t", u)
			}
		}
	}
	return fmt.Sprintf("[%s %s %s %s]", s.Kind, s.Decoration, s.Pattern, extras)
}

// normalizeNumber prints JSON numbers decoded as float64 like integers.
func normalizeNumber(v any) any {
	if f, ok := v.(float64); ok && f == float64(int64(f)) {
		return int64(f)
	}
	return v
}
