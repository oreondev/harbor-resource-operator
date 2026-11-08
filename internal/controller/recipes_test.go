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
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// The recipes under config/samples/recipes are documented as ready to apply,
// so every object in them must pass the CRD schemas with strict field
// validation, and a docs page must show each one in full. They are created
// as a dry run, so no controller acts on them.
var _ = Describe("Recipes", func() {
	It("pass the CRD schemas", func() {
		paths, err := filepath.Glob(filepath.Join("..", "..", "config", "samples", "recipes", "*.yaml"))
		Expect(err).NotTo(HaveOccurred())
		Expect(paths).NotTo(BeEmpty())
		pages, err := filepath.Glob(filepath.Join("..", "..", "docs", "*.md"))
		Expect(err).NotTo(HaveOccurred())
		var docs []byte
		for _, p := range pages {
			b, err := os.ReadFile(p)
			Expect(err).NotTo(HaveOccurred())
			docs = append(docs, b...)
		}
		for _, p := range paths {
			data, err := os.ReadFile(p)
			Expect(err).NotTo(HaveOccurred())
			Expect(bytes.Contains(docs, data)).To(BeTrue(), "no page under docs/ shows %s as it is", p)
			dec := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
			n := 0
			for {
				obj := &unstructured.Unstructured{}
				err := dec.Decode(&obj.Object)
				if errors.Is(err, io.EOF) {
					break
				}
				Expect(err).NotTo(HaveOccurred(), p)
				if len(obj.Object) == 0 {
					continue
				}
				ns := &corev1.Namespace{}
				ns.Name = obj.GetNamespace()
				Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, ns))).To(Succeed())
				Expect(k8sClient.Create(ctx, obj, client.DryRunAll, client.FieldValidation("Strict"))).
					To(Succeed(), "%s: %s %s", p, obj.GetKind(), obj.GetName())
				n++
			}
			Expect(n).To(BeNumerically(">", 0), p)
		}
	})
})
