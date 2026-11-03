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
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

const (
	apiPrefix   = "/api/v2.0"
	maxPageSize = 100
	userAgent   = "harbor-resource-operator"
	qPage       = "page"
	qPageSize   = "page_size"
)

// Options configure a Client.
type Options struct {
	// URL of the Harbor instance without the API path.
	URL string
	// Username and Password for basic authentication.
	Username string
	Password string
	// CABundle holds PEM certificates to trust in addition to the system pool.
	CABundle []byte
	// InsecureSkipVerify disables TLS verification.
	InsecureSkipVerify bool
	// Transport, when set, is used as is and CABundle and InsecureSkipVerify
	// are ignored. Build it with NewTransport.
	Transport http.RoundTripper
	// Limiter caps the request rate. Nil means no limit.
	Limiter *rate.Limiter
	// Timeout per request. Zero means 30 seconds.
	Timeout time.Duration
	// Observe, when set, is called after every request with the HTTP method,
	// the response status code (0 when no response arrived) and the duration
	// excluding time spent waiting for the rate limiter.
	Observe func(method string, code int, d time.Duration)
}

// Client talks to one Harbor instance.
type Client struct {
	base     *url.URL
	http     *http.Client
	username string
	password string
	limiter  *rate.Limiter
	observe  func(method string, code int, d time.Duration)
}

// NewTransport builds an HTTP transport that trusts caBundle in addition to
// the system pool. Share one per Harbor instance so connections are reused.
func NewTransport(caBundle []byte, insecureSkipVerify bool) (*http.Transport, error) {
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: insecureSkipVerify} //nolint:gosec // user opt-in
	if len(caBundle) > 0 {
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(caBundle) {
			return nil, fmt.Errorf("caBundle contains no valid PEM certificates")
		}
		tlsCfg.RootCAs = pool
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = tlsCfg
	return transport, nil
}

// New builds a Client. It does not contact Harbor.
func New(o Options) (*Client, error) {
	base, err := url.Parse(strings.TrimRight(o.URL, "/"))
	if err != nil {
		return nil, fmt.Errorf("parse harbor url: %w", err)
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return nil, fmt.Errorf("harbor url must start with http:// or https://")
	}
	transport := o.Transport
	if transport == nil {
		t, terr := NewTransport(o.CABundle, o.InsecureSkipVerify)
		if terr != nil {
			return nil, terr
		}
		transport = t
	}
	timeout := o.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		base:     base,
		http:     &http.Client{Transport: transport, Timeout: timeout},
		username: o.Username,
		password: o.Password,
		limiter:  o.Limiter,
		observe:  o.Observe,
	}, nil
}

// Host returns the host[:port] of the Harbor URL, used for image pull secrets.
func (c *Client) Host() string { return c.base.Host }

// do performs one request. body is JSON-encoded when non-nil; out is decoded
// from a 2xx response when non-nil. The response is returned for callers that
// need headers (Location).
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any, headers map[string]string) (*http.Response, error) {
	if c.limiter != nil {
		if err := c.limiter.Wait(ctx); err != nil {
			return nil, err
		}
	}
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + apiPrefix + path
	if query != nil {
		u.RawQuery = query.Encode()
	}
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode request: %w", err)
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), reader)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	start := time.Now()
	resp, err := c.http.Do(req)
	if c.observe != nil {
		code := 0
		if resp != nil {
			code = resp.StatusCode
		}
		c.observe(method, code, time.Since(start))
	}
	if err != nil {
		return nil, fmt.Errorf("harbor %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("harbor %s %s: read body: %w", method, path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp, &APIError{StatusCode: resp.StatusCode, Method: method, Path: path, Message: errorMessage(data)}
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return resp, fmt.Errorf("harbor %s %s: decode response: %w", method, path, err)
		}
	}
	return resp, nil
}

