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
		name      string
		payload   string
		wantNames []string
		wantSeen  bool
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
			name:      "neither key present",
			payload:   `{"_links":{"self":{"href":"/x"}}}`,
			wantNames: nil,
			wantSeen:  false,
		},
		{
			name:      "both keys present, underscore wins",
			payload:   `{"_instances":[{"name":"real"}],"instances":[{"name":"doc"}]}`,
			wantNames: []string{"real"},
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
	var got halList[halTestItem]
	err := json.Unmarshal([]byte(`{"_instances":"not-a-list"}`), &got)
	if err == nil {
		t.Fatal("want a decode error when _instances is not an array, got nil")
	}
}

func TestWarnUnknownHalShape(t *testing.T) {
	tests := []struct {
		name     string
		keySeen  bool
		wantLogs int
	}{
		{name: "key seen stays silent", keySeen: true, wantLogs: 0},
		{name: "key missing warns once", keySeen: false, wantLogs: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hook := test.NewGlobal()
			defer hook.Reset()

			warnUnknownHalShape("test-cluster", "/dashboard/zones/localzone/nodes", tc.keySeen)

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
