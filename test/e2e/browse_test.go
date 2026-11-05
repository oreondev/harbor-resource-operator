//go:build e2e

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

package e2e

import (
	"context"
	"net/url"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/oreondev/harbor-resource-operator/pkg/harbor"
)

const browseProject = "e2e-browse"

// These specs check the client's read-only listing (used by the Harbor MCP
// server) against a real Harbor: paging totals, a repository name with a
// slash (double URL encoding), tags and scan overviews, and what Harbor
// returns for the vulnerability report of an artifact that was never scanned
// (the e2e Harbor has no scanner).
var _ = Describe("Harbor client reads", Ordered, func() {
	ctx := context.Background()
	var hc *harbor.Client
	var projectID int64

	BeforeAll(func() {
		hc = harborAdmin()
		if p, err := hc.GetProjectByName(ctx, browseProject); err == nil {
			projectID = p.ProjectID
		} else {
			Expect(harbor.IsNotFound(err)).To(BeTrue(), "%v", err)
			projectID, err = hc.CreateProject(ctx, harbor.ProjectReq{ProjectName: browseProject})
			Expect(err).NotTo(HaveOccurred())
		}
		for _, tag := range []string{"1.0", "1.1"} {
			Expect(pushTestImage("admin", harborAdminPassword, browseProject+"/team/app", tag)).To(Succeed())
		}
	})

	AfterAll(func() {
		if os.Getenv("E2E_SKIP_CLEANUP") == "true" {
			return
		}
		Expect(deleteRepository(browseProject, url.PathEscape("team/app"))).To(Succeed())
		repos, total, err := hc.ListRepositories(ctx, browseProject, "", harbor.PageOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(total).To(BeZero(), "repositories left: %+v", repos)
		Expect(hc.DeleteProject(ctx, projectID)).To(Succeed())
	})

	It("lists projects with Harbor's total count", func() {
		projects, total, err := hc.ListProjects(ctx, browseProject, harbor.PageOptions{PageSize: 1})
		Expect(err).NotTo(HaveOccurred())
		Expect(total).To(BeNumerically(">=", 1))
		Expect(projects).To(HaveLen(1))
		Expect(projects[0].Name).To(ContainSubstring(browseProject))
		Expect(projects[0].CreationTime.IsZero()).To(BeFalse())
	})

	It("lists repositories with full names and counts", func() {
		repos, total, err := hc.ListRepositories(ctx, browseProject, "app", harbor.PageOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(total).To(BeEquivalentTo(1))
		Expect(repos[0].Name).To(Equal(browseProject + "/team/app"))
		Expect(repos[0].ArtifactCount).To(BeEquivalentTo(2))
	})

	It("lists artifacts newest first, with tags, by either repository name", func() {
		for _, repo := range []string{"team/app", browseProject + "/team/app"} {
			arts, total, err := hc.ListArtifacts(ctx, browseProject, repo, harbor.PageOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(total).To(BeEquivalentTo(2))
			Expect(arts[0].Tags).NotTo(BeEmpty())
			Expect(arts[0].Tags[0].Name).To(Equal("1.1"))
			Expect(arts[0].Digest).To(HavePrefix("sha256:"))
			Expect(arts[0].PushTime.IsZero()).To(BeFalse())
			Expect(arts[0].Size).To(BeNumerically(">", 0))
			GinkgoWriter.Printf("unscanned artifact scan overview: %+v, pull time %v\n", arts[0].Scan(), arts[0].PullTime)
		}
		// Harbor answers 200 with nothing, not 404, for a missing repository.
		arts, total, err := hc.ListArtifacts(ctx, browseProject, "missing", harbor.PageOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(total).To(BeZero())
		Expect(arts).To(BeEmpty())
	})

	It("returns no vulnerability report for an unscanned artifact", func() {
		// Without a scanner (the e2e Harbor has no Trivy) Harbor answers 404
		// naming the missing scanner; with one, 200 and no report.
		report, err := hc.GetVulnerabilityReport(ctx, browseProject, "team/app", "1.1")
		GinkgoWriter.Printf("vulnerability report of an unscanned artifact: %+v, %v\n", report, err)
		if err != nil {
			Expect(harbor.IsNotFound(err)).To(BeTrue(), "%v", err)
			Expect(err.Error()).To(ContainSubstring("no scanner registration"))
		}
		Expect(report).To(BeNil())
		_, err = hc.GetVulnerabilityReport(ctx, browseProject, "team/app", "9.9")
		GinkgoWriter.Printf("vulnerability report of a missing tag: %v\n", err)
		Expect(harbor.IsNotFound(err)).To(BeTrue(), "%v", err)
		_, err = hc.GetVulnerabilityReport(ctx, browseProject, "missing", "1.1")
		Expect(harbor.IsNotFound(err)).To(BeTrue(), "%v", err)
	})
})
