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

// Package harbor is a thin client for the parts of the Harbor REST API v2.0
// the operator uses. It is written against the 2.14 OpenAPI specification and
// deliberately avoids the generated client to keep dependencies small.
package harbor

import "time"

// SystemInfo is the subset of GET /systeminfo the operator reads.
type SystemInfo struct {
	HarborVersion string `json:"harbor_version"`
	ExternalURL   string `json:"external_url"`
	RegistryURL   string `json:"registry_url"`
	AuthMode      string `json:"auth_mode"`
}

// User is the subset of GET /users/current the operator reads.
type User struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	Admin    bool   `json:"sysadmin_flag"`
}

// ProjectMetadata mirrors Harbor's string-typed project metadata.
type ProjectMetadata struct {
	Public                   string `json:"public,omitempty"`
	EnableContentTrust       string `json:"enable_content_trust,omitempty"`
	EnableContentTrustCosign string `json:"enable_content_trust_cosign,omitempty"`
	PreventVul               string `json:"prevent_vul,omitempty"`
	Severity                 string `json:"severity,omitempty"`
	AutoScan                 string `json:"auto_scan,omitempty"`
	AutoSBOMGeneration       string `json:"auto_sbom_generation,omitempty"`
	ReuseSysCVEAllowlist     string `json:"reuse_sys_cve_allowlist,omitempty"`
	RetentionID              string `json:"retention_id,omitempty"`
	ProxySpeedKB             string `json:"proxy_speed_kb,omitempty"`
}

// CVEAllowlistItem is one allowed CVE.
type CVEAllowlistItem struct {
	CVEID string `json:"cve_id"`
}

// CVEAllowlist is a project's CVE allowlist.
type CVEAllowlist struct {
	ID        int64              `json:"id,omitempty"`
	ProjectID int64              `json:"project_id,omitempty"`
	ExpiresAt *int64             `json:"expires_at,omitempty"`
	Items     []CVEAllowlistItem `json:"items"`
}

// ProjectReq is the body of POST and PUT /projects.
type ProjectReq struct {
	ProjectName  string           `json:"project_name,omitempty"`
	Public       *bool            `json:"public,omitempty"`
	Metadata     *ProjectMetadata `json:"metadata,omitempty"`
	CVEAllowlist *CVEAllowlist    `json:"cve_allowlist,omitempty"`
	StorageLimit *int64           `json:"storage_limit,omitempty"`
	RegistryID   *int64           `json:"registry_id,omitempty"`
}

// Project is the subset of a Harbor project the operator reads.
type Project struct {
	ProjectID    int64            `json:"project_id"`
	Name         string           `json:"name"`
	RegistryID   int64            `json:"registry_id"`
	OwnerName    string           `json:"owner_name"`
	RepoCount    int64            `json:"repo_count"`
	CreationTime time.Time        `json:"creation_time"`
	UpdateTime   time.Time        `json:"update_time"`
	Metadata     *ProjectMetadata `json:"metadata"`
	CVEAllowlist *CVEAllowlist    `json:"cve_allowlist"`
}

// Deletable is the response of GET /projects/{id}/_deletable.
type Deletable struct {
	Deletable bool   `json:"deletable"`
	Message   string `json:"message"`
}

// Quota is a Harbor quota record.
type Quota struct {
	ID   int64            `json:"id"`
	Hard map[string]int64 `json:"hard"`
	Used map[string]int64 `json:"used"`
}

// Access is one robot permission entry.
type Access struct {
	Resource string `json:"resource"`
	Action   string `json:"action"`
	Effect   string `json:"effect,omitempty"`
}

// RobotPermission groups access entries for one namespace.
type RobotPermission struct {
	Kind      string   `json:"kind"`
	Namespace string   `json:"namespace"`
	Access    []Access `json:"access"`
}

// RobotCreate is the body of POST /robots.
type RobotCreate struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Secret      string            `json:"secret,omitempty"`
	Level       string            `json:"level"`
	Disable     bool              `json:"disable"`
	Duration    int64             `json:"duration"`
	Permissions []RobotPermission `json:"permissions"`
}

// RobotCreated is the response of POST /robots. Secret is only ever returned here.
type RobotCreated struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Secret    string `json:"secret"`
	ExpiresAt int64  `json:"expires_at"`
}

