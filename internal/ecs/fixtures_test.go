package ecs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fjacquet/obs_exporter/internal/ecsclient"
)

// fixture reads a testdata JSON file.
func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// mockClient returns a Mock pre-loaded with every fixture at its real API path.
func mockClient(t *testing.T) *ecsclient.Mock {
	t.Helper()
	return &ecsclient.Mock{
		ClusterName: "test-cluster",
		Responses: map[string]string{
			pathLocalZone:                             fixture(t, "localzone.json"),
			pathReplicationGroups:                     fixture(t, "replicationgroups.json"),
			pathLocalZoneNodes:                        fixture(t, "nodes.json"),
			pathVdcNodes:                              fixture(t, "vdc-nodes.json"),
			pathNamespaces:                            fixture(t, "namespaces.json"),
			pathNamespaces + "/namespace/s3/quota":    fixture(t, "quota-s3.json"),
			pathNamespaces + "/namespace/swift/quota": fixture(t, "quota-swift.json"),
			pathBillingBulk:                           fixture(t, "billing.json"),
		},
	}
}

// findSample returns the first sample matching name and all given label pairs.
func findSample(samples []Sample, name string, labels ...Label) (Sample, bool) {
	for _, s := range samples {
		if s.Name != name {
			continue
		}
		match := true
		for _, want := range labels {
			if s.LabelValue(want.Key) != want.Value {
				match = false
				break
			}
		}
		if match {
			return s, true
		}
	}
	return Sample{}, false
}

// mustSample fails the test unless the sample exists with the expected value.
func mustSample(t *testing.T, samples []Sample, name string, want float64, labels ...Label) {
	t.Helper()
	s, ok := findSample(samples, name, labels...)
	if !ok {
		t.Fatalf("sample %s%v not found", name, labels)
	}
	if s.Value != want {
		t.Errorf("%s%v = %v, want %v", name, labels, s.Value, want)
	}
}
