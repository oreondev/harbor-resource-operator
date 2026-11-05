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
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/oreondev/harbor-resource-operator/pkg/harbor"
)

// ImmutableRule is an immutable tag rule with the project it belongs to.
type ImmutableRule struct {
	ProjectID int64
	Rule      harbor.TagRule
}

var roleNames = map[int64]string{
	harbor.RoleProjectAdmin: "projectAdmin", harbor.RoleDeveloper: "developer", harbor.RoleGuest: "guest",
	harbor.RoleMaintainer: "maintainer", harbor.RoleLimitedGuest: "limitedGuest",
}

func (s *Server) registerCompliance(mux *http.ServeMux, p string) {
	mux.HandleFunc("GET "+p+"/projects/{id}/members", s.listMembers)
	mux.HandleFunc("POST "+p+"/projects/{id}/members", s.createMember)
	mux.HandleFunc("GET "+p+"/projects/{id}/members/{mid}", s.getMember)
	mux.HandleFunc("PUT "+p+"/projects/{id}/members/{mid}", s.updateMember)
	mux.HandleFunc("DELETE "+p+"/projects/{id}/members/{mid}", s.deleteMember)
	mux.HandleFunc("GET "+p+"/labels", s.listLabels)
	mux.HandleFunc("POST "+p+"/labels", s.createLabel)
	mux.HandleFunc("GET "+p+"/labels/{id}", s.getLabel)
	mux.HandleFunc("PUT "+p+"/labels/{id}", s.updateLabel)
	mux.HandleFunc("DELETE "+p+"/labels/{id}", s.deleteLabel)
	mux.HandleFunc("GET "+p+"/projects/{id}/immutabletagrules", s.listImmutable)
	mux.HandleFunc("POST "+p+"/projects/{id}/immutabletagrules", s.createImmutable)
	mux.HandleFunc("PUT "+p+"/projects/{id}/immutabletagrules/{rid}", s.updateImmutable)
	mux.HandleFunc("DELETE "+p+"/projects/{id}/immutabletagrules/{rid}", s.deleteImmutable)
	mux.HandleFunc("GET "+p+"/projects/{id}/webhook/policies", s.listWebhooks)
	mux.HandleFunc("POST "+p+"/projects/{id}/webhook/policies", s.createWebhook)
	mux.HandleFunc("GET "+p+"/projects/{id}/webhook/policies/{wid}", s.getWebhook)
	mux.HandleFunc("PUT "+p+"/projects/{id}/webhook/policies/{wid}", s.updateWebhook)
	mux.HandleFunc("DELETE "+p+"/projects/{id}/webhook/policies/{wid}", s.deleteWebhook)
	mux.HandleFunc("POST "+p+"/retentions", s.createRetention)
	mux.HandleFunc("GET "+p+"/retentions/{id}", s.getRetention)
	mux.HandleFunc("PUT "+p+"/retentions/{id}", s.updateRetention)
	mux.HandleFunc("DELETE "+p+"/retentions/{id}", s.deleteRetention)
	mux.HandleFunc("DELETE "+p+"/projects/{id}/metadatas/{name}", s.deleteProjectMetadata)
	mux.HandleFunc("GET "+p+"/system/gc/schedule", s.getGCSchedule)
	mux.HandleFunc("POST "+p+"/system/gc/schedule", s.createGCSchedule)
	mux.HandleFunc("PUT "+p+"/system/gc/schedule", s.updateGCSchedule)
	mux.HandleFunc("GET "+p+"/system/gc", s.listGC)
}

// AddUser seeds a Harbor user that can be added to projects.
func (s *Server) AddUser(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Users[name] = s.id()
}

// MembersOf returns copies of a project's members.
func (s *Server) MembersOf(projectID int64) []harbor.ProjectMemberEntity {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []harbor.ProjectMemberEntity
	for _, m := range s.Members {
		if m.ProjectID == projectID {
			out = append(out, *m)
		}
	}
	return out
}

// AddGCRun records a finished garbage collection run.
func (s *Server) AddGCRun(status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC().Truncate(time.Second)
	s.GCRuns = append(s.GCRuns, harbor.GCHistory{ID: s.id(), JobStatus: status, JobKind: "SCHEDULE", CreationTime: now, UpdateTime: now})
}

