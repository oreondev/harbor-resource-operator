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

// Package fake is an in-memory Harbor API v2.0 server for tests. It covers
// exactly the endpoints the operator uses and mimics Harbor's status codes,
// Location headers and error bodies.
package fake

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/oreondev/harbor-resource-operator/pkg/harbor"
)

// Server is a fake Harbor.
type Server struct {
	mu sync.Mutex

	Username string
	Password string
	Version  string

	nextID     int64
	Projects   map[int64]*harbor.Project
	Quotas     map[int64]*harbor.Quota // keyed by quota ID; Ref holds project ID
	quotaRef   map[int64]int64         // quota ID -> project ID
	Robots     map[int64]*harbor.Robot
	Secrets    map[int64]string // robot ID -> secret
	Registries map[int64]*harbor.Registry
	Policies   map[int64]*harbor.ReplicationPolicy
	Executions map[int64]*harbor.ReplicationExecution

	// Milestone 2 state; see compliance.go.
	Users          map[string]int64 // username -> user ID
	Members        map[int64]*harbor.ProjectMemberEntity
	Labels         map[int64]*harbor.Label
	ImmutableRules map[int64]*ImmutableRule
	Webhooks       map[int64]*harbor.WebhookPolicy
	Retentions     map[int64]*harbor.RetentionPolicy
	GC             *harbor.GCSchedule
	// KeepRetentionPointer mimics Harbor 2.12 and 2.13, whose DELETE
	// /retentions/{id} leaves the project's retention_id in place.
	KeepRetentionPointer bool
	GCRuns               []harbor.GCHistory

	// Requests records "METHOD path" of every call, for assertions.
	Requests []string

	srv *httptest.Server
}

// New starts a fake Harbor with the given admin credentials.
func New(username, password string) *Server {
	s := &Server{
		Username:       username,
		Password:       password,
		Version:        "v2.14.6-fake",
		nextID:         1,
		Projects:       map[int64]*harbor.Project{},
		Quotas:         map[int64]*harbor.Quota{},
		quotaRef:       map[int64]int64{},
		Robots:         map[int64]*harbor.Robot{},
		Secrets:        map[int64]string{},
		Registries:     map[int64]*harbor.Registry{},
		Policies:       map[int64]*harbor.ReplicationPolicy{},
		Executions:     map[int64]*harbor.ReplicationExecution{},
		Users:          map[string]int64{},
		Members:        map[int64]*harbor.ProjectMemberEntity{},
		Labels:         map[int64]*harbor.Label{},
		ImmutableRules: map[int64]*ImmutableRule{},
		Webhooks:       map[int64]*harbor.WebhookPolicy{},
		Retentions:     map[int64]*harbor.RetentionPolicy{},
	}
	s.Users[username] = 1
	mux := http.NewServeMux()
	const p = "/api/v2.0"
	mux.HandleFunc("GET "+p+"/systeminfo", s.systemInfo)
	mux.HandleFunc("GET "+p+"/users/current", s.currentUser)
	mux.HandleFunc("GET "+p+"/projects/{id}", s.getProject)
	mux.HandleFunc("GET "+p+"/projects/{id}/_deletable", s.projectDeletable)
	mux.HandleFunc("POST "+p+"/projects", s.createProject)
	mux.HandleFunc("PUT "+p+"/projects/{id}", s.updateProject)
	mux.HandleFunc("DELETE "+p+"/projects/{id}", s.deleteProject)
	mux.HandleFunc("GET "+p+"/quotas", s.listQuotas)
	mux.HandleFunc("PUT "+p+"/quotas/{id}", s.updateQuota)
	mux.HandleFunc("GET "+p+"/robots", s.listRobots)
	mux.HandleFunc("POST "+p+"/robots", s.createRobot)
	mux.HandleFunc("GET "+p+"/robots/{id}", s.getRobot)
	mux.HandleFunc("PUT "+p+"/robots/{id}", s.updateRobot)
	mux.HandleFunc("PATCH "+p+"/robots/{id}", s.refreshRobot)
	mux.HandleFunc("DELETE "+p+"/robots/{id}", s.deleteRobot)
	mux.HandleFunc("POST "+p+"/registries/ping", s.pingRegistry)
	mux.HandleFunc("GET "+p+"/registries", s.listRegistries)
	mux.HandleFunc("POST "+p+"/registries", s.createRegistry)
	mux.HandleFunc("GET "+p+"/registries/{id}", s.getRegistry)
	mux.HandleFunc("PUT "+p+"/registries/{id}", s.updateRegistry)
	mux.HandleFunc("DELETE "+p+"/registries/{id}", s.deleteRegistry)
	mux.HandleFunc("GET "+p+"/replication/policies", s.listPolicies)
	mux.HandleFunc("POST "+p+"/replication/policies", s.createPolicy)
	mux.HandleFunc("GET "+p+"/replication/policies/{id}", s.getPolicy)
	mux.HandleFunc("PUT "+p+"/replication/policies/{id}", s.updatePolicy)
	mux.HandleFunc("DELETE "+p+"/replication/policies/{id}", s.deletePolicy)
	mux.HandleFunc("POST "+p+"/replication/executions", s.startExecution)
	mux.HandleFunc("GET "+p+"/replication/executions", s.listExecutions)
	s.registerCompliance(mux, p)
	s.srv = httptest.NewServer(s.auth(mux))
	return s
}

