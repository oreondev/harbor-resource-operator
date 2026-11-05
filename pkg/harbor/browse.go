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
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Read-only listing of projects, repositories and artifacts, one page at a
// time with Harbor's total count, for callers that show results to people
// (the Harbor MCP server). The operator itself does not use these.

// PageOptions selects one page of a list. Page starts at 1 (0 means 1);
// PageSize 0 means DefaultPageSize and is capped at 100.
type PageOptions struct {
	Page     int
	PageSize int
}

// DefaultPageSize is the page size when PageOptions.PageSize is 0.
const DefaultPageSize = 25

// acceptVulnerabilities asks Harbor for scan overviews in both report formats.
var acceptVulnerabilities = map[string]string{
	"X-Accept-Vulnerabilities": "application/vnd.security.vulnerability.report; version=1.1, " +
		"application/vnd.scanner.adapter.vuln.report.harbor+json; version=1.0",
}

// Repository is the subset of a Harbor repository shown to users. Name
// includes the project, as in "library/nginx".
type Repository struct {
	ID            int64     `json:"id"`
	ProjectID     int64     `json:"project_id"`
	Name          string    `json:"name"`
	Description   string    `json:"description,omitempty"`
	ArtifactCount int64     `json:"artifact_count"`
	PullCount     int64     `json:"pull_count"`
	CreationTime  time.Time `json:"creation_time"`
	UpdateTime    time.Time `json:"update_time"`
}

// Tag is a tag of an artifact.
type Tag struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	PushTime  time.Time `json:"push_time"`
	PullTime  time.Time `json:"pull_time"`
	Immutable bool      `json:"immutable"`
}

// Scanner identifies the scanner that produced a report.
type Scanner struct {
	Name    string `json:"name"`
	Vendor  string `json:"vendor"`
	Version string `json:"version"`
}

// VulnerabilitySummary counts vulnerabilities by severity ("Critical", "High", ...).
type VulnerabilitySummary struct {
	Total   int64            `json:"total"`
	Fixable int64            `json:"fixable"`
	Summary map[string]int64 `json:"summary"`
}

// ScanSummary is Harbor's NativeReportSummary: the state and result of the
// last scan of an artifact.
type ScanSummary struct {
	ReportID        string                `json:"report_id"`
	ScanStatus      string                `json:"scan_status"`
	Severity        string                `json:"severity"`
	Duration        int64                 `json:"duration"`
	Summary         *VulnerabilitySummary `json:"summary,omitempty"`
	StartTime       time.Time             `json:"start_time"`
	EndTime         time.Time             `json:"end_time"`
	CompletePercent int                   `json:"complete_percent"`
	Scanner         *Scanner              `json:"scanner,omitempty"`
}

// Artifact is the subset of a Harbor artifact shown to users.
type Artifact struct {
	ID             int64     `json:"id"`
	Type           string    `json:"type"`
	MediaType      string    `json:"media_type"`
	ProjectID      int64     `json:"project_id"`
	RepositoryName string    `json:"repository_name"`
	Digest         string    `json:"digest"`
	Size           int64     `json:"size"`
	PushTime       time.Time `json:"push_time"`
	PullTime       time.Time `json:"pull_time"`
	Tags           []Tag     `json:"tags"`
	Labels         []Label   `json:"labels"`
	// ScanOverview is keyed by report MIME type.
	ScanOverview map[string]ScanSummary `json:"scan_overview,omitempty"`
}

// Scan returns the artifact's scan summary, or nil when it was never scanned.
func (a *Artifact) Scan() *ScanSummary {
	for _, s := range a.ScanOverview {
		return &s
	}
	return nil
}

// Vulnerability is one entry of a scan report.
type Vulnerability struct {
	ID          string   `json:"id"`
	Package     string   `json:"package"`
	Version     string   `json:"version"`
	FixVersion  string   `json:"fix_version,omitempty"`
	Severity    string   `json:"severity"`
	Description string   `json:"description,omitempty"`
	Links       []string `json:"links,omitempty"`
}