func (s *Server) dropProjectChildren(projectID int64) {
	for id, m := range s.Members {
		if m.ProjectID == projectID {
			delete(s.Members, id)
		}
	}
	for id, r := range s.ImmutableRules {
		if r.ProjectID == projectID {
			delete(s.ImmutableRules, id)
		}
	}
	for id, w := range s.Webhooks {
		if w.ProjectID == projectID {
			delete(s.Webhooks, id)
		}
	}
	for id, l := range s.Labels {
		if l.ProjectID == projectID {
			delete(s.Labels, id)
		}
	}
}

// projectByPath resolves {id} (numeric) or writes 404.
func (s *Server) projectByPath(w http.ResponseWriter, r *http.Request) *harbor.Project {
	p := s.findProject(r)
	if p == nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "project not found")
	}
	return p
}

func pathInt(r *http.Request, name string) int64 {
	v, _ := strconv.ParseInt(r.PathValue(name), 10, 64)
	return v
}

func created(w http.ResponseWriter, location string) {
	w.Header().Set("Location", location)
	w.WriteHeader(http.StatusCreated)
}

func sortedIDs[T any](m map[int64]T) []int64 {
	ids := make([]int64, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// ---- members ----

func (s *Server) listMembers(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.projectByPath(w, r)
	if p == nil {
		return
	}
	q := strings.ToLower(r.URL.Query().Get("entityname"))
	var out []harbor.ProjectMemberEntity
	for _, id := range sortedIDs(s.Members) {
		m := s.Members[id]
		if m.ProjectID == p.ProjectID && strings.Contains(strings.ToLower(m.EntityName), q) {
			out = append(out, *m)
		}
	}
	start, end := page(r, len(out))
	writeJSON(w, out[start:end])
}

func (s *Server) createMember(w http.ResponseWriter, r *http.Request) {
	var req harbor.ProjectMemberReq
	if err := decode(r, &req); err != nil || roleNames[req.RoleID] == "" || (req.MemberUser == nil) == (req.MemberGroup == nil) {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid member")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.projectByPath(w, r)
	if p == nil {
		return
	}
	m := &harbor.ProjectMemberEntity{ProjectID: p.ProjectID, RoleID: req.RoleID, RoleName: roleNames[req.RoleID]}
	if req.MemberUser != nil {
		uid, ok := s.Users[req.MemberUser.Username]
		if !ok {
			writeErr(w, http.StatusNotFound, "NOT_FOUND", "user "+req.MemberUser.Username+" not found")
			return
		}
		m.EntityType, m.EntityName, m.EntityID = "u", req.MemberUser.Username, uid
	} else {
		if req.MemberGroup.GroupName == "" || req.MemberGroup.GroupType == 0 {
			writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "group_name and group_type are required")
			return
		}
		m.EntityType, m.EntityName = "g", req.MemberGroup.GroupName
	}
	for _, x := range s.Members {
		if x.ProjectID == p.ProjectID && x.EntityType == m.EntityType && x.EntityName == m.EntityName {
			writeErr(w, http.StatusConflict, "CONFLICT", "the member already exists")
			return
		}
	}
	m.ID = s.id()
	s.Members[m.ID] = m
	created(w, fmt.Sprintf("/api/v2.0/projects/%d/members/%d", p.ProjectID, m.ID))
}

func (s *Server) memberByPath(w http.ResponseWriter, r *http.Request) *harbor.ProjectMemberEntity {
	p := s.projectByPath(w, r)
	if p == nil {
		return nil
	}
	m := s.Members[pathInt(r, "mid")]
	if m == nil || m.ProjectID != p.ProjectID {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "member not found")
		return nil
	}
	return m
}

func (s *Server) getMember(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m := s.memberByPath(w, r); m != nil {
		writeJSON(w, m)
	}
}

func (s *Server) updateMember(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RoleID int64 `json:"role_id"`
	}
	if err := decode(r, &req); err != nil || roleNames[req.RoleID] == "" {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid role")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if m := s.memberByPath(w, r); m != nil {
		m.RoleID, m.RoleName = req.RoleID, roleNames[req.RoleID]
		w.WriteHeader(http.StatusOK)
	}
}

func (s *Server) deleteMember(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m := s.memberByPath(w, r); m != nil {
		delete(s.Members, m.ID)
		w.WriteHeader(http.StatusOK)
	}
}

// ---- labels ----

