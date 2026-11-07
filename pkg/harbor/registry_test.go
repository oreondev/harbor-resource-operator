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

package harbor_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/oreondev/harbor-resource-operator/pkg/harbor"
	"github.com/oreondev/harbor-resource-operator/pkg/harbor/fake"
)

// pushSkill stores a skill artifact in project/repo as a push with ORAS would.
func pushSkill(srv *fake.Server, project, repo, name string, content []byte, tags ...string) (string, harbor.Descriptor) {
	cfg, _ := json.Marshal(map[string]string{"name": name, "description": "a test skill"})
	cd := srv.AddBlob(project, repo, harbor.SkillConfigType, cfg)
	ld := srv.AddBlob(project, repo, harbor.SkillContentType, content)
	d := srv.AddManifest(project, repo, harbor.Manifest{SchemaVersion: 2, MediaType: "application/vnd.oci.image.manifest.v1+json",
		ArtifactType: harbor.SkillArtifactType, Config: cd, Layers: []harbor.Descriptor{ld},
		Annotations: map[string]string{harbor.SkillNameAnnotation: name}}, tags...)
	return d, ld
}

func TestSkillArtifacts(t *testing.T) {
	srv := fake.New("admin", "pw")
	defer srv.Close()
	ctx := context.Background()
	c := newClient(t, srv, "admin", "pw")
	srv.AddProject("skills", false)
	srv.AddRepository("skills", "review")
	srv.AddArtifact("skills", "review", harbor.Artifact{Type: "IMAGE", Digest: "sha256:img"}, nil)
	d, layer := pushSkill(srv, "skills", "review", "review", []byte("tarball"), "1.0.0", "latest")

	arts, total, err := c.ListArtifactsOfType(ctx, "skills", "review", harbor.ArtifactTypeSkill, harbor.PageOptions{})
	if err != nil || total != 1 {
		t.Fatalf("skills: %d total, %v", total, err)
	}
	a := arts[0]
	if a.Digest != d || a.ArtifactType != harbor.SkillArtifactType || a.Annotations[harbor.SkillNameAnnotation] != "review" ||
		a.ExtraAttrs["description"] != "a test skill" || len(a.Tags) != 2 || a.Signed() {
		t.Fatalf("skill artifact: %+v", a)
	}

	m, err := c.GetManifest(ctx, "skills", "review", "1.0.0")
	if err != nil || m.Digest != d || m.ArtifactType != harbor.SkillArtifactType || len(m.Layers) != 1 {
		t.Fatalf("manifest: %+v, %v", m, err)
	}
	if _, err := c.GetManifest(ctx, "skills", "skills/review", d); err != nil {
		t.Fatalf("manifest by digest and full name: %v", err)
	}
	b, err := c.GetBlob(ctx, "skills", "review", m.Layers[0], 1<<10)
	if err != nil || string(b) != "tarball" {
		t.Fatalf("blob: %q, %v", b, err)
	}

	// Limits and digests are checked.
	if _, err := c.GetBlob(ctx, "skills", "review", layer, 3); !errors.Is(err, harbor.ErrTooLarge) {
		t.Fatalf("blob over the limit: want ErrTooLarge, got %v", err)
	}
	lying := layer
	lying.Size = 100
	if _, err := c.GetBlob(ctx, "skills", "review", lying, 1<<10); err == nil {
		t.Fatal("a blob whose size does not match its descriptor was accepted")
	}
	if _, err := c.GetManifest(ctx, "skills", "review", "sha256:"+fortyZeros+fortyZeros[:24]); !harbor.IsNotFound(err) {
		t.Fatalf("unknown digest: want 404, got %v", err)
	}
	for _, bad := range [][2]string{{"../etc", "1.0.0"}, {"review", "1.0.0?x=1"}, {"review", "../../x"}, {"Review", "1"}} {
		if _, err := c.GetManifest(ctx, "skills", bad[0], bad[1]); err == nil {
			t.Fatalf("GetManifest(%q, %q) should be refused", bad[0], bad[1])
		}
	}

	// With cosign enforcement on, Harbor refuses the unsigned manifest.
	srv.Locked(func() {
		for _, p := range srv.Projects {
			p.Metadata = &harbor.ProjectMetadata{EnableContentTrustCosign: "true"}
		}
	})
	_, err = c.GetManifest(ctx, "skills", "review", "1.0.0")
	var apiErr *harbor.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("unsigned skill under cosign policy: want 412, got %v", err)
	}
	srv.Sign(d)
	if _, err := c.GetManifest(ctx, "skills", "review", "1.0.0"); err != nil {
		t.Fatalf("signed skill under cosign policy: %v", err)
	}
	arts, _, _ = c.ListArtifactsOfType(ctx, "skills", "review", harbor.ArtifactTypeSkill, harbor.PageOptions{})
	if !arts[0].Signed() {
		t.Fatalf("signature accessory not listed: %+v", arts[0].Accessories)
	}
}

const fortyZeros = "0000000000000000000000000000000000000000"
