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
package harbor

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Endpoints for the milestone 2 kinds: project members, labels, immutable
// tag rules, webhook policies, tag retention and garbage collection.

// listAll follows Harbor's page/page_size pagination for a list endpoint.
func listAll[T any](ctx context.Context, c *Client, path string, query url.Values) ([]T, error) {
	var all []T
	for page := 1; ; page++ {
		q := url.Values{}
		maps.Copy(q, query)
		q.Set(qPage, strconv.Itoa(page))
		q.Set(qPageSize, strconv.Itoa(maxPageSize))
		var out []T
		if _, err := c.do(ctx, http.MethodGet, path, q, nil, &out, nil); err != nil {
			return nil, err
		}
		all = append(all, out...)
		if len(out) < maxPageSize {
			return all, nil
		}
	}
}

// create POSTs body to path and returns the ID from the Location header.
func (c *Client) create(ctx context.Context, path string, body any) (int64, error) {
	resp, err := c.do(ctx, http.MethodPost, path, nil, body, nil, nil)
	if err != nil {
		return 0, err
	}
	return idFromLocation(resp), nil
}

// ---- project members ----

// Harbor project role IDs.
const (
	RoleProjectAdmin = 1
	RoleDeveloper    = 2
	RoleGuest        = 3
	RoleMaintainer   = 4
	RoleLimitedGuest = 5
)

// Harbor user group types.
const (
	GroupTypeLDAP = 1
	GroupTypeHTTP = 2
	GroupTypeOIDC = 3
)

// ProjectMemberEntity is a member as Harbor lists it. EntityType is "u" for
// users and "g" for groups.
type ProjectMemberEntity struct {
	ID         int64  `json:"id"`
	ProjectID  int64  `json:"project_id"`
	EntityName string `json:"entity_name"`
	RoleName   string `json:"role_name"`
	RoleID     int64  `json:"role_id"`
	EntityID   int64  `json:"entity_id"`
	EntityType string `json:"entity_type"`
}

// MemberUser names a user to add.
type MemberUser struct {
	UserID   int64  `json:"user_id,omitempty"`
	Username string `json:"username,omitempty"`
}

// MemberGroup names a user group to add.
type MemberGroup struct {
	ID          int64  `json:"id,omitempty"`
	GroupName   string `json:"group_name,omitempty"`
	GroupType   int64  `json:"group_type,omitempty"`
	LDAPGroupDN string `json:"ldap_group_dn,omitempty"`
}

// ProjectMemberReq is the body of POST /projects/{id}/members.
type ProjectMemberReq struct {
	RoleID      int64        `json:"role_id"`
	MemberUser  *MemberUser  `json:"member_user,omitempty"`
	MemberGroup *MemberGroup `json:"member_group,omitempty"`
}

// ListProjectMembers lists members of a project whose name contains entityName.
func (c *Client) ListProjectMembers(ctx context.Context, projectID int64, entityName string) ([]ProjectMemberEntity, error) {
	q := url.Values{}
	if entityName != "" {
		q.Set("entityname", entityName)
	}
	return listAll[ProjectMemberEntity](ctx, c, fmt.Sprintf("/projects/%d/members", projectID), q)
}