// errorMessage extracts Harbor's {"errors":[{"code","message"}]} body.
func errorMessage(data []byte) string {
	var e struct {
		Errors []struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if json.Unmarshal(data, &e) == nil && len(e.Errors) > 0 {
		parts := make([]string, 0, len(e.Errors))
		for _, x := range e.Errors {
			parts = append(parts, strings.TrimSpace(x.Code+" "+x.Message))
		}
		return strings.Join(parts, "; ")
	}
	s := strings.TrimSpace(string(data))
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// idFromLocation parses the trailing integer of a Location header.
func idFromLocation(resp *http.Response) int64 {
	if resp == nil {
		return 0
	}
	loc := resp.Header.Get("Location")
	if loc == "" {
		return 0
	}
	parts := strings.Split(strings.TrimRight(loc, "/"), "/")
	id, err := strconv.ParseInt(parts[len(parts)-1], 10, 64)
	if err != nil {
		return 0
	}
	return id
}

// ---- system ----

// SystemInfo calls GET /systeminfo.
func (c *Client) SystemInfo(ctx context.Context) (*SystemInfo, error) {
	var out SystemInfo
	_, err := c.do(ctx, http.MethodGet, "/systeminfo", nil, nil, &out, nil)
	return &out, err
}

// CurrentUser calls GET /users/current.
func (c *Client) CurrentUser(ctx context.Context) (*User, error) {
	var out User
	_, err := c.do(ctx, http.MethodGet, "/users/current", nil, nil, &out, nil)
	return &out, err
}

// ---- projects ----

var byName = map[string]string{"X-Is-Resource-Name": "true"}

// GetProjectByName returns the project or a 404 APIError.
func (c *Client) GetProjectByName(ctx context.Context, name string) (*Project, error) {
	var out Project
	_, err := c.do(ctx, http.MethodGet, "/projects/"+url.PathEscape(name), nil, nil, &out, byName)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// GetProject returns the project by ID or a 404 APIError.
func (c *Client) GetProject(ctx context.Context, id int64) (*Project, error) {
	var out Project
	_, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/projects/%d", id), nil, nil, &out, nil)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateProject calls POST /projects and returns the new ID when Harbor sends
// a Location header, otherwise 0.
func (c *Client) CreateProject(ctx context.Context, req ProjectReq) (int64, error) {
	resp, err := c.do(ctx, http.MethodPost, "/projects", nil, req, nil, nil)
	if err != nil {
		return 0, err
	}
	return idFromLocation(resp), nil
}

// UpdateProject calls PUT /projects/{id}.
func (c *Client) UpdateProject(ctx context.Context, id int64, req ProjectReq) error {
	_, err := c.do(ctx, http.MethodPut, fmt.Sprintf("/projects/%d", id), nil, req, nil, nil)
	return err
}

// DeleteProject calls DELETE /projects/{id}.
func (c *Client) DeleteProject(ctx context.Context, id int64) error {
	_, err := c.do(ctx, http.MethodDelete, fmt.Sprintf("/projects/%d", id), nil, nil, nil, nil)
	return err
}

// ProjectDeletable calls GET /projects/{id}/_deletable.
func (c *Client) ProjectDeletable(ctx context.Context, id int64) (*Deletable, error) {
	var out Deletable
	_, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/projects/%d/_deletable", id), nil, nil, &out, nil)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// GetProjectQuota returns the quota record for a project, or nil when none exists.
func (c *Client) GetProjectQuota(ctx context.Context, projectID int64) (*Quota, error) {
	q := url.Values{"reference": {"project"}, "reference_id": {strconv.FormatInt(projectID, 10)}}
	var out []Quota
	if _, err := c.do(ctx, http.MethodGet, "/quotas", q, nil, &out, nil); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	return &out[0], nil
}

// UpdateQuota calls PUT /quotas/{id}.
func (c *Client) UpdateQuota(ctx context.Context, quotaID int64, hard map[string]int64) error {
	body := struct {
		Hard map[string]int64 `json:"hard"`
	}{Hard: hard}
	_, err := c.do(ctx, http.MethodPut, fmt.Sprintf("/quotas/%d", quotaID), nil, body, nil, nil)
	return err
}

// ---- robots ----

// ListRobots returns every robot matching the Harbor query string q
// (for example "name=~ci"), following pagination.
func (c *Client) ListRobots(ctx context.Context, q string) ([]Robot, error) {
	var all []Robot
	for page := 1; ; page++ {
		query := url.Values{qPage: {strconv.Itoa(page)}, qPageSize: {strconv.Itoa(maxPageSize)}}
		if q != "" {
			query.Set("q", q)
		}
		var out []Robot
		if _, err := c.do(ctx, http.MethodGet, "/robots", query, nil, &out, nil); err != nil {
			return nil, err
		}
		all = append(all, out...)
		if len(out) < maxPageSize {
			return all, nil
		}
	}
}

// GetRobot calls GET /robots/{id}.
func (c *Client) GetRobot(ctx context.Context, id int64) (*Robot, error) {
	var out Robot
	if _, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/robots/%d", id), nil, nil, &out, nil); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateRobot calls POST /robots. The returned secret is only available here.
func (c *Client) CreateRobot(ctx context.Context, req RobotCreate) (*RobotCreated, error) {
	var out RobotCreated
	if _, err := c.do(ctx, http.MethodPost, "/robots", nil, req, &out, nil); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateRobot calls PUT /robots/{id} with the full robot.
func (c *Client) UpdateRobot(ctx context.Context, id int64, robot Robot) error {
	_, err := c.do(ctx, http.MethodPut, fmt.Sprintf("/robots/%d", id), nil, robot, nil, nil)
	return err
}

// DeleteRobot calls DELETE /robots/{id}.
func (c *Client) DeleteRobot(ctx context.Context, id int64) error {
	_, err := c.do(ctx, http.MethodDelete, fmt.Sprintf("/robots/%d", id), nil, nil, nil, nil)
	return err
}

// RefreshRobotSecret calls PATCH /robots/{id} and returns the new secret.
func (c *Client) RefreshRobotSecret(ctx context.Context, id int64) (string, error) {
	var out RobotSec
	if _, err := c.do(ctx, http.MethodPatch, fmt.Sprintf("/robots/%d", id), nil, RobotSec{}, &out, nil); err != nil {
		return "", err
	}
	return out.Secret, nil
}

// ---- registries ----

// PingRegistry calls POST /registries/ping.
func (c *Client) PingRegistry(ctx context.Context, reg Registry) error {
	_, err := c.do(ctx, http.MethodPost, "/registries/ping", nil, reg, nil, nil)
	return err
}

// ListRegistries returns the registries whose name matches; callers compare exactly.
func (c *Client) ListRegistries(ctx context.Context, name string) ([]Registry, error) {
	var all []Registry
	for page := 1; ; page++ {
		query := url.Values{qPage: {strconv.Itoa(page)}, qPageSize: {strconv.Itoa(maxPageSize)}}
		if name != "" {
			query.Set("name", name)
		}
		var out []Registry
		if _, err := c.do(ctx, http.MethodGet, "/registries", query, nil, &out, nil); err != nil {
			return nil, err
		}
		all = append(all, out...)
		if len(out) < maxPageSize {
			return all, nil
		}
	}
}

// GetRegistry calls GET /registries/{id}.
func (c *Client) GetRegistry(ctx context.Context, id int64) (*Registry, error) {
	var out Registry
	if _, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/registries/%d", id), nil, nil, &out, nil); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateRegistry calls POST /registries and returns the new ID when known.
func (c *Client) CreateRegistry(ctx context.Context, reg Registry) (int64, error) {
	resp, err := c.do(ctx, http.MethodPost, "/registries", nil, reg, nil, nil)
	if err != nil {
		return 0, err
	}
	return idFromLocation(resp), nil
}

// UpdateRegistry calls PUT /registries/{id}.
func (c *Client) UpdateRegistry(ctx context.Context, id int64, upd RegistryUpdate) error {
	_, err := c.do(ctx, http.MethodPut, fmt.Sprintf("/registries/%d", id), nil, upd, nil, nil)
	return err
}

// DeleteRegistry calls DELETE /registries/{id}.
func (c *Client) DeleteRegistry(ctx context.Context, id int64) error {
	_, err := c.do(ctx, http.MethodDelete, fmt.Sprintf("/registries/%d", id), nil, nil, nil, nil)
	return err
}

// ---- replication ----

// ListReplicationPolicies returns policies matching name; callers compare exactly.
func (c *Client) ListReplicationPolicies(ctx context.Context, name string) ([]ReplicationPolicy, error) {
	var all []ReplicationPolicy
	for page := 1; ; page++ {
		query := url.Values{qPage: {strconv.Itoa(page)}, qPageSize: {strconv.Itoa(maxPageSize)}}
		if name != "" {
			query.Set("name", name)
		}
		var out []ReplicationPolicy
		if _, err := c.do(ctx, http.MethodGet, "/replication/policies", query, nil, &out, nil); err != nil {
			return nil, err
		}
		all = append(all, out...)
		if len(out) < maxPageSize {
			return all, nil
		}
	}
}

// GetReplicationPolicy calls GET /replication/policies/{id}.
func (c *Client) GetReplicationPolicy(ctx context.Context, id int64) (*ReplicationPolicy, error) {
	var out ReplicationPolicy
	if _, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/replication/policies/%d", id), nil, nil, &out, nil); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateReplicationPolicy calls POST /replication/policies and returns the new ID when known.
func (c *Client) CreateReplicationPolicy(ctx context.Context, p ReplicationPolicy) (int64, error) {
	resp, err := c.do(ctx, http.MethodPost, "/replication/policies", nil, p, nil, nil)
	if err != nil {
		return 0, err
	}
	return idFromLocation(resp), nil
}

// UpdateReplicationPolicy calls PUT /replication/policies/{id}.
func (c *Client) UpdateReplicationPolicy(ctx context.Context, id int64, p ReplicationPolicy) error {
	_, err := c.do(ctx, http.MethodPut, fmt.Sprintf("/replication/policies/%d", id), nil, p, nil, nil)
	return err
}

// DeleteReplicationPolicy calls DELETE /replication/policies/{id}.
func (c *Client) DeleteReplicationPolicy(ctx context.Context, id int64) error {
	_, err := c.do(ctx, http.MethodDelete, fmt.Sprintf("/replication/policies/%d", id), nil, nil, nil, nil)
	return err
}

// StartReplication calls POST /replication/executions.
func (c *Client) StartReplication(ctx context.Context, policyID int64) error {
	_, err := c.do(ctx, http.MethodPost, "/replication/executions", nil, StartReplicationExecution{PolicyID: policyID}, nil, nil)
	return err
}

// LatestReplicationExecution returns the most recent execution of a policy, or nil.
func (c *Client) LatestReplicationExecution(ctx context.Context, policyID int64) (*ReplicationExecution, error) {
	q := url.Values{
		"policy_id": {strconv.FormatInt(policyID, 10)},
		qPageSize:   {"1"},
		qPage:       {"1"},
		"sort":      {"-start_time"},
	}
	var out []ReplicationExecution
	if _, err := c.do(ctx, http.MethodGet, "/replication/executions", q, nil, &out, nil); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	return &out[0], nil
}
