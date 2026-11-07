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

package harbor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

// Reads from Harbor's registry API (/v2/), for the artifacts whose content
// is small and meant to be read (agent skills). Basic auth works on /v2/, so
// no token service is involved. Every digest is checked.

// Media types of the agent skills OCI artifact spec
// (github.com/ThomasVitale/agents-skills-oci-artifacts-spec, v0.1.0).
const (
	SkillArtifactType   = "application/vnd.agentskills.skill.v1"
	SkillConfigType     = "application/vnd.agentskills.skill.config.v1+json"
	SkillContentType    = "application/vnd.agentskills.skill.content.v1.tar+gzip"
	SkillNameAnnotation = "io.agentskills.skill.name"
)

// MaxManifestBytes caps a manifest read.
const MaxManifestBytes = 4 << 20

const manifestAccept = "application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json"

// Descriptor is an OCI content descriptor.
type Descriptor struct {
	MediaType    string            `json:"mediaType"`
	Digest       string            `json:"digest"`
	Size         int64             `json:"size"`
	ArtifactType string            `json:"artifactType,omitempty"`
	Annotations  map[string]string `json:"annotations,omitempty"`
}

// Manifest is an OCI image manifest. Digest is the digest of the bytes read,
// checked against the reference when that is a digest.
type Manifest struct {
	SchemaVersion int               `json:"schemaVersion"`
	MediaType     string            `json:"mediaType"`
	ArtifactType  string            `json:"artifactType,omitempty"`
	Config        Descriptor        `json:"config"`
	Layers        []Descriptor      `json:"layers"`
	Annotations   map[string]string `json:"annotations,omitempty"`
	Digest        string            `json:"-"`
}

// OCI distribution name, tag and digest grammar; checked before a name goes
// into a /v2/ path.
var (
	nameRe   = regexp.MustCompile(`^[a-z0-9]+(?:(?:\.|_|__|-+)[a-z0-9]+)*(?:/[a-z0-9]+(?:(?:\.|_|__|-+)[a-z0-9]+)*)*$`)
	tagRe    = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$`)
	digestRe = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
)

func registryPath(project, repo, kind, ref string) (string, error) {
	name := project + "/" + strings.TrimPrefix(repo, project+"/")
	if !nameRe.MatchString(name) {
		return "", fmt.Errorf("invalid repository name %q", name)
	}
	if !tagRe.MatchString(ref) && !digestRe.MatchString(ref) {
		return "", fmt.Errorf("invalid tag or digest %q", ref)
	}
	return "/v2/" + name + "/" + kind + "/" + ref, nil
}

// GetManifest reads the manifest of project/repo at a tag or digest. Harbor
// applies the project's pull policies here: with cosign enforcement on, an
// unsigned artifact answers 412 PROJECTPOLICYVIOLATION.
func (c *Client) GetManifest(ctx context.Context, project, repo, ref string) (*Manifest, error) {
	path, err := registryPath(project, repo, "manifests", ref)
	if err != nil {
		return nil, err
	}
	_, data, err := c.send(ctx, http.MethodGet, path, path, nil, nil, map[string]string{"Accept": manifestAccept}, MaxManifestBytes)
	if err != nil {
		return nil, err
	}
	d := sha256Digest(data)
	if strings.HasPrefix(ref, "sha256:") && ref != d {
		return nil, fmt.Errorf("harbor GET %s: manifest digest %s does not match", path, d)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("harbor GET %s: decode manifest: %w", path, err)
	}
	m.Digest = d
	return &m, nil
}

// GetBlob reads the blob d of project/repo, refusing more than limit bytes
// and content that does not match d's size and digest. Read blobs through a
// manifest from GetManifest: Harbor checks pull policies on manifests only.
func (c *Client) GetBlob(ctx context.Context, project, repo string, d Descriptor, limit int64) ([]byte, error) {
	path, err := registryPath(project, repo, "blobs", d.Digest)
	if err != nil {
		return nil, err
	}
	if d.Size > limit {
		return nil, fmt.Errorf("harbor GET %s: %w (%d bytes, blob is %d)", path, ErrTooLarge, limit, d.Size)
	}
	_, data, err := c.send(ctx, http.MethodGet, path, path, nil, nil, nil, limit)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != d.Size || sha256Digest(data) != d.Digest {
		return nil, fmt.Errorf("harbor GET %s: content does not match its digest", path)
	}
	return data, nil
}

func sha256Digest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}