// GetProjectMember calls GET /projects/{id}/members/{mid}.
func (c *Client) GetProjectMember(ctx context.Context, projectID, memberID int64) (*ProjectMemberEntity, error) {
	var out ProjectMemberEntity
	if _, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/projects/%d/members/%d", projectID, memberID), nil, nil, &out, nil); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateProjectMember adds a member and returns its member ID.
func (c *Client) CreateProjectMember(ctx context.Context, projectID int64, req ProjectMemberReq) (int64, error) {
	return c.create(ctx, fmt.Sprintf("/projects/%d/members", projectID), req)
}

// UpdateProjectMemberRole calls PUT /projects/{id}/members/{mid}.
func (c *Client) UpdateProjectMemberRole(ctx context.Context, projectID, memberID, roleID int64) error {
	body := struct {
		RoleID int64 `json:"role_id"`
	}{RoleID: roleID}
	_, err := c.do(ctx, http.MethodPut, fmt.Sprintf("/projects/%d/members/%d", projectID, memberID), nil, body, nil, nil)
	return err
}

// DeleteProjectMember calls DELETE /projects/{id}/members/{mid}.
func (c *Client) DeleteProjectMember(ctx context.Context, projectID, memberID int64) error {
	_, err := c.do(ctx, http.MethodDelete, fmt.Sprintf("/projects/%d/members/%d", projectID, memberID), nil, nil, nil, nil)
	return err
}

// ---- labels ----

// Label scopes as Harbor spells them.
const (
	LabelScopeGlobal  = "g"
	LabelScopeProject = "p"
)

// Label is a Harbor label.
type Label struct {
	ID          int64  `json:"id,omitempty"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Color       string `json:"color"`
	Scope       string `json:"scope"`
	ProjectID   int64  `json:"project_id"`
}

// ListLabels lists labels in a scope; projectID is used for scope "p".
// Harbor matches name fuzzily, so callers filter for exact names.
func (c *Client) ListLabels(ctx context.Context, scope string, projectID int64, name string) ([]Label, error) {
	q := url.Values{"scope": {scope}}
	if scope == LabelScopeProject {
		q.Set("project_id", strconv.FormatInt(projectID, 10))
	}
	if name != "" {
		q.Set("name", name)
	}
	return listAll[Label](ctx, c, "/labels", q)
}

// GetLabel calls GET /labels/{id}.
func (c *Client) GetLabel(ctx context.Context, id int64) (*Label, error) {
	var out Label
	if _, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/labels/%d", id), nil, nil, &out, nil); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateLabel calls POST /labels and returns the new ID.
func (c *Client) CreateLabel(ctx context.Context, l Label) (int64, error) {
	return c.create(ctx, "/labels", l)
}

// UpdateLabel calls PUT /labels/{id}.
func (c *Client) UpdateLabel(ctx context.Context, id int64, l Label) error {
	_, err := c.do(ctx, http.MethodPut, fmt.Sprintf("/labels/%d", id), nil, l, nil, nil)
	return err
}

// DeleteLabel calls DELETE /labels/{id}.
func (c *Client) DeleteLabel(ctx context.Context, id int64) error {
	_, err := c.do(ctx, http.MethodDelete, fmt.Sprintf("/labels/%d", id), nil, nil, nil, nil)
	return err
}

// ---- tag rules shared by immutability and retention ----

// Selector kinds and decorations as Harbor spells them.
const (
	SelectorKindDoublestar = "doublestar"
	DecorationMatches      = "matches"
	DecorationExcludes     = "excludes"
	DecorationRepoMatches  = "repoMatches"
	DecorationRepoExcludes = "repoExcludes"
)

// TagSelector selects tags or repositories by a doublestar pattern.
type TagSelector struct {
	Kind       string `json:"kind"`
	Decoration string `json:"decoration"`
	Pattern    string `json:"pattern"`
	Extras     string `json:"extras,omitempty"`
}

// TagRule is the rule shape used by immutable tag rules and retention rules.
type TagRule struct {
	ID             int64                    `json:"id,omitempty"`
	Priority       int64                    `json:"priority"`
	Disabled       bool                     `json:"disabled"`
	Action         string                   `json:"action"`
	Template       string                   `json:"template"`
	Params         map[string]any           `json:"params"`
	TagSelectors   []TagSelector            `json:"tag_selectors"`
	ScopeSelectors map[string][]TagSelector `json:"scope_selectors"`
}

// ---- immutable tag rules ----

// ListImmutableRules lists a project's immutable tag rules.
func (c *Client) ListImmutableRules(ctx context.Context, projectID int64) ([]TagRule, error) {
	return listAll[TagRule](ctx, c, fmt.Sprintf("/projects/%d/immutabletagrules", projectID), nil)
}

// CreateImmutableRule adds a rule and returns its ID.
func (c *Client) CreateImmutableRule(ctx context.Context, projectID int64, rule TagRule) (int64, error) {
	return c.create(ctx, fmt.Sprintf("/projects/%d/immutabletagrules", projectID), rule)
}

// UpdateImmutableRule calls PUT /projects/{id}/immutabletagrules/{rid}.
func (c *Client) UpdateImmutableRule(ctx context.Context, projectID, ruleID int64, rule TagRule) error {
	_, err := c.do(ctx, http.MethodPut, fmt.Sprintf("/projects/%d/immutabletagrules/%d", projectID, ruleID), nil, rule, nil, nil)
	return err
}

// DeleteImmutableRule calls DELETE /projects/{id}/immutabletagrules/{rid}.
func (c *Client) DeleteImmutableRule(ctx context.Context, projectID, ruleID int64) error {
	_, err := c.do(ctx, http.MethodDelete, fmt.Sprintf("/projects/%d/immutabletagrules/%d", projectID, ruleID), nil, nil, nil, nil)
	return err
}

// ---- webhooks ----

// WebhookTarget is where a webhook policy delivers events.
type WebhookTarget struct {
	Type           string `json:"type"`
	Address        string `json:"address"`
	AuthHeader     string `json:"auth_header,omitempty"`
	SkipCertVerify bool   `json:"skip_cert_verify"`
	PayloadFormat  string `json:"payload_format,omitempty"`
}

// WebhookPolicy is a project webhook.
type WebhookPolicy struct {
	ID          int64           `json:"id,omitempty"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	ProjectID   int64           `json:"project_id,omitempty"`
	Targets     []WebhookTarget `json:"targets"`
	EventTypes  []string        `json:"event_types"`
	Enabled     bool            `json:"enabled"`
}

// ListWebhookPolicies lists a project's webhook policies.
func (c *Client) ListWebhookPolicies(ctx context.Context, projectID int64) ([]WebhookPolicy, error) {
	return listAll[WebhookPolicy](ctx, c, fmt.Sprintf("/projects/%d/webhook/policies", projectID), nil)
}

// GetWebhookPolicy calls GET /projects/{id}/webhook/policies/{wid}.
func (c *Client) GetWebhookPolicy(ctx context.Context, projectID, id int64) (*WebhookPolicy, error) {
	var out WebhookPolicy
	if _, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/projects/%d/webhook/policies/%d", projectID, id), nil, nil, &out, nil); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateWebhookPolicy adds a webhook and returns its ID.
func (c *Client) CreateWebhookPolicy(ctx context.Context, projectID int64, p WebhookPolicy) (int64, error) {
	return c.create(ctx, fmt.Sprintf("/projects/%d/webhook/policies", projectID), p)
}

// UpdateWebhookPolicy calls PUT /projects/{id}/webhook/policies/{wid}.
func (c *Client) UpdateWebhookPolicy(ctx context.Context, projectID, id int64, p WebhookPolicy) error {
	_, err := c.do(ctx, http.MethodPut, fmt.Sprintf("/projects/%d/webhook/policies/%d", projectID, id), nil, p, nil, nil)
	return err
}

// DeleteWebhookPolicy calls DELETE /projects/{id}/webhook/policies/{wid}.
func (c *Client) DeleteWebhookPolicy(ctx context.Context, projectID, id int64) error {
	_, err := c.do(ctx, http.MethodDelete, fmt.Sprintf("/projects/%d/webhook/policies/%d", projectID, id), nil, nil, nil, nil)
	return err
}

// ---- tag retention ----

// RetentionTrigger runs a retention policy on a cron schedule; an empty cron means manual.
type RetentionTrigger struct {
	Kind       string            `json:"kind"`
	Settings   map[string]string `json:"settings"`
	References map[string]any    `json:"references"`
}

// RetentionScope binds a policy to a project.
type RetentionScope struct {
	Level string `json:"level"`
	Ref   int64  `json:"ref"`
}

// RetentionPolicy is a project's tag retention policy.
type RetentionPolicy struct {
	ID        int64             `json:"id,omitempty"`
	Algorithm string            `json:"algorithm"`
	Rules     []TagRule         `json:"rules"`
	Trigger   *RetentionTrigger `json:"trigger"`
	Scope     *RetentionScope   `json:"scope"`
}

// GetRetention calls GET /retentions/{id}.
func (c *Client) GetRetention(ctx context.Context, id int64) (*RetentionPolicy, error) {
	var out RetentionPolicy
	if _, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/retentions/%d", id), nil, nil, &out, nil); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateRetention calls POST /retentions and returns the new ID. Harbor
// records the ID in the project's retention_id metadata.
func (c *Client) CreateRetention(ctx context.Context, p RetentionPolicy) (int64, error) {
	return c.create(ctx, "/retentions", p)
}

// UpdateRetention calls PUT /retentions/{id}.
func (c *Client) UpdateRetention(ctx context.Context, id int64, p RetentionPolicy) error {
	_, err := c.do(ctx, http.MethodPut, fmt.Sprintf("/retentions/%d", id), nil, p, nil, nil)
	return err
}

// DeleteRetention calls DELETE /retentions/{id}.
func (c *Client) DeleteRetention(ctx context.Context, id int64) error {
	_, err := c.do(ctx, http.MethodDelete, fmt.Sprintf("/retentions/%d", id), nil, nil, nil, nil)
	return err
}

// DeleteProjectMetadata calls DELETE /projects/{id}/metadatas/{name}.
func (c *Client) DeleteProjectMetadata(ctx context.Context, projectID int64, name string) error {
	_, err := c.do(ctx, http.MethodDelete, fmt.Sprintf("/projects/%d/metadatas/%s", projectID, url.PathEscape(name)), nil, nil, nil, nil)
	return err
}

// ---- garbage collection ----

// Schedule types as Harbor spells them.
const (
	ScheduleNone   = "None"
	ScheduleHourly = "Hourly"
	ScheduleDaily  = "Daily"
	ScheduleWeekly = "Weekly"
	ScheduleCustom = "Custom"
)

// ScheduleObj is Harbor's schedule description.
type ScheduleObj struct {
	Type              string `json:"type,omitempty"`
	Cron              string `json:"cron,omitempty"`
	NextScheduledTime string `json:"next_scheduled_time,omitempty"`
}

// GCSchedule is the system garbage collection schedule.
type GCSchedule struct {
	ID         int64          `json:"id,omitempty"`
	Schedule   *ScheduleObj   `json:"schedule,omitempty"`
	Parameters map[string]any `json:"parameters,omitempty"`
	// JobParameters is how GET returns the parameters: a JSON object encoded
	// as a string. GetGCSchedule decodes it into Parameters.
	JobParameters string `json:"job_parameters,omitempty"`
}

// GCHistory is one garbage collection run.
type GCHistory struct {
	ID           int64        `json:"id"`
	JobStatus    string       `json:"job_status"`
	JobKind      string       `json:"job_kind"`
	Schedule     *ScheduleObj `json:"schedule"`
	CreationTime time.Time    `json:"creation_time"`
	UpdateTime   time.Time    `json:"update_time"`
}

// GetGCSchedule calls GET /system/gc/schedule. A Harbor without a schedule
// returns an empty or "None" schedule.
func (c *Client) GetGCSchedule(ctx context.Context) (*GCSchedule, error) {
	var out GCSchedule
	if _, err := c.do(ctx, http.MethodGet, "/system/gc/schedule", nil, nil, &out, nil); err != nil {
		return nil, err
	}
	if out.Parameters == nil && out.JobParameters != "" {
		if err := json.Unmarshal([]byte(out.JobParameters), &out.Parameters); err != nil {
			return nil, fmt.Errorf("harbor GET /system/gc/schedule: decode job_parameters: %w", err)
		}
	}
	out.JobParameters = ""
	return &out, nil
}

// CreateGCSchedule calls POST /system/gc/schedule.
func (c *Client) CreateGCSchedule(ctx context.Context, s GCSchedule) error {
	_, err := c.do(ctx, http.MethodPost, "/system/gc/schedule", nil, s, nil, nil)
	return err
}

// UpdateGCSchedule calls PUT /system/gc/schedule.
func (c *Client) UpdateGCSchedule(ctx context.Context, s GCSchedule) error {
	_, err := c.do(ctx, http.MethodPut, "/system/gc/schedule", nil, s, nil, nil)
	return err
}

// LatestGC returns the most recent garbage collection run, or nil.
func (c *Client) LatestGC(ctx context.Context) (*GCHistory, error) {
	q := url.Values{"sort": {"-creation_time"}, qPage: {"1"}, qPageSize: {"1"}}
	var out []GCHistory
	if _, err := c.do(ctx, http.MethodGet, "/system/gc", q, nil, &out, nil); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	return &out[0], nil
}