// VulnerabilityReport is the scan report of one artifact.
type VulnerabilityReport struct {
	GeneratedAt     time.Time       `json:"generated_at"`
	Scanner         *Scanner        `json:"scanner,omitempty"`
	Severity        string          `json:"severity"`
	Vulnerabilities []Vulnerability `json:"vulnerabilities"`
}

// listPage fetches one page of path and returns it with Harbor's
// X-Total-Count (the page length when the header is missing).
func listPage[T any](ctx context.Context, c *Client, path string, query url.Values, po PageOptions, headers map[string]string) ([]T, int64, error) {
	pg, size := po.Page, po.PageSize
	if pg < 1 {
		pg = 1
	}
	if size < 1 {
		size = DefaultPageSize
	}
	size = min(size, maxPageSize)
	q := url.Values{}
	maps.Copy(q, query)
	q.Set(qPage, strconv.Itoa(pg))
	q.Set(qPageSize, strconv.Itoa(size))
	var out []T
	resp, err := c.do(ctx, http.MethodGet, path, q, nil, &out, headers)
	if err != nil {
		return nil, 0, err
	}
	total := int64(len(out))
	if n, perr := strconv.ParseInt(resp.Header.Get("X-Total-Count"), 10, 64); perr == nil {
		total = n
	}
	return out, total, nil
}

// fuzzyName is Harbor's query for names containing s.
func fuzzyName(s string) url.Values {
	if s == "" {
		return nil
	}
	return url.Values{"q": {"name=~" + s}}
}

// repoPath is the artifacts path of a repository. repo may include the
// project prefix; Harbor wants the rest, URL-encoded twice when it holds a
// slash (the second encoding happens when the request URL is built).
func repoPath(project, repo string) string {
	repo = strings.TrimPrefix(repo, project+"/")
	return "/projects/" + url.PathEscape(project) + "/repositories/" + url.PathEscape(repo) + "/artifacts"
}

// ListProjects returns one page of projects whose name contains name.
func (c *Client) ListProjects(ctx context.Context, name string, po PageOptions) ([]Project, int64, error) {
	return listPage[Project](ctx, c, "/projects", fuzzyName(name), po, nil)
}

// ListRepositories returns one page of a project's repositories whose name contains name.
func (c *Client) ListRepositories(ctx context.Context, project, name string, po PageOptions) ([]Repository, int64, error) {
	return listPage[Repository](ctx, c, "/projects/"+url.PathEscape(project)+"/repositories", fuzzyName(name), po, nil)
}

// ListArtifacts returns one page of a repository's artifacts, newest push
// first, with tags, labels and scan overviews.
func (c *Client) ListArtifacts(ctx context.Context, project, repo string, po PageOptions) ([]Artifact, int64, error) {
	q := url.Values{"with_tag": {vTrue}, "with_label": {vTrue}, "with_scan_overview": {vTrue}, qSort: {"-push_time"}}
	return listPage[Artifact](ctx, c, repoPath(project, repo), q, po, acceptVulnerabilities)
}

// GetVulnerabilityReport returns the scan report of the artifact with the
// given tag or digest, or nil when it has no report. Harbor answers 404 both
// for a missing artifact and for a project without a scanner; the APIError
// message tells them apart ("no scanner registration configured").
func (c *Client) GetVulnerabilityReport(ctx context.Context, project, repo, reference string) (*VulnerabilityReport, error) {
	var out map[string]VulnerabilityReport
	path := repoPath(project, repo) + "/" + url.PathEscape(reference) + "/additions/vulnerabilities"
	if _, err := c.do(ctx, http.MethodGet, path, nil, nil, &out, nil); err != nil {
		return nil, err
	}
	for _, r := range out {
		return &r, nil
	}
	return nil, nil
}