// URL of the fake Harbor.
func (s *Server) URL() string { return s.srv.URL }

// Close stops the server.
func (s *Server) Close() { s.srv.Close() }

// Reset clears all state but keeps credentials.
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID = 1
	s.Projects = map[int64]*harbor.Project{}
	s.Quotas = map[int64]*harbor.Quota{}
	s.quotaRef = map[int64]int64{}
	s.Robots = map[int64]*harbor.Robot{}
	s.Secrets = map[int64]string{}
	s.Registries = map[int64]*harbor.Registry{}
	s.Policies = map[int64]*harbor.ReplicationPolicy{}
	s.Executions = map[int64]*harbor.ReplicationExecution{}
	s.Users = map[string]int64{s.Username: 1}
	s.Members = map[int64]*harbor.ProjectMemberEntity{}
	s.Labels = map[int64]*harbor.Label{}
	s.ImmutableRules = map[int64]*ImmutableRule{}
	s.Webhooks = map[int64]*harbor.WebhookPolicy{}
	s.Retentions = map[int64]*harbor.RetentionPolicy{}
	s.GC = nil
	s.KeepRetentionPointer = false
	s.GCRuns = nil
	s.Requests = nil
}

// Locked runs fn with the state lock held, for test assertions and seeding.
func (s *Server) Locked(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn()
}

// AddProject seeds a project and its quota and returns the project ID.
func (s *Server) AddProject(name string, public bool) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addProject(harbor.ProjectReq{ProjectName: name, Public: &public})
}

// AddRegistry seeds a registry and returns its ID.
func (s *Server) AddRegistry(reg harbor.Registry) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.id()
	reg.ID = id
	if reg.Status == "" {
		reg.Status = "healthy"
	}
	s.Registries[id] = &reg
	return id
}

// AddRobot seeds a robot with a known secret and returns its ID.
func (s *Server) AddRobot(r harbor.Robot, secret string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.id()
	r.ID = id
	s.Robots[id] = &r
	s.Secrets[id] = secret
	return id
}

// ProjectByName returns a copy of the project or nil.
func (s *Server) ProjectByName(name string) *harbor.Project {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.Projects {
		if p.Name == name {
			c := *p
			return &c
		}
	}
	return nil
}

// RobotByName returns a copy of the robot with the full Harbor name, or nil.
func (s *Server) RobotByName(full string) *harbor.Robot {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.Robots {
		if r.Name == full {
			c := *r
			return &c
		}
	}
	return nil
}

// SecretOf returns the current secret of a robot.
func (s *Server) SecretOf(id int64) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Secrets[id]
}

// QuotaOfProject returns the storage hard limit for a project, or 0 and false.
func (s *Server) QuotaOfProject(projectID int64) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for qid, pid := range s.quotaRef {
		if pid == projectID {
			return s.Quotas[qid].Hard["storage"], true
		}
	}
	return 0, false
}

// ---- helpers ----

