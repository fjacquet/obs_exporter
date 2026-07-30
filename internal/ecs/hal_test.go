package ecs

import (
	"encoding/json"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
)

// halTestItem is a minimal element type: halList must not care what T is.
type halTestItem struct {
	Name string `json:"name"`
}

func TestHalListUnmarshal(t *testing.T) {
	tests := []struct {
		name         string
		payload      string
		wantNames    []string
		wantSeen     bool
		wantConflict bool
	}{
		{
			name:      "underscore key, as real clusters emit",
			payload:   `{"_instances":[{"name":"a"},{"name":"b"}]}`,
			wantNames: []string{"a", "b"},
			wantSeen:  true,
		},
		{
			name:      "documented key without underscore",
			payload:   `{"instances":[{"name":"a"},{"name":"b"}]}`,
			wantNames: []string{"a", "b"},
			wantSeen:  true,
		},
		{
			// An empty list is a legitimately empty cluster, not shape drift:
			// the key was seen, so no warning must be triggered downstream.
			name:      "empty underscore list still counts as a key sighting",
			payload:   `{"_instances":[]}`,
			wantNames: nil,
			wantSeen:  true,
		},
		{
			// An explicit null carries no list at all, so it must not pass for a
			// sighting and mask the shape warning.
			name:      "explicit null is not a key sighting",
			payload:   `{"_instances":null}`,
			wantNames: nil,
			wantSeen:  false,
		},
		{
			name:      "neither key present",
			payload:   `{"_links":{"self":{"href":"/x"}}}`,
			wantNames: nil,
			wantSeen:  false,
		},
		{
			// Same contents under both spellings: the preference discards
			// nothing, so this is not a conflict.
			name:      "both keys present with identical contents",
			payload:   `{"_instances":[{"name":"same"}],"instances":[{"name":"same"}]}`,
			wantNames: []string{"same"},
			wantSeen:  true,
		},
		{
			name:         "both keys present with different contents, underscore wins",
			payload:      `{"_instances":[{"name":"real"}],"instances":[{"name":"doc"}]}`,
			wantNames:    []string{"real"},
			wantSeen:     true,
			wantConflict: true,
		},
		{
			// Only the documented key can be used, so nothing is discarded.
			name:      "documented key alongside an explicit null underscore",
			payload:   `{"_instances":null,"instances":[{"name":"doc"}]}`,
			wantNames: []string{"doc"},
			wantSeen:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got halList[halTestItem]
			if err := json.Unmarshal([]byte(tc.payload), &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got.KeySeen != tc.wantSeen {
				t.Errorf("KeySeen = %v, want %v", got.KeySeen, tc.wantSeen)
			}
			if got.Conflict != tc.wantConflict {
				t.Errorf("Conflict = %v, want %v", got.Conflict, tc.wantConflict)
			}
			if len(got.Instances) != len(tc.wantNames) {
				t.Fatalf("got %d instances, want %d", len(got.Instances), len(tc.wantNames))
			}
			for i, want := range tc.wantNames {
				if got.Instances[i].Name != want {
					t.Errorf("instance %d name = %q, want %q", i, got.Instances[i].Name, want)
				}
			}
		})
	}
}

func TestHalListRejectsMalformedList(t *testing.T) {
	tests := []struct {
		name    string
		payload string
	}{
		{name: "underscore key is not an array", payload: `{"_instances":"not-a-list"}`},
		// The documented key is decoded even when it loses the preference, so a
		// malformed one must not slip through as a silent zero-instance decode.
		{name: "documented key is not an array", payload: `{"_instances":[],"instances":"not-a-list"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got halList[halTestItem]
			if err := json.Unmarshal([]byte(tc.payload), &got); err == nil {
				t.Fatal("want a decode error, got nil")
			}
		})
	}
}

func TestWarnHalShape(t *testing.T) {
	tests := []struct {
		name     string
		shape    halShape
		wantLogs int
	}{
		{name: "key seen stays silent", shape: halShape{KeySeen: true}, wantLogs: 0},
		{name: "key missing warns once", shape: halShape{}, wantLogs: 1},
		{
			name:     "conflicting keys warn once",
			shape:    halShape{KeySeen: true, Conflict: true},
			wantLogs: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hook := test.NewGlobal()
			defer hook.Reset()

			warnHalShape("test-cluster", "/dashboard/zones/localzone/nodes", tc.shape)

			if got := len(hook.Entries); got != tc.wantLogs {
				t.Fatalf("got %d log entries, want %d", got, tc.wantLogs)
			}
			if tc.wantLogs == 0 {
				return
			}
			entry := hook.LastEntry()
			if entry.Level != logrus.WarnLevel {
				t.Errorf("level = %v, want warning", entry.Level)
			}
			if entry.Data["cluster"] != "test-cluster" {
				t.Errorf("cluster field = %v, want the cluster name", entry.Data["cluster"])
			}
			if entry.Data["path"] != "/dashboard/zones/localzone/nodes" {
				t.Errorf("path field = %v, want the endpoint path", entry.Data["path"])
			}
		})
	}
}
