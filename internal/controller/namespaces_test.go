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
package controller

import (
	"slices"
	"testing"
)

func TestParseWatchNamespaces(t *testing.T) {
	cases := []struct {
		in      string
		want    []string
		wantErr bool
	}{
		{in: "", want: nil},
		{in: " , ", want: nil},
		{in: "team-a", want: []string{"team-a"}},
		{in: "team-b, team-a,team-b", want: []string{"team-a", "team-b"}},
		{in: "Team-A", wantErr: true},
		{in: "a/b", wantErr: true},
	}
	for _, c := range cases {
		got, err := ParseWatchNamespaces(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("ParseWatchNamespaces(%q) error = %v, wantErr %v", c.in, err, c.wantErr)
			continue
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("ParseWatchNamespaces(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
