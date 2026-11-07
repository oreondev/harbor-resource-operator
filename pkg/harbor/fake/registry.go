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

package fake

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/oreondev/harbor-resource-operator/pkg/harbor"
)

// Registry API (/v2/) for the content of small artifacts. Blobs are stored
// per repository; manifests are stored on the artifact they describe.

// vTrue is Harbor's string form of a true flag or query parameter.
const vTrue = "true"

// typeRe is the rule of Harbor's default artifact processor: the type is the
// part of the config media type between the vendor and ".config".
var typeRe = regexp.MustCompile(`^application/vnd\.[^.]*\.(.*)\.config\.[^.]*\+json$`)

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// AddBlob stores data in project/repo (the repository must exist) and returns
// its descriptor with the given media type.
func (s *Server) AddBlob(project, repo, mediaType string, data []byte) harbor.Descriptor {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := harbor.Descriptor{MediaType: mediaType, Digest: digestOf(data), Size: int64(len(data))}
	if rp := s.repositoryNamed(project, repo); rp != nil {
		if s.blobs[rp.ID] == nil {
			s.blobs[rp.ID] = map[string][]byte{}
		}
		s.blobs[rp.ID][d.Digest] = data
	}
	return d
}

// AddManifest stores m in project/repo as a new artifact with the given tags
// and returns its digest ("" when the repository does not exist). The
// artifact's type, artifact_type, annotations and extra_attrs are derived as
// Harbor does: the type from the config media type ("SKILL" for agent
// skills, "UNKNOWN" otherwise) and extra_attrs from the config blob.
func (s *Server) AddManifest(project, repo string, m harbor.Manifest, tags ...string) string {
	raw, _ := json.Marshal(m)
	s.mu.Lock()
	defer s.mu.Unlock()
	rp := s.repositoryNamed(project, repo)
	if rp == nil {
		return ""
	}
	d := digestOf(raw)
	typ := "UNKNOWN"
	if g := typeRe.FindStringSubmatch(m.Config.MediaType); g != nil {
		typ = strings.ToUpper(g[1])
	}
	var extra map[string]any
	_ = json.Unmarshal(s.blobs[rp.ID][m.Config.Digest], &extra)
	a := harbor.Artifact{Type: typ, MediaType: m.Config.MediaType, Digest: d, ArtifactType: m.ArtifactType,
		Annotations: m.Annotations, ExtraAttrs: extra, Size: int64(len(raw)) + m.Config.Size}
	for _, l := range m.Layers {
		a.Size += l.Size
	}
	for _, t := range tags {
		a.Tags = append(a.Tags, harbor.Tag{Name: t})
	}
	id := s.addArtifact(project, repo, a, nil)
	s.Artifacts[id].manifest = raw
	return d
}

// Sign attaches a cosign signature accessory to the artifact with digest d.
func (s *Server) Sign(d string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.Artifacts {
		if a.a.Digest == d {
			a.a.Accessories = append(a.a.Accessories, harbor.Accessory{Type: harbor.AccessoryCosign, Digest: digestOf([]byte("sig " + d))})
		}
	}
}

// registry serves GET /v2/<name>/manifests/<ref> and /v2/<name>/blobs/<digest>.
func (s *Server) registry(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/v2/")
	kind, name, ref := "manifests", "", ""
	if i := strings.LastIndex(rest, "/manifests/"); i > 0 {
		name, ref = rest[:i], rest[i+len("/manifests/"):]
	} else if i := strings.LastIndex(rest, "/blobs/"); i > 0 {
		kind, name, ref = "blobs", rest[:i], rest[i+len("/blobs/"):]
	} else {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "not found")
		return
	}
	project, repo, _ := strings.Cut(name, "/")
	s.mu.Lock()
	defer s.mu.Unlock()
	rp := s.repositoryNamed(project, repo)
	if rp == nil {
		writeErr(w, http.StatusNotFound, "NAME_UNKNOWN", "repository name not known to registry")
		return
	}
	if kind == "blobs" {
		b, ok := s.blobs[rp.ID][ref]
		if !ok {
			writeErr(w, http.StatusNotFound, "BLOB_UNKNOWN", "blob unknown to registry")
			return
		}
		w.Header().Set("Docker-Content-Digest", ref)
		_, _ = w.Write(b)
		return
	}
	for _, a := range s.Artifacts {
		if a.repoID != rp.ID || a.manifest == nil ||
			(a.a.Digest != ref && !slices.ContainsFunc(a.a.Tags, func(t harbor.Tag) bool { return t.Name == ref })) {
			continue
		}
		if p := s.projectNamed(project); p.Metadata != nil && p.Metadata.EnableContentTrustCosign == vTrue && !a.a.Signed() {
			writeErr(w, http.StatusPreconditionFailed, "PROJECTPOLICYVIOLATION", "The image is not signed by cosign.")
			return
		}
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		w.Header().Set("Docker-Content-Digest", a.a.Digest)
		_, _ = w.Write(a.manifest)
		return
	}
	writeErr(w, http.StatusNotFound, "MANIFEST_UNKNOWN", "manifest unknown")
}