func (s *Server) listLabels(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	scope := q.Get("scope")
	if scope != harbor.LabelScopeGlobal && scope != harbor.LabelScopeProject {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid scope")
		return
	}
	pid, _ := strconv.ParseInt(q.Get("project_id"), 10, 64)
	if scope == harbor.LabelScopeProject && pid == 0 {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "project_id is required for project scope")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []harbor.Label
	for _, id := range sortedIDs(s.Labels) {
		l := s.Labels[id]
		if l.Scope == scope && (scope == harbor.LabelScopeGlobal || l.ProjectID == pid) && strings.Contains(l.Name, q.Get("name")) {
			out = append(out, *l)
		}
	}
	start, end := page(r, len(out))
	writeJSON(w, out[start:end])
}

func (s *Server) createLabel(w http.ResponseWriter, r *http.Request) {
	var l harbor.Label
	if err := decode(r, &l); err != nil || l.Name == "" || (l.Scope != harbor.LabelScopeGlobal && l.Scope != harbor.LabelScopeProject) {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid label")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if l.Scope == harbor.LabelScopeProject && s.Projects[l.ProjectID] == nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "project not found")
		return
	}
	if l.Scope == harbor.LabelScopeGlobal {
		l.ProjectID = 0
	}
	for _, x := range s.Labels {
		if x.Name == l.Name && x.Scope == l.Scope && x.ProjectID == l.ProjectID {
			writeErr(w, http.StatusConflict, "CONFLICT", "label already exists")
			return
		}
	}
	l.ID = s.id()
	s.Labels[l.ID] = &l
	created(w, fmt.Sprintf("/api/v2.0/labels/%d", l.ID))
}

func (s *Server) getLabel(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.Labels[pathID(r)]
	if l == nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "label not found")
		return
	}
	writeJSON(w, l)
}

func (s *Server) updateLabel(w http.ResponseWriter, r *http.Request) {
	var in harbor.Label
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid label")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.Labels[pathID(r)]
	if l == nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "label not found")
		return
	}
	// Harbor keeps scope and project of an existing label.
	l.Name, l.Color, l.Description = in.Name, in.Color, in.Description
	w.WriteHeader(http.StatusOK)
}

func (s *Server) deleteLabel(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Labels[pathID(r)] == nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "label not found")
		return
	}
	delete(s.Labels, pathID(r))
	w.WriteHeader(http.StatusOK)
}

// ---- immutable tag rules ----

func (s *Server) listImmutable(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.projectByPath(w, r)
	if p == nil {
		return
	}
	var out []harbor.TagRule
	for _, id := range sortedIDs(s.ImmutableRules) {
		if ir := s.ImmutableRules[id]; ir.ProjectID == p.ProjectID {
			out = append(out, ir.Rule)
		}
	}
	start, end := page(r, len(out))
	writeJSON(w, out[start:end])
}

func validTagRule(rule harbor.TagRule) bool {
	return len(rule.TagSelectors) > 0 && len(rule.ScopeSelectors["repository"]) > 0 && rule.Template != ""
}

func (s *Server) createImmutable(w http.ResponseWriter, r *http.Request) {
	var rule harbor.TagRule
	if err := decode(r, &rule); err != nil || !validTagRule(rule) || rule.Action != "immutable" {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid immutable rule")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.projectByPath(w, r)
	if p == nil {
		return
	}
	for _, ir := range s.ImmutableRules {
		if ir.ProjectID == p.ProjectID && sameRule(ir.Rule, rule) {
			writeErr(w, http.StatusConflict, "CONFLICT", "the immutable tag rule already exists")
			return
		}
	}
	rule.ID = s.id()
	s.ImmutableRules[rule.ID] = &ImmutableRule{ProjectID: p.ProjectID, Rule: rule}
	created(w, fmt.Sprintf("/api/v2.0/projects/%d/immutabletagrules/%d", p.ProjectID, rule.ID))
}

func sameRule(a, b harbor.TagRule) bool {
	return fmt.Sprint(a.TagSelectors) == fmt.Sprint(b.TagSelectors) &&
		fmt.Sprint(a.ScopeSelectors["repository"]) == fmt.Sprint(b.ScopeSelectors["repository"])
}

func (s *Server) immutableByPath(w http.ResponseWriter, r *http.Request) *ImmutableRule {
	p := s.projectByPath(w, r)
	if p == nil {
		return nil
	}
	ir := s.ImmutableRules[pathInt(r, "rid")]
	if ir == nil || ir.ProjectID != p.ProjectID {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "immutable rule not found")
		return nil
	}
	return ir
}

func (s *Server) updateImmutable(w http.ResponseWriter, r *http.Request) {
	var rule harbor.TagRule
	if err := decode(r, &rule); err != nil || !validTagRule(rule) {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid immutable rule")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ir := s.immutableByPath(w, r); ir != nil {
		rule.ID = ir.Rule.ID
		ir.Rule = rule
		w.WriteHeader(http.StatusOK)
	}
}

