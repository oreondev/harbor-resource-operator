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
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/oreondev/harbor-resource-operator/pkg/harbor"
	"github.com/oreondev/harbor-resource-operator/pkg/harbor/fake"
)

func TestListProjectsPages(t *testing.T) {
	srv := fake.New("admin", "pw")
	defer srv.Close()
	ctx := context.Background()
	c := newClient(t, srv, "admin", "pw")
	for i := range 30 {
		srv.AddProject(fmt.Sprintf("team-%02d", i), false)
	}
	srv.AddProject("other", true)

	all, total, err := c.ListProjects(ctx, "", harbor.PageOptions{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != harbor.DefaultPageSize || total != 31 {
		t.Fatalf("default page: %d items, total %d; want %d of 31", len(all), total, harbor.DefaultPageSize)
	}
	second, total, err := c.ListProjects(ctx, "team-", harbor.PageOptions{Page: 2, PageSize: 20})
	if err != nil {
		t.Fatalf("list page 2: %v", err)
	}
	if len(second) != 10 || total != 30 || second[0].Name != "team-20" {
		t.Fatalf("page 2: %d items from %q, total %d; want 10 from team-20 of 30", len(second), second[0].Name, total)
	}
	big, _, err := c.ListProjects(ctx, "", harbor.PageOptions{PageSize: 1000})
	if err != nil || len(big) != 31 {
		t.Fatalf("page size above 100 should be capped, not rejected: %d items, %v", len(big), err)
	}
	if !slices.ContainsFunc(srv.Requests, func(r string) bool { return r == "GET /api/v2.0/projects" }) {
		t.Fatalf("no GET /projects in %v", srv.Requests)
	}
}

func TestRepositoriesArtifactsAndReports(t *testing.T) {
	srv := fake.New("admin", "pw")
	defer srv.Close()
	ctx := context.Background()
	c := newClient(t, srv, "admin", "pw")
	srv.AddProject("library", false)
	srv.AddRepository("library", "nginx")
	srv.AddRepository("library", "team/app")

	repos, total, err := c.ListRepositories(ctx, "library", "app", harbor.PageOptions{})
	if err != nil || total != 1 || repos[0].Name != "library/team/app" {
		t.Fatalf("repositories: %+v total %d, %v", repos, total, err)
	}
	if _, _, err := c.ListRepositories(ctx, "missing", "", harbor.PageOptions{}); !harbor.IsNotFound(err) {
		t.Fatalf("repositories of a missing project: want 404, got %v", err)
	}

	old := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	scan := map[string]harbor.ScanSummary{"application/vnd.security.vulnerability.report; version=1.1": {
		ScanStatus: "Success", Severity: "High",
		Summary: &harbor.VulnerabilitySummary{Total: 3, Fixable: 2, Summary: map[string]int64{"High": 1, "Medium": 2}},
	}}
	report := &harbor.VulnerabilityReport{Severity: "High", Vulnerabilities: []harbor.Vulnerability{
		{ID: "CVE-2026-0001", Package: "openssl", Version: "3.0.0", FixVersion: "3.0.1", Severity: "High"},
	}}
	srv.AddArtifact("library", "team/app", harbor.Artifact{Digest: "sha256:old", PushTime: old,
		Tags: []harbor.Tag{{Name: "1.0"}}}, nil)
	srv.AddArtifact("library", "library/team/app", harbor.Artifact{Digest: "sha256:new", PushTime: old.Add(time.Hour),
		Tags: []harbor.Tag{{Name: "1.1"}}, ScanOverview: scan}, report)

	// Both the name inside the project and the full name work.
	for _, repo := range []string{"team/app", "library/team/app"} {
		arts, total, err := c.ListArtifacts(ctx, "library", repo, harbor.PageOptions{})
		if err != nil || total != 2 {
			t.Fatalf("artifacts of %s: %d total, %v", repo, total, err)
		}
		if arts[0].Digest != "sha256:new" || arts[0].Tags[0].Name != "1.1" {
			t.Fatalf("artifacts of %s should be newest first: %+v", repo, arts)
		}
		if s := arts[0].Scan(); s == nil || s.Summary.Total != 3 || s.Severity != "High" {
			t.Fatalf("scan overview of the new artifact: %+v", s)
		}
		if arts[1].Scan() != nil {
			t.Fatalf("the old artifact was never scanned: %+v", arts[1].Scan())
		}
	}
	// A slash in the repository name is URL-encoded twice, as Harbor requires.
	if !slices.ContainsFunc(srv.Requests, func(r string) bool {
		return strings.HasPrefix(r, "GET /api/v2.0/projects/library/repositories/team%2Fapp/artifacts")
	}) {
		t.Fatalf("repository name not double-encoded: %v", srv.Requests)
	}

	for _, ref := range []string{"1.1", "sha256:new"} {
		got, err := c.GetVulnerabilityReport(ctx, "library", "team/app", ref)
		if err != nil || got == nil || len(got.Vulnerabilities) != 1 || got.Vulnerabilities[0].FixVersion != "3.0.1" {
			t.Fatalf("report for %s: %+v, %v", ref, got, err)
		}
	}
	if got, err := c.GetVulnerabilityReport(ctx, "library", "team/app", "1.0"); err != nil || got != nil {
		t.Fatalf("unscanned artifact should have no report: %+v, %v", got, err)
	}
	srv.Locked(func() { srv.NoScanner = true })
	_, err = c.GetVulnerabilityReport(ctx, "library", "team/app", "1.1")
	if !harbor.IsNotFound(err) || !strings.Contains(err.Error(), "no scanner registration") {
		t.Fatalf("project without a scanner: want 404 naming the scanner, got %v", err)
	}
	srv.Locked(func() { srv.NoScanner = false })
	if _, err := c.GetVulnerabilityReport(ctx, "library", "team/app", "9.9"); !harbor.IsNotFound(err) {
		t.Fatalf("missing tag: want 404, got %v", err)
	}
	// Harbor answers 200 with nothing for a missing repository or project.
	for _, pr := range [][2]string{{"library", "missing"}, {"missing", "app"}} {
		arts, total, err := c.ListArtifacts(ctx, pr[0], pr[1], harbor.PageOptions{})
		if err != nil || total != 0 || len(arts) != 0 {
			t.Fatalf("artifacts of missing %s/%s: %d items, total %d, %v", pr[0], pr[1], len(arts), total, err)
		}
	}
	if _, err := c.GetVulnerabilityReport(ctx, "library", "missing", "1.0"); !harbor.IsNotFound(err) {
		t.Fatalf("report from a missing repository: want 404, got %v", err)
	}
}
