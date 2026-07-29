package ecs

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestFixturesMatchMockecs enforces the rule stated in testdata/README.md and
// CLAUDE.md: every fixture that exists in both directories must be byte-identical.
//
// Without this, adding a field to internal/ecs/testdata/ for a new metric and
// forgetting cmd/mockecs/fixtures/ leaves `make ci` green while `make demo`
// serves a payload missing the field a Grafana panel queries — an empty panel and
// no failing test.
//
// The set is the intersection on purpose: localzone-live-4.3.json lives only in
// testdata/ and must not be mirrored, so it is skipped rather than reported.
func TestFixturesMatchMockecs(t *testing.T) {
	const (
		testdata = "testdata"
		mockecs  = "../../cmd/mockecs/fixtures"
	)

	entries, err := os.ReadDir(testdata)
	if err != nil {
		t.Fatalf("reading %s: %v", testdata, err)
	}

	compared := 0
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		mirror := filepath.Join(mockecs, e.Name())
		want, err := os.ReadFile(mirror)
		if err != nil {
			// Not mirrored: legitimate for fixtures only one side needs.
			continue
		}
		got, err := os.ReadFile(filepath.Join(testdata, e.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		compared++
		if !bytes.Equal(got, want) {
			t.Errorf("%s differs between %s/ and %s/ — the two copies must stay byte-identical",
				e.Name(), testdata, mockecs)
		}
	}

	// A silent zero would make this test pass by comparing nothing at all, which
	// is exactly the failure it exists to prevent.
	if compared == 0 {
		t.Fatal("compared no fixtures: the mirror directory is missing or empty")
	}
	t.Logf("compared %d mirrored fixtures", compared)
}