func (s *Server) id() int64 {
	id := s.nextID
	s.nextID++
	return id
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.Requests = append(s.Requests, r.Method+" "+r.URL.Path)
		s.mu.Unlock()
		u, p, ok := r.BasicAuth()
		if !ok || u != s.Username || p != s.Password {
			writeErr(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeErr(w http.ResponseWriter, code int, ecode, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"errors": []map[string]string{{"code": ecode, "message": msg}}})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func pathID(r *http.Request) int64 {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id
}

func decode(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

func randomSecret() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func page(r *http.Request, n int) (int, int) {
	pg, _ := strconv.Atoi(r.URL.Query().Get("page"))
	ps, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if pg < 1 {
		pg = 1
	}
	if ps < 1 || ps > 100 {
		ps = 10
	}
	start := (pg - 1) * ps
	end := start + ps
	if start > n {
		start = n
	}
	if end > n {
		end = n
	}
	return start, end
}

// ---- system ----

func (s *Server) systemInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, harbor.SystemInfo{HarborVersion: s.Version, ExternalURL: s.srv.URL, AuthMode: "db_auth"})
}

func (s *Server) currentUser(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, harbor.User{UserID: 1, Username: s.Username, Admin: true})
}

// ---- projects ----

func (s *Server) findProject(r *http.Request) *harbor.Project {
	raw := r.PathValue("id")
	byName := strings.EqualFold(r.Header.Get("X-Is-Resource-Name"), "true")
	if id, err := strconv.ParseInt(raw, 10, 64); err == nil && !byName {
		return s.Projects[id]
	}
	for _, p := range s.Projects {
		if p.Name == raw {
			return p
		}
	}
	return nil
}

func (s *Server) addProject(req harbor.ProjectReq) int64 {
	id := s.id()
	md := &harbor.ProjectMetadata{Public: "false"}
	if req.Metadata != nil {
		*md = *req.Metadata
	}
	if req.Public != nil {
		md.Public = harbor.BoolString(*req.Public)
	}
	if md.Public == "" {
		md.Public = "false"
	}
	p := &harbor.Project{ProjectID: id, Name: req.ProjectName, OwnerName: s.Username, Metadata: md}
	if req.RegistryID != nil {
		p.RegistryID = *req.RegistryID
	}
	if req.CVEAllowlist != nil {
		al := *req.CVEAllowlist
		al.ProjectID = id
		p.CVEAllowlist = &al
	}
	s.Projects[id] = p
	qid := s.id()
	limit := int64(-1)
	if req.StorageLimit != nil {
		limit = *req.StorageLimit
	}
	s.Quotas[qid] = &harbor.Quota{ID: qid, Hard: map[string]int64{"storage": limit}, Used: map[string]int64{"storage": 0}}
	s.quotaRef[qid] = id
	// Harbor makes the creator a project admin.
	mid := s.id()
	s.Members[mid] = &harbor.ProjectMemberEntity{ID: mid, ProjectID: id, EntityName: s.Username, EntityID: s.Users[s.Username],
		EntityType: "u", RoleID: harbor.RoleProjectAdmin, RoleName: "projectAdmin"}
	return id
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.findProject(r)
	if p == nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "project not found")
		return
	}
	writeJSON(w, p)
}

