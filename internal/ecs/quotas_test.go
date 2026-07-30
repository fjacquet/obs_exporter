package ecs

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestQuotasCollect(t *testing.T) {
	samples, err := Quotas{}.Collect(context.Background(), mockClient(t))
	if err != nil {
		t.Fatal(err)
	}

	// blockSize/notificationSize are GiB in the API, bytes on the wire.
	s3 := Label{"namespace", "s3"}
	mustSample(t, samples, "ecs_namespace_quota_hard_bytes", 10*gib, s3)
	mustSample(t, samples, "ecs_namespace_quota_soft_bytes", 8*gib, s3)

	// Unset quotas (-1) must be absent, not negative.
	swift := Label{"namespace", "swift"}
	if _, ok := findSample(samples, "ecs_namespace_quota_hard_bytes", swift); ok {
		t.Error("swift hard quota should be absent (unset)")
	}
	if _, ok := findSample(samples, "ecs_namespace_quota_soft_bytes", swift); ok {
		t.Error("swift soft quota should be absent (unset)")
	}
}

// Quotas are fetched concurrently; the emitted order must still follow the
// namespace inventory so --once --debug output is stable between cycles.
func TestQuotasOrderFollowsInventory(t *testing.T) {
	// The shipped swift fixture has both quotas unset, which would leave only s3
	// contributing samples — and a single contributing namespace cannot tell
	// inventory order from completion order. Give swift a quota so the assertion
	// can actually fail if the ordering regresses.
	c := mockClient(t)
	c.Responses[pathNamespaces+"/namespace/swift/quota"] =
		`{"namespace":"swift","blockSize":20,"notificationSize":16}`

	samples, err := Quotas{}.Collect(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	var namespaces []string
	for _, s := range samples {
		if strings.HasPrefix(s.Name, "ecs_namespace_quota_") {
			namespaces = append(namespaces, s.LabelValue("namespace"))
		}
	}
	// namespaces.json lists s3 before swift; each contributes a hard and a soft
	// quota sample, and the pair stays with its namespace.
	if want := []string{"s3", "s3", "swift", "swift"}; !slices.Equal(namespaces, want) {
		t.Errorf("quota namespaces = %v, want %v", namespaces, want)
	}
}

// A quota the cluster refuses must not blank the namespaces that answered: the
// collector degrades per namespace and still reports up.
func TestQuotasCollectDegradesPerNamespace(t *testing.T) {
	c := mockClient(t)
	c.Errs = map[string]error{pathNamespaces + "/namespace/s3/quota": errors.New("boom")}
	c.Responses[pathNamespaces+"/namespace/swift/quota"] =
		`{"namespace":"swift","blockSize":20,"notificationSize":16}`

	samples, err := Quotas{}.Collect(context.Background(), c)
	if err != nil {
		t.Fatalf("one namespace failing must not fail the collector: %v", err)
	}
	if _, ok := findSample(samples, "ecs_namespace_quota_hard_bytes", Label{"namespace", "s3"}); ok {
		t.Error("s3 quota should be absent: its fetch failed")
	}
	mustSample(t, samples, "ecs_namespace_quota_hard_bytes", 20*gib, Label{"namespace", "swift"})
}

// The namespace listing is the one failure that takes the collector down: with
// no inventory there is nothing to report, and a silently empty result would
// read as "this cluster sets no quotas" — the exact ambiguity this collector
// exists to remove.
func TestQuotasCollectFailsWithoutNamespaceList(t *testing.T) {
	c := mockClient(t)
	c.Errs = map[string]error{pathNamespaces: errors.New("boom")}
	if _, err := (Quotas{}).Collect(context.Background(), c); err == nil {
		t.Fatal("want an error when the namespace listing fails")
	}
}
