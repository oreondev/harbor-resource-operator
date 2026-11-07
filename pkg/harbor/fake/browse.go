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
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/oreondev/harbor-resource-operator/pkg/harbor"
)

// reportMIME is the key Harbor uses for its own vulnerability reports.
const reportMIME = "application/vnd.security.vulnerability.report; version=1.1"

// artifact is a stored artifact with the repository it belongs to and its
// scan report, if any.
type artifact struct {
	repoID int64
	a      harbor.Artifact
	report *harbor.VulnerabilityReport
	// manifest is the raw manifest served on /v2/, if added with AddManifest.
	manifest []byte
}

func (s *Server) registerBrowse(mux *http.ServeMux, p string) {
	mux.HandleFunc("GET "+p+"/projects", s.listProjects)
	mux.HandleFunc("GET "+p+"/projects/{id}/repositories", s.listRepositories)
	mux.HandleFunc("GET "+p+"/projects/{id}/repositories/{repo}/artifacts", s.listArtifacts)
	mux.HandleFunc("GET "+p+"/projects/{id}/repositories/{repo}/artifacts/{ref}/additions/{addition}", s.getAddition)
	mux.HandleFunc("GET /v2/", s.registry)
}

// AddRepository seeds a repository in an existing project and returns its ID
// (0 when the project does not exist). name may include the project prefix.
func (s *Server) AddRepository(project, name string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.projectNamed(project)
	if p == nil {
		return 0
	}
	id := s.id()
	now := time.Now().UTC()
	s.Repositories[id] = &harbor.Repository{ID: id, ProjectID: p.ProjectID, Name: project + "/" + strings.TrimPrefix(name, project+"/"),
		CreationTime: now, UpdateTime: now}
	p.RepoCount++
	return id
}

// AddArtifact seeds an artifact, with an optional scan report, in an existing
// repository and returns its ID (0 when the repository does not exist). The
// artifact's ScanOverview is kept as given.
func (s *Server) AddArtifact(project, repo string, a harbor.Artifact, report *harbor.VulnerabilityReport) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addArtifact(project, repo, a, report)
}

func (s *Server) addArtifact(project, repo string, a harbor.Artifact, report *harbor.VulnerabilityReport) int64 {
	rp := s.repositoryNamed(project, repo)
	if rp == nil {
		return 0
	}
	id := s.id()
	a.ID, a.ProjectID, a.RepositoryName = id, rp.ProjectID, rp.Name
	if a.PushTime.IsZero() {
		a.PushTime = time.Now().UTC()
	}
	s.Artifacts[id] = &artifact{repoID: rp.ID, a: a, report: report}
	rp.ArtifactCount++
	return id
}

func (s *Server) projectNamed(name string) *harbor.Project {
	for _, p := range s.Projects {
		if p.Name == name {
			return p
		}
	}
	return nil
}

func (s *Server) repositoryNamed(project, repo string) *harbor.Repository {
	full := project + "/" + strings.TrimPrefix(repo, project+"/")
	for _, r := range s.Repositories {
		if r.Name == full {
			return r
		}
	}
	return nil
}

// repoFromPath resolves {id} (project name) and {repo} (URL-encoded once more
// by the client, as Harbor requires) or writes 404.
func (s *Server) repoFromPath(w http.ResponseWriter, r *http.Request) *harbor.Repository {
	project := r.PathValue("id")
	if s.projectNamed(project) == nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "project "+project+" not found")
		return nil
	}
	repo, err := url.PathUnescape(r.PathValue("repo"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid repository name")
		return nil
	}
	rp := s.repositoryNamed(project, repo)
	if rp == nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "repository "+project+"/"+repo+" not found")
		return nil
	}
	return rp
}

// writePage writes items[a:b] of a filtered list with X-Total-Count.
func writePage[T any](w http.ResponseWriter, r *http.Request, items []T) {
	a, b := page(r, len(items))
	w.Header().Set("X-Total-Count", strconv.Itoa(len(items)))
	writeJSON(w, items[a:b])
}

func fuzzyQuery(r *http.Request) string {
	f, _ := strings.CutPrefix(r.URL.Query().Get("q"), "name=~")
	return f
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := fuzzyQuery(r)
	ids := make([]int64, 0, len(s.Projects))
	for id := range s.Projects {
		ids = append(ids, id)
	}
	sortInts(ids)
	out := []harbor.Project{}
	for _, id := range ids {
		if p := s.Projects[id]; strings.Contains(p.Name, f) {
			out = append(out, *p)
		}
	}
	writePage(w, r, out)
}

func (s *Server) listRepositories(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.projectNamed(r.PathValue("id"))
	if p == nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "project "+r.PathValue("id")+" not found")
		return
	}
	f := fuzzyQuery(r)
	ids := make([]int64, 0, len(s.Repositories))
	for id := range s.Repositories {
		ids = append(ids, id)
	}
	sortInts(ids)
	out := []harbor.Repository{}
	for _, id := range ids {
		if rp := s.Repositories[id]; rp.ProjectID == p.ProjectID && strings.Contains(rp.Name, f) {
			out = append(out, *rp)
		}
	}
	writePage(w, r, out)
}

// listArtifacts answers 200 with an empty list for a missing project or
// repository, as Harbor 2.15 does.
func (s *Server) listArtifacts(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []harbor.Artifact{}
	repo, _ := url.PathUnescape(r.PathValue("repo"))
	rp := s.repositoryNamed(r.PathValue("id"), repo)
	if rp == nil {
		writePage(w, r, out)
		return
	}
	withScan := r.URL.Query().Get("with_scan_overview") == vTrue
	withAccessory := r.URL.Query().Get("with_accessory") == vTrue
	typ, byType := strings.CutPrefix(r.URL.Query().Get("q"), "type=")
	for _, a := range s.Artifacts {
		if a.repoID != rp.ID || (byType && a.a.Type != typ) {
			continue
		}
		c := a.a
		if !withScan {
			c.ScanOverview = nil
		}
		if !withAccessory {
			c.Accessories = nil
		}
		out = append(out, c)
	}
	// Newest push first, as with sort=-push_time.
	slices.SortFunc(out, func(x, y harbor.Artifact) int { return y.PushTime.Compare(x.PushTime) })
	writePage(w, r, out)
}

func (s *Server) getAddition(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Harbor validates the addition before looking anything up.
	if r.PathValue("addition") != "vulnerabilities" {
		writeErr(w, http.StatusUnprocessableEntity, "UNPROCESSABLE_ENTITY",
			"addition in path should be one of [build_history values.yaml readme.md dependencies vulnerabilities]")
		return
	}
	rp := s.repoFromPath(w, r)
	if rp == nil {
		return
	}
	ref := r.PathValue("ref")
	for _, a := range s.Artifacts {
		if a.repoID != rp.ID || (a.a.Digest != ref && !slices.ContainsFunc(a.a.Tags, func(t harbor.Tag) bool { return t.Name == ref })) {
			continue
		}
		if s.NoScanner {
			writeErr(w, http.StatusNotFound, "NOT_FOUND",
				"no scanner registration configured for project: "+strconv.FormatInt(rp.ProjectID, 10))
			return
		}
		if a.report == nil {
			writeJSON(w, map[string]any{})
			return
		}
		writeJSON(w, map[string]harbor.VulnerabilityReport{reportMIME: *a.report})
		return
	}
	writeErr(w, http.StatusNotFound, "NOT_FOUND", "artifact "+rp.Name+":"+ref+" not found")
}
