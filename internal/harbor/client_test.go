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

package harbor_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/oreondev/harbor-resource-operator/internal/harbor"
	"github.com/oreondev/harbor-resource-operator/internal/harbor/fake"
)

func newClient(t *testing.T, srv *fake.Server, user, pass string) *harbor.Client {
	t.Helper()
	c, err := harbor.New(harbor.Options{URL: srv.URL(), Username: user, Password: pass})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return c
}

func TestNewRejectsBadURL(t *testing.T) {
	if _, err := harbor.New(harbor.Options{URL: "ftp://x"}); err == nil {
		t.Fatal("expected error for ftp scheme")
	}
	if _, err := harbor.New(harbor.Options{URL: "https://x", CABundle: []byte("not pem")}); err == nil {
		t.Fatal("expected error for invalid CA bundle")
	}
}

func TestSystemInfoAndAuth(t *testing.T) {
	srv := fake.New("admin", "pw")
	defer srv.Close()
	ctx := context.Background()

	c := newClient(t, srv, "admin", "pw")
	info, err := c.SystemInfo(ctx)
	if err != nil {
		t.Fatalf("systeminfo: %v", err)
	}
	if info.HarborVersion != srv.Version {
		t.Fatalf("version %q, want %q", info.HarborVersion, srv.Version)
	}
	u, err := c.CurrentUser(ctx)
	if err != nil || u.Username != "admin" {
		t.Fatalf("current user: %v %+v", err, u)
	}

	bad := newClient(t, srv, "admin", "wrong")
	_, err = bad.SystemInfo(ctx)
	if !harbor.IsUnauthorized(err) {
		t.Fatalf("expected unauthorized, got %v", err)
	}
	if got := err.Error(); got == "" || !contains(got, "401") {
		t.Fatalf("error should mention 401: %q", got)
	}
}

