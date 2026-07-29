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
// Both directories are enumerated, not just testdata/: a fixture added only on
// the mockecs side would otherwise never be reported. One-sided files must be
// named in allowedOneSided, so dropping a mirror is a test failure rather than a
// silent skip.
func TestFixturesMatchMockecs(t *testing.T) {
	const (
		testdata = "testdata"
		mockecs  = "../../cmd/mockecs/fixtures"
	)

	// localzone-live-4.3.json is an unedited real-cluster capture read by one
	// shape test; mirroring it into the demo server would serve a payload the
	// demo never wants. See testdata/README.md.
	allowedOneSided := map[string]string{
		"localzone-live-4.3.json": "real-cluster capture, deliberately not served by mockecs",
	}

	jsonNames := func(dir string) map[string]bool {
		t.Helper()
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("reading %s: %v", dir, err)
		}
		out := map[string]bool{}
		for _, e := range entries {
			if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
				out[e.Name()] = true
			}
		}
		return out
	}

	left, right := jsonNames(testdata), jsonNames(mockecs)

	compared := 0
	for name := range left {
		if !right[name] {
			if why, ok := allowedOneSided[name]; ok {
				t.Logf("%s: present only in %s/ — %s", name, testdata, why)
				continue
			}
			t.Errorf("%s exists in %s/ but not in %s/ — add the mirror or document it as one-sided",
				name, testdata, mockecs)
			continue
		}
		got, err := os.ReadFile(filepath.Join(testdata, name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		want, err := os.ReadFile(filepath.Join(mockecs, name))
		if err != nil {
			t.Fatalf("reading mirror of %s: %v", name, err)
		}
		compared++
		if !bytes.Equal(got, want) {
			t.Errorf("%s differs between %s/ and %s/ — the two copies must stay byte-identical",
				name, testdata, mockecs)
		}
	}
	for name := range right {
		if !left[name] && allowedOneSided[name] == "" {
			t.Errorf("%s exists in %s/ but not in %s/ — the demo would serve a payload the suite never tests",
				name, mockecs, testdata)
		}
	}

	// A silent zero would make this test pass by comparing nothing at all, which
	// is exactly the failure it exists to prevent.
	if compared == 0 {
		t.Fatal("compared no fixtures: the mirror directory is missing or empty")
	}
	t.Logf("compared %d mirrored fixtures", compared)
}