func (s *Server) projectDeletable(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.findProject(r)
	if p == nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "project not found")
		return
	}
	if p.RepoCount > 0 {
		writeJSON(w, harbor.Deletable{Deletable: false, Message: "contains repositories, can not be deleted"})
		return
	}
	writeJSON(w, harbor.Deletable{Deletable: true})
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var req harbor.ProjectReq
	if err := decode(r, &req); err != nil || req.ProjectName == "" {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid project")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.Projects {
		if p.Name == req.ProjectName {
			writeErr(w, http.StatusConflict, "CONFLICT", "project already exists")
			return
		}
	}
	id := s.addProject(req)
	w.Header().Set("Location", fmt.Sprintf("/api/v2.0/projects/%d", id))
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) updateProject(w http.ResponseWriter, r *http.Request) {
	var req harbor.ProjectReq
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid body")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.findProject(r)
	if p == nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "project not found")
		return
	}
	if p.Metadata == nil {
		p.Metadata = &harbor.ProjectMetadata{}
	}
	if req.Metadata != nil {
		m := req.Metadata
		set := func(dst *string, v string) {
			if v != "" {
				*dst = v
			}
		}
		set(&p.Metadata.Public, m.Public)
		set(&p.Metadata.AutoScan, m.AutoScan)
		set(&p.Metadata.AutoSBOMGeneration, m.AutoSBOMGeneration)
		set(&p.Metadata.PreventVul, m.PreventVul)
		set(&p.Metadata.Severity, m.Severity)
		set(&p.Metadata.EnableContentTrustCosign, m.EnableContentTrustCosign)
		set(&p.Metadata.ReuseSysCVEAllowlist, m.ReuseSysCVEAllowlist)
	}
	if req.Public != nil {
		p.Metadata.Public = harbor.BoolString(*req.Public)
	}
	if req.CVEAllowlist != nil {
		al := *req.CVEAllowlist
		al.ProjectID = p.ProjectID
		p.CVEAllowlist = &al
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.findProject(r)
	if p == nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "project not found")
		return
	}
	if p.RepoCount > 0 {
		writeErr(w, http.StatusPreconditionFailed, "PRECONDITION", "project contains repositories")
		return
	}
	delete(s.Projects, p.ProjectID)
	s.dropProjectChildren(p.ProjectID)
	for qid, pid := range s.quotaRef {
		if pid == p.ProjectID {
			delete(s.quotaRef, qid)
			delete(s.Quotas, qid)
		}
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) listQuotas(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	refID, _ := strconv.ParseInt(r.URL.Query().Get("reference_id"), 10, 64)
	out := []harbor.Quota{}
	for qid, pid := range s.quotaRef {
		if refID == 0 || pid == refID {
			out = append(out, *s.Quotas[qid])
		}
	}
	writeJSON(w, out)
}