func TestProjectsLifecycle(t *testing.T) {
	srv := fake.New("admin", "pw")
	defer srv.Close()
	ctx := context.Background()
	c := newClient(t, srv, "admin", "pw")

	_, err := c.GetProjectByName(ctx, "missing")
	if !harbor.IsNotFound(err) {
		t.Fatalf("expected not found, got %v", err)
	}

	id, err := c.CreateProject(ctx, harbor.ProjectReq{ProjectName: "team-a", Public: new(false), StorageLimit: new(int64(1024))})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if id == 0 {
		t.Fatal("expected ID from Location header")
	}
	p, err := c.GetProjectByName(ctx, "team-a")
	if err != nil || p.ProjectID != id || p.Metadata.Public != "false" {
		t.Fatalf("get by name: %v %+v", err, p)
	}
	if err := c.UpdateProject(ctx, id, harbor.ProjectReq{Metadata: &harbor.ProjectMetadata{Public: "true", AutoScan: "true"}}); err != nil {
		t.Fatalf("update: %v", err)
	}
	p, _ = c.GetProject(ctx, id)
	if p.Metadata.Public != "true" || p.Metadata.AutoScan != "true" {
		t.Fatalf("update not applied: %+v", p.Metadata)
	}
	q, err := c.GetProjectQuota(ctx, id)
	if err != nil || q == nil || q.Hard["storage"] != 1024 {
		t.Fatalf("quota: %v %+v", err, q)
	}
	if err := c.UpdateQuota(ctx, q.ID, map[string]int64{"storage": 2048}); err != nil {
		t.Fatalf("update quota: %v", err)
	}
	if got, _ := srv.QuotaOfProject(id); got != 2048 {
		t.Fatalf("quota %d, want 2048", got)
	}
	d, err := c.ProjectDeletable(ctx, id)
	if err != nil || !d.Deletable {
		t.Fatalf("deletable: %v %+v", err, d)
	}
	if _, err := c.CreateProject(ctx, harbor.ProjectReq{ProjectName: "team-a"}); !harbor.IsConflict(err) {
		t.Fatalf("expected conflict, got %v", err)
	}
	if err := c.DeleteProject(ctx, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := c.DeleteProject(ctx, id); !harbor.IsNotFound(err) {
		t.Fatalf("expected not found on second delete, got %v", err)
	}
}

func TestRobotsLifecycleAndPagination(t *testing.T) {
	srv := fake.New("admin", "pw")
	defer srv.Close()
	ctx := context.Background()
	c := newClient(t, srv, "admin", "pw")

	created, err := c.CreateRobot(ctx, harbor.RobotCreate{Name: "ci", Level: "project", Duration: 30,
		Permissions: []harbor.RobotPermission{{Kind: "project", Namespace: "team-a", Access: []harbor.Access{{Resource: "repository", Action: "pull"}}}}})
	if err != nil {
		t.Fatalf("create robot: %v", err)
	}
	if created.Name != "robot$team-a+ci" || created.Secret == "" || created.ExpiresAt <= 0 {
		t.Fatalf("unexpected created robot: %+v", created)
	}
	sec, err := c.RefreshRobotSecret(ctx, created.ID)
	if err != nil || sec == "" || sec == created.Secret {
		t.Fatalf("refresh secret: %v %q", err, sec)
	}
	rb, err := c.GetRobot(ctx, created.ID)
	if err != nil || rb.Level != "project" {
		t.Fatalf("get robot: %v %+v", err, rb)
	}
	rb.Description = "updated"
	if err := c.UpdateRobot(ctx, rb.ID, *rb); err != nil {
		t.Fatalf("update robot: %v", err)
	}

	// 150 robots to force two pages.
	for i := range 150 {
		srv.AddRobot(harbor.Robot{Name: fmt.Sprintf("robot$bulk-%03d", i), Level: "system"}, "x")
	}
	all, err := c.ListRobots(ctx, "name=~bulk-")
	if err != nil || len(all) != 150 {
		t.Fatalf("list robots: %v len=%d", err, len(all))
	}
	if err := c.DeleteRobot(ctx, created.ID); err != nil {
		t.Fatalf("delete robot: %v", err)
	}
}

func TestRegistriesAndReplication(t *testing.T) {
	srv := fake.New("admin", "pw")
	defer srv.Close()
	ctx := context.Background()
	c := newClient(t, srv, "admin", "pw")

	reg := harbor.Registry{Name: "edge", Type: "harbor", URL: "https://edge.example.com",
		Credential: &harbor.RegistryCredential{Type: "basic", AccessKey: "u", AccessSecret: "s"}}
	if err := c.PingRegistry(ctx, reg); err != nil {
		t.Fatalf("ping: %v", err)
	}
	if err := c.PingRegistry(ctx, harbor.Registry{URL: "https://unreachable.example.com", Type: "harbor"}); err == nil || !harbor.IsClientError(err) {
		t.Fatalf("expected client error from ping, got %v", err)
	}
	id, err := c.CreateRegistry(ctx, reg)
	if err != nil || id == 0 {
		t.Fatalf("create registry: %v id=%d", err, id)
	}
	got, err := c.GetRegistry(ctx, id)
	if err != nil || got.Credential.AccessSecret != "*****" {
		t.Fatalf("get registry: %v %+v", err, got)
	}
	if err := c.UpdateRegistry(ctx, id, harbor.RegistryUpdate{Description: new("edge site")}); err != nil {
		t.Fatalf("update registry: %v", err)
	}
	list, err := c.ListRegistries(ctx, "edge")
	if err != nil || len(list) != 1 || list[0].Description != "edge site" {
		t.Fatalf("list registries: %v %+v", err, list)
	}

	pol := harbor.ReplicationPolicy{Name: "to-edge", DestRegistry: &harbor.Registry{ID: id},
		Trigger: &harbor.ReplicationTrigger{Type: "manual"}, Filters: []harbor.ReplicationFilter{{Type: "name", Value: "team-a/**"}}, Enabled: true, Override: true}
	pid, err := c.CreateReplicationPolicy(ctx, pol)
	if err != nil || pid == 0 {
		t.Fatalf("create policy: %v id=%d", err, pid)
	}
	gp, err := c.GetReplicationPolicy(ctx, pid)
	if err != nil || gp.DestRegistry == nil || gp.DestRegistry.ID != id {
		t.Fatalf("get policy: %v %+v", err, gp)
	}
	if err := c.StartReplication(ctx, pid); err != nil {
		t.Fatalf("start replication: %v", err)
	}
	ex, err := c.LatestReplicationExecution(ctx, pid)
	if err != nil || ex == nil || ex.Status != "Succeed" {
		t.Fatalf("latest execution: %v %+v", err, ex)
	}
	if err := c.DeleteReplicationPolicy(ctx, pid); err != nil {
		t.Fatalf("delete policy: %v", err)
	}
	if err := c.DeleteRegistry(ctx, id); err != nil {
		t.Fatalf("delete registry: %v", err)
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// Harbor returns the GC parameters as a JSON string in job_parameters and an
// empty 200 body when no schedule exists (both checked against Harbor 2.15).
func TestGCScheduleDecodesJobParameters(t *testing.T) {
	srv := fake.New("admin", "pw")
	defer srv.Close()
	ctx := context.Background()
	c := newClient(t, srv, "admin", "pw")

	got, err := c.GetGCSchedule(ctx)
	if err != nil {
		t.Fatalf("get empty schedule: %v", err)
	}
	if got.Schedule != nil || got.Parameters != nil {
		t.Fatalf("expected an empty schedule, got %+v", got)
	}
	if err := c.CreateGCSchedule(ctx, harbor.GCSchedule{
		Schedule:   &harbor.ScheduleObj{Type: harbor.ScheduleDaily},
		Parameters: map[string]any{"delete_untagged": true, "workers": 3},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err = c.GetGCSchedule(ctx)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Parameters["delete_untagged"] != true || got.Parameters["workers"] != float64(3) {
		t.Fatalf("parameters not decoded from job_parameters: %+v", got.Parameters)
	}
	if got.JobParameters != "" {
		t.Fatalf("job_parameters should be cleared after decoding, got %q", got.JobParameters)
	}
}
