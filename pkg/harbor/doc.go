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

// Package harbor is a thin client for the Harbor REST API v2.0, tested against
// Harbor 2.12 to 2.15. The operator uses it, and so does the Harbor MCP server
// (github.com/oreondev/harbor-mcp). Its API follows the operator's releases and
// may change in any minor version before 1.0; pin a release tag.
package harbor