func (s *Server) updateQuota(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Hard map[string]int64 `json:"hard"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid body")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id := pathID(r)
	q := s.Quotas[id]
	if q == nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "quota not found")
		return
	}
	maps.Copy(q.Hard, body.Hard)
	w.WriteHeader(http.StatusOK)
}

// ---- robots ----

func (s *Server) listRobots(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := r.URL.Query().Get("q")
	fuzzy, _ := strings.CutPrefix(q, "name=~")
	ids := make([]int64, 0, len(s.Robots))
	for id := range s.Robots {
		ids = append(ids, id)
	}
	sortInts(ids)
	out := []harbor.Robot{}
	for _, id := range ids {
		rb := s.Robots[id]
		if fuzzy == "" || strings.Contains(rb.Name, fuzzy) {
			out = append(out, *rb)
		}
	}
	a, b := page(r, len(out))
	writeJSON(w, out[a:b])
}

func (s *Server) createRobot(w http.ResponseWriter, r *http.Request) {
	var req harbor.RobotCreate
	if err := decode(r, &req); err != nil || req.Name == "" {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid robot")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	full := "robot$" + req.Name
	if req.Level == "project" {
		if len(req.Permissions) == 0 {
			writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "project robot needs permissions")
			return
		}
		full = "robot$" + req.Permissions[0].Namespace + "+" + req.Name
	}
	for _, rb := range s.Robots {
		if rb.Name == full {
			writeErr(w, http.StatusConflict, "CONFLICT", "robot account already exists")
			return
		}
	}
	id := s.id()
	exp := int64(-1)
	if req.Duration > 0 {
		exp = time.Now().Add(time.Duration(req.Duration) * 24 * time.Hour).Unix()
	}
	dur := req.Duration
	s.Robots[id] = &harbor.Robot{ID: id, Name: full, Description: req.Description, Level: req.Level, Duration: &dur,
		Editable: true, Disable: req.Disable, ExpiresAt: exp, Permissions: req.Permissions}
	sec := req.Secret
	if sec == "" {
		sec = randomSecret()
	}
	s.Secrets[id] = sec
	w.Header().Set("Location", fmt.Sprintf("/api/v2.0/robots/%d", id))
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, harbor.RobotCreated{ID: id, Name: full, Secret: sec, ExpiresAt: exp})
}

func (s *Server) getRobot(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := pathID(r)
	rb := s.Robots[id]
	if rb == nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "robot not found")
		return
	}
	writeJSON(w, rb)
}

func (s *Server) updateRobot(w http.ResponseWriter, r *http.Request) {
	var req harbor.Robot
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid body")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id := pathID(r)
	rb := s.Robots[id]
	if rb == nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "robot not found")
		return
	}
	rb.Description = req.Description
	rb.Disable = req.Disable
	rb.Permissions = req.Permissions
	if req.Duration != nil {
		rb.Duration = req.Duration
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) refreshRobot(w http.ResponseWriter, r *http.Request) {
	var req harbor.RobotSec
	_ = decode(r, &req)
	s.mu.Lock()
	defer s.mu.Unlock()
	id := pathID(r)
	if s.Robots[id] == nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "robot not found")
		return
	}
	sec := req.Secret
	if sec == "" {
		sec = randomSecret()
	}
	s.Secrets[id] = sec
	writeJSON(w, harbor.RobotSec{Secret: sec})
}

func (s *Server) deleteRobot(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := pathID(r)
	if s.Robots[id] == nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "robot not found")
		return
	}
	delete(s.Robots, id)
	delete(s.Secrets, id)
	w.WriteHeader(http.StatusOK)
}

// ---- registries ----

func (s *Server) pingRegistry(w http.ResponseWriter, r *http.Request) {
	var reg harbor.Registry
	if err := decode(r, &reg); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid body")
		return
	}
	if strings.Contains(reg.URL, "unreachable") {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "the registry is unhealthy")
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) listRegistries(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := r.URL.Query().Get("name")
	ids := make([]int64, 0, len(s.Registries))
	for id := range s.Registries {
		ids = append(ids, id)
	}
	sortInts(ids)
	out := []harbor.Registry{}
	for _, id := range ids {
		reg := s.Registries[id]
		if name == "" || strings.Contains(reg.Name, name) {
			c := *reg
			if c.Credential != nil {
				cc := *c.Credential
				cc.AccessSecret = "*****"
				c.Credential = &cc
			}
			out = append(out, c)
		}
	}
	a, b := page(r, len(out))
	writeJSON(w, out[a:b])
}

func (s *Server) createRegistry(w http.ResponseWriter, r *http.Request) {
	var reg harbor.Registry
	if err := decode(r, &reg); err != nil || reg.Name == "" || reg.URL == "" {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid registry")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.Registries {
		if x.Name == reg.Name {
			writeErr(w, http.StatusConflict, "CONFLICT", "registry name already exists")
			return
		}
	}
	id := s.id()
	reg.ID = id
	reg.Status = "healthy"
	if strings.Contains(reg.URL, "unreachable") {
		reg.Status = "unhealthy"
	}
	s.Registries[id] = &reg
	w.Header().Set("Location", fmt.Sprintf("/api/v2.0/registries/%d", id))
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) getRegistry(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := pathID(r)
	reg := s.Registries[id]
	if reg == nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "registry not found")
		return
	}
	c := *reg
	if c.Credential != nil {
		cc := *c.Credential
		cc.AccessSecret = "*****"
		c.Credential = &cc
	}
	writeJSON(w, c)
}

func (s *Server) updateRegistry(w http.ResponseWriter, r *http.Request) {
	var upd harbor.RegistryUpdate
	if err := decode(r, &upd); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid body")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id := pathID(r)
	reg := s.Registries[id]
	if reg == nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "registry not found")
		return
	}
	if upd.Name != nil {
		reg.Name = *upd.Name
	}
	if upd.Description != nil {
		reg.Description = *upd.Description
	}
	if upd.URL != nil {
		reg.URL = *upd.URL
	}
	if upd.Insecure != nil {
		reg.Insecure = *upd.Insecure
	}
	if upd.AccessKey != nil || upd.AccessSecret != nil || upd.CredentialType != nil {
		if reg.Credential == nil {
			reg.Credential = &harbor.RegistryCredential{}
		}
		if upd.CredentialType != nil {
			reg.Credential.Type = *upd.CredentialType
		}
		if upd.AccessKey != nil {
			reg.Credential.AccessKey = *upd.AccessKey
		}
		if upd.AccessSecret != nil {
			reg.Credential.AccessSecret = *upd.AccessSecret
		}
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) deleteRegistry(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := pathID(r)
	if s.Registries[id] == nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "registry not found")
		return
	}
	delete(s.Registries, id)
	w.WriteHeader(http.StatusOK)
}

// ---- replication ----

func (s *Server) fillRegistry(ref *harbor.Registry) *harbor.Registry {
	if ref == nil || ref.ID == 0 {
		return nil
	}
	if reg := s.Registries[ref.ID]; reg != nil {
		c := *reg
		c.Credential = nil
		return &c
	}
	return nil
}

func (s *Server) listPolicies(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := r.URL.Query().Get("name")
	ids := make([]int64, 0, len(s.Policies))
	for id := range s.Policies {
		ids = append(ids, id)
	}
	sortInts(ids)
	out := []harbor.ReplicationPolicy{}
	for _, id := range ids {
		p := s.Policies[id]
		if name == "" || strings.Contains(p.Name, name) {
			out = append(out, *p)
		}
	}
	a, b := page(r, len(out))
	writeJSON(w, out[a:b])
}

func (s *Server) createPolicy(w http.ResponseWriter, r *http.Request) {
	var p harbor.ReplicationPolicy
	if err := decode(r, &p); err != nil || p.Name == "" {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid policy")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.Policies {
		if x.Name == p.Name {
			writeErr(w, http.StatusConflict, "CONFLICT", "policy name already exists")
			return
		}
	}
	if (p.SrcRegistry == nil || p.SrcRegistry.ID == 0) && (p.DestRegistry == nil || p.DestRegistry.ID == 0) {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "source or destination registry required")
		return
	}
	if s.fillRegistry(p.SrcRegistry) == nil && s.fillRegistry(p.DestRegistry) == nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "registry not found")
		return
	}
	id := s.id()
	p.ID = id
	p.SrcRegistry = s.fillRegistry(p.SrcRegistry)
	p.DestRegistry = s.fillRegistry(p.DestRegistry)
	if p.Filters == nil {
		p.Filters = []harbor.ReplicationFilter{}
	}
	s.Policies[id] = &p
	w.Header().Set("Location", fmt.Sprintf("/api/v2.0/replication/policies/%d", id))
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) getPolicy(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := pathID(r)
	p := s.Policies[id]
	if p == nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "policy not found")
		return
	}
	writeJSON(w, p)
}

func (s *Server) updatePolicy(w http.ResponseWriter, r *http.Request) {
	var p harbor.ReplicationPolicy
	if err := decode(r, &p); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid body")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id := pathID(r)
	if s.Policies[id] == nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "policy not found")
		return
	}
	p.ID = id
	p.SrcRegistry = s.fillRegistry(p.SrcRegistry)
	p.DestRegistry = s.fillRegistry(p.DestRegistry)
	if p.Filters == nil {
		p.Filters = []harbor.ReplicationFilter{}
	}
	s.Policies[id] = &p
	w.WriteHeader(http.StatusOK)
}

func (s *Server) deletePolicy(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := pathID(r)
	if s.Policies[id] == nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "policy not found")
		return
	}
	delete(s.Policies, id)
	w.WriteHeader(http.StatusOK)
}

func (s *Server) startExecution(w http.ResponseWriter, r *http.Request) {
	var req harbor.StartReplicationExecution
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid body")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Policies[req.PolicyID] == nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "policy not found")
		return
	}
	id := s.id()
	now := time.Now()
	s.Executions[id] = &harbor.ReplicationExecution{ID: id, PolicyID: req.PolicyID, Status: "Succeed", Trigger: "manual",
		StartTime: now, EndTime: now.Add(time.Second)}
	w.Header().Set("Location", fmt.Sprintf("/api/v2.0/replication/executions/%d", id))
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) listExecutions(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pid, _ := strconv.ParseInt(r.URL.Query().Get("policy_id"), 10, 64)
	ids := make([]int64, 0, len(s.Executions))
	for id, e := range s.Executions {
		if pid == 0 || e.PolicyID == pid {
			ids = append(ids, id)
		}
	}
	sortInts(ids)
	out := []harbor.ReplicationExecution{}
	for _, id := range slices.Backward(ids) { // newest first, as sort=-start_time would
		out = append(out, *s.Executions[id])
	}
	a, b := page(r, len(out))
	writeJSON(w, out[a:b])
}

func sortInts(a []int64) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}