func (s *Server) deleteImmutable(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ir := s.immutableByPath(w, r); ir != nil {
		delete(s.ImmutableRules, ir.Rule.ID)
		w.WriteHeader(http.StatusOK)
	}
}

// ---- webhooks ----

func (s *Server) listWebhooks(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.projectByPath(w, r)
	if p == nil {
		return
	}
	var out []harbor.WebhookPolicy
	for _, id := range sortedIDs(s.Webhooks) {
		if wh := s.Webhooks[id]; wh.ProjectID == p.ProjectID {
			out = append(out, *wh)
		}
	}
	start, end := page(r, len(out))
	writeJSON(w, out[start:end])
}

func validWebhook(p harbor.WebhookPolicy) bool {
	if p.Name == "" || len(p.Targets) == 0 || len(p.EventTypes) == 0 {
		return false
	}
	for _, t := range p.Targets {
		if (t.Type != "http" && t.Type != "slack") || t.Address == "" {
			return false
		}
	}
	return true
}

func (s *Server) createWebhook(w http.ResponseWriter, r *http.Request) {
	var in harbor.WebhookPolicy
	if err := decode(r, &in); err != nil || !validWebhook(in) {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid webhook policy")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.projectByPath(w, r)
	if p == nil {
		return
	}
	for _, x := range s.Webhooks {
		if x.ProjectID == p.ProjectID && x.Name == in.Name {
			writeErr(w, http.StatusConflict, "CONFLICT", "webhook policy name already exists")
			return
		}
	}
	in.ID, in.ProjectID = s.id(), p.ProjectID
	s.Webhooks[in.ID] = &in
	created(w, fmt.Sprintf("/api/v2.0/projects/%d/webhook/policies/%d", p.ProjectID, in.ID))
}

func (s *Server) webhookByPath(w http.ResponseWriter, r *http.Request) *harbor.WebhookPolicy {
	p := s.projectByPath(w, r)
	if p == nil {
		return nil
	}
	wh := s.Webhooks[pathInt(r, "wid")]
	if wh == nil || wh.ProjectID != p.ProjectID {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "webhook policy not found")
		return nil
	}
	return wh
}

func (s *Server) getWebhook(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if wh := s.webhookByPath(w, r); wh != nil {
		writeJSON(w, wh)
	}
}

func (s *Server) updateWebhook(w http.ResponseWriter, r *http.Request) {
	var in harbor.WebhookPolicy
	if err := decode(r, &in); err != nil || !validWebhook(in) {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid webhook policy")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if wh := s.webhookByPath(w, r); wh != nil {
		in.ID, in.ProjectID = wh.ID, wh.ProjectID
		*wh = in
		w.WriteHeader(http.StatusOK)
	}
}

func (s *Server) deleteWebhook(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if wh := s.webhookByPath(w, r); wh != nil {
		delete(s.Webhooks, wh.ID)
		w.WriteHeader(http.StatusOK)
	}
}

// ---- retention ----

func validRetention(p harbor.RetentionPolicy) bool {
	if p.Algorithm != "or" || p.Scope == nil || p.Scope.Level != levelProject || p.Trigger == nil || p.Trigger.Kind != "Schedule" {
		return false
	}
	for _, rule := range p.Rules {
		if !validTagRule(rule) || rule.Action != "retain" {
			return false
		}
	}
	return len(p.Rules) <= 15
}

func (s *Server) createRetention(w http.ResponseWriter, r *http.Request) {
	var in harbor.RetentionPolicy
	if err := decode(r, &in); err != nil || !validRetention(in) {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid retention policy")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	proj := s.Projects[in.Scope.Ref]
	if proj == nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "project not found")
		return
	}
	if proj.Metadata.RetentionID != "" {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "project already has a retention policy")
		return
	}
	in.ID = s.id()
	s.Retentions[in.ID] = &in
	proj.Metadata.RetentionID = strconv.FormatInt(in.ID, 10)
	created(w, fmt.Sprintf("/api/v2.0/retentions/%d", in.ID))
}

func (s *Server) getRetention(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.Retentions[pathID(r)]
	if p == nil {
		// Real Harbor (checked on 2.15) answers 500 for a deleted policy.
		writeErr(w, http.StatusInternalServerError, "UNKNOWN", "internal server error")
		return
	}
	writeJSON(w, p)
}