// Robot is a robot account as returned by GET /robots.
type Robot struct {
	ID          int64             `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Level       string            `json:"level"`
	Duration    *int64            `json:"duration"`
	Editable    bool              `json:"editable"`
	Disable     bool              `json:"disable"`
	ExpiresAt   int64             `json:"expires_at"`
	Permissions []RobotPermission `json:"permissions"`
}

// RobotSec is the body and response of PATCH /robots/{id}.
type RobotSec struct {
	Secret string `json:"secret"`
}

// RegistryCredential is a remote registry credential.
type RegistryCredential struct {
	Type         string `json:"type,omitempty"`
	AccessKey    string `json:"access_key,omitempty"`
	AccessSecret string `json:"access_secret,omitempty"`
}

// Registry is a replication endpoint.
type Registry struct {
	ID          int64               `json:"id,omitempty"`
	URL         string              `json:"url,omitempty"`
	Name        string              `json:"name,omitempty"`
	Credential  *RegistryCredential `json:"credential,omitempty"`
	Type        string              `json:"type,omitempty"`
	Insecure    bool                `json:"insecure"`
	Description string              `json:"description,omitempty"`
	Status      string              `json:"status,omitempty"`
}

// RegistryUpdate is the body of PUT /registries/{id}.
type RegistryUpdate struct {
	Name           *string `json:"name,omitempty"`
	Description    *string `json:"description,omitempty"`
	URL            *string `json:"url,omitempty"`
	CredentialType *string `json:"credential_type,omitempty"`
	AccessKey      *string `json:"access_key,omitempty"`
	AccessSecret   *string `json:"access_secret,omitempty"`
	Insecure       *bool   `json:"insecure,omitempty"`
}

// ReplicationFilter narrows a replication policy. Value is a string except
// for label filters, where Harbor expects a list of label names.
type ReplicationFilter struct {
	Type       string `json:"type"`
	Value      any    `json:"value"`
	Decoration string `json:"decoration,omitempty"`
}

// ReplicationTriggerSettings holds the cron for scheduled triggers.
type ReplicationTriggerSettings struct {
	Cron string `json:"cron,omitempty"`
}

// ReplicationTrigger says when a policy runs.
type ReplicationTrigger struct {
	Type            string                      `json:"type"`
	TriggerSettings *ReplicationTriggerSettings `json:"trigger_settings,omitempty"`
}

// ReplicationPolicy is a Harbor replication policy.
type ReplicationPolicy struct {
	ID                        int64               `json:"id,omitempty"`
	Name                      string              `json:"name"`
	Description               string              `json:"description,omitempty"`
	SrcRegistry               *Registry           `json:"src_registry,omitempty"`
	DestRegistry              *Registry           `json:"dest_registry,omitempty"`
	DestNamespace             string              `json:"dest_namespace,omitempty"`
	DestNamespaceReplaceCount *int32              `json:"dest_namespace_replace_count,omitempty"`
	Trigger                   *ReplicationTrigger `json:"trigger,omitempty"`
	Filters                   []ReplicationFilter `json:"filters"`
	ReplicateDeletion         bool                `json:"replicate_deletion"`
	Override                  bool                `json:"override"`
	Enabled                   bool                `json:"enabled"`
	Speed                     *int32              `json:"speed,omitempty"`
	CopyByChunk               *bool               `json:"copy_by_chunk,omitempty"`
	SingleActiveReplication   *bool               `json:"single_active_replication,omitempty"`
}

// ReplicationExecution is one run of a policy.
type ReplicationExecution struct {
	ID         int64     `json:"id"`
	PolicyID   int64     `json:"policy_id"`
	Status     string    `json:"status"`
	Trigger    string    `json:"trigger"`
	StartTime  time.Time `json:"start_time"`
	EndTime    time.Time `json:"end_time"`
	StatusText string    `json:"status_text"`
	Total      int64     `json:"total"`
	Failed     int64     `json:"failed"`
	Succeed    int64     `json:"succeed"`
}

// StartReplicationExecution is the body of POST /replication/executions.
type StartReplicationExecution struct {
	PolicyID int64 `json:"policy_id"`
}

// BoolString converts a Go bool to Harbor's "true"/"false" metadata strings.
func BoolString(b bool) string {
	if b {
		return vTrue
	}
	return "false"
}
