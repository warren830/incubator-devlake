/*
Licensed to the Apache Software Foundation (ASF) under one or more
contributor license agreements.  See the NOTICE file distributed with
this work for additional information regarding copyright ownership.
The ASF licenses this file to You under the Apache License, Version 2.0
(the "License"); you may not use this file except in compliance with
the License.  You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package tasks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseSessionTrailers(t *testing.T) {
	const id1 = "sess_3de1c67d-1234-4abc-9def-0123456789ab"
	const id2 = "9f8e7d6c-5b4a-4321-8765-fedcba987654"

	cases := []struct {
		name string
		msg  string
		want []sessionTrailer
	}{
		{
			name: "plain trailer at end",
			msg:  "fix: payment retry logic\n\nKiro-Session-Id: " + id1 + "\n",
			want: []sessionTrailer{{"kiro", id1}},
		},
		{
			name: "indented by local squash",
			msg:  "squash rollup\n\n    Kiro-Session-Id: " + id1 + "\n",
			want: []sessionTrailer{{"kiro", id1}},
		},
		{
			name: "buried mid-body by github squash",
			msg:  "feat: big change (#42)\n\n* commit one\n\nKiro-Session-Id: " + id1 + "\n\n* commit two\n\nmore text after\n",
			want: []sessionTrailer{{"kiro", id1}},
		},
		{
			name: "multiple sessions, mixed keys",
			msg:  "feat: pair work\n\nKiro-Session-Id: " + id1 + "\nDevdelta-Session-Id: " + id2 + "\n",
			want: []sessionTrailer{{"kiro", id1}, {"devdelta", id2}},
		},
		{
			name: "duplicate id kept once, first key wins",
			msg:  "chore\n\nKiro-Session-Id: " + id1 + "\nKiro-Session-Id: " + id1 + "\n",
			want: []sessionTrailer{{"kiro", id1}},
		},
		{
			name: "bare uuid without sess_ prefix",
			msg:  "fix\n\nDevdelta-Session-Id: " + id2 + "\n",
			want: []sessionTrailer{{"devdelta", id2}},
		},
		{
			name: "invalid value rejected",
			msg:  "fix\n\nKiro-Session-Id: not-a-uuid\n",
			want: nil,
		},
		{
			name: "key mentioned mid-sentence is not a trailer",
			msg:  "docs: explain that Kiro-Session-Id: values are appended by the hook\n",
			want: nil,
		},
		{
			name: "no trailer",
			msg:  "fix: something ordinary\n",
			want: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseSessionTrailers(tc.msg)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ParseSessionTrailers() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestParseSessionTrailersSharedFixture pins the reader to the golden cases
// the writer (DevDelta internal/client/commitlink, ExtractSessionIDs) is
// tested against. The fixture is duplicated in both repositories on purpose:
// if this test and its twin disagree, one parser has drifted and a commit is
// attributed differently in the lake than on the server that wrote it.
func TestParseSessionTrailersSharedFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "session_trailers.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fx struct {
		Cases []struct {
			Name    string `json:"name"`
			Message string `json:"message"`
			Want    []struct {
				Key string `json:"key"`
				ID  string `json:"id"`
			} `json:"want"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fx); err != nil {
		t.Fatal(err)
	}
	if len(fx.Cases) == 0 {
		t.Fatal("fixture has no cases")
	}
	for _, tc := range fx.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			var want []sessionTrailer
			for _, w := range tc.Want {
				want = append(want, sessionTrailer{TrailerKey: w.Key, SessionId: w.ID})
			}
			got := ParseSessionTrailers(tc.Message)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("ParseSessionTrailers() = %v, want %v", got, want)
			}
		})
	}
}