func (s *Server) updateRetention(w http.ResponseWriter, r *http.Request) {
	var in harbor.RetentionPolicy
	if err := decode(r, &in); err != nil || !validRetention(in) {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid retention policy")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.Retentions[pathID(r)]
	if p == nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "retention policy not found")
		return
	}
	in.ID = p.ID
	*p = in
	w.WriteHeader(http.StatusOK)
}

// deleteRetention removes the policy but, like the harshest Harbor
// behaviour, leaves the project's retention_id pointing at it.
func (s *Server) deleteRetention(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Retentions[pathID(r)] == nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "retention policy not found")
		return
	}
	s.dropRetention(pathID(r))
	w.WriteHeader(http.StatusOK)
}

// dropRetention deletes a policy and, as Harbor 2.14+ does, the project's pointer to it.
// Callers hold s.mu.
func (s *Server) dropRetention(id int64) {
	if p := s.Retentions[id]; p != nil && p.Scope != nil && !s.KeepRetentionPointer {
		if proj := s.Projects[p.Scope.Ref]; proj != nil && proj.Metadata.RetentionID == strconv.FormatInt(id, 10) {
			proj.Metadata.RetentionID = ""
		}
	}
	delete(s.Retentions, id)
}

func (s *Server) deleteProjectMetadata(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.projectByPath(w, r)
	if p == nil {
		return
	}
	if r.PathValue("name") != "retention_id" || p.Metadata.RetentionID == "" {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "metadata not found")
		return
	}
	p.Metadata.RetentionID = ""
	w.WriteHeader(http.StatusOK)
}

// ---- garbage collection ----

func validGC(g harbor.GCSchedule) bool {
	if g.Schedule == nil {
		return false
	}
	switch g.Schedule.Type {
	case harbor.ScheduleNone, harbor.ScheduleHourly, harbor.ScheduleDaily, harbor.ScheduleWeekly:
		return true
	case harbor.ScheduleCustom:
		return len(strings.Fields(g.Schedule.Cron)) == 6
	}
	return false
}

// storeGC normalizes the schedule the way Harbor does: presets get their
// cron, and a next run time is computed.
func (s *Server) storeGC(g harbor.GCSchedule) {
	crons := map[string]string{harbor.ScheduleHourly: "0 0 * * * *", harbor.ScheduleDaily: "0 0 0 * * *", harbor.ScheduleWeekly: "0 0 0 * * 0"}
	if c, ok := crons[g.Schedule.Type]; ok {
		g.Schedule.Cron = c
	}
	if g.Schedule.Type != harbor.ScheduleNone {
		g.Schedule.NextScheduledTime = time.Now().UTC().Add(time.Hour).Truncate(time.Hour).Format(time.RFC3339)
	} else {
		g.Schedule.Cron, g.Parameters = "", nil
	}
	if s.GC != nil && s.GC.ID != 0 {
		g.ID = s.GC.ID
	} else {
		g.ID = s.id()
	}
	s.GC = &g
}

func (s *Server) getGCSchedule(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.GC == nil {
		w.WriteHeader(http.StatusOK) // Harbor answers 200 with an empty body
		return
	}
	// Harbor returns the parameters as a JSON string in job_parameters.
	out := *s.GC
	if out.Parameters != nil {
		raw, _ := json.Marshal(out.Parameters)
		out.JobParameters = string(raw)
	}
	out.Parameters = nil
	writeJSON(w, out)
}

func (s *Server) createGCSchedule(w http.ResponseWriter, r *http.Request) {
	var in harbor.GCSchedule
	if err := decode(r, &in); err != nil || !validGC(in) {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid schedule")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.GC != nil {
		writeErr(w, http.StatusConflict, "CONFLICT", "a schedule already exists")
		return
	}
	s.storeGC(in)
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) updateGCSchedule(w http.ResponseWriter, r *http.Request) {
	var in harbor.GCSchedule
	if err := decode(r, &in); err != nil || !validGC(in) {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid schedule")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.GC == nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "no schedule to update")
		return
	}
	s.storeGC(in)
	w.WriteHeader(http.StatusOK)
}

func (s *Server) listGC(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := slices.Clone(s.GCRuns)
	slices.Reverse(out) // newest first, as with sort=-creation_time
	start, end := page(r, len(out))
	writeJSON(w, out[start:end])
}

// DropRetention deletes a retention policy the way DELETE /retentions/{id}
// does. Call it inside Locked.
func (s *Server) DropRetention(id int64) { s.dropRetention(id) }
