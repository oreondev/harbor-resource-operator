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
	"errors"
	"fmt"
	"net/http"
)

// APIError is a non-2xx answer from Harbor.
type APIError struct {
	StatusCode int
	Method     string
	Path       string
	Message    string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("harbor %s %s: HTTP %d", e.Method, e.Path, e.StatusCode)
	}
	return fmt.Sprintf("harbor %s %s: HTTP %d: %s", e.Method, e.Path, e.StatusCode, e.Message)
}

// IsNotFound reports a 404.
func IsNotFound(err error) bool { return statusIs(err, http.StatusNotFound) }

// IsConflict reports a 409.
func IsConflict(err error) bool { return statusIs(err, http.StatusConflict) }

// IsUnauthorized reports a 401 or 403.
func IsUnauthorized(err error) bool {
	return statusIs(err, http.StatusUnauthorized) || statusIs(err, http.StatusForbidden)
}

// IsClientError reports a 4xx other than 404 and 409: the request itself is
// wrong and retrying without a spec change will not help.
func IsClientError(err error) bool {
	var e *APIError
	if !errors.As(err, &e) {
		return false
	}
	return e.StatusCode >= 400 && e.StatusCode < 500 &&
		e.StatusCode != http.StatusNotFound && e.StatusCode != http.StatusConflict
}

func statusIs(err error, code int) bool {
	var e *APIError
	return errors.As(err, &e) && e.StatusCode == code
}
