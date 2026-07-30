package ecs

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/fjacquet/obs_exporter/internal/config"
)

func TestMeteringCollect(t *testing.T) {
	samples, err := Metering{Quotas: true}.Collect(context.Background(), mockClient(t))
	if err != nil {
		t.Fatal(err)
	}

	s3 := Label{"namespace", "s3"}
	// Quota blockSize/notificationSize are GiB → bytes.
	mustSample(t, samples, "ecs_namespace_quota_hard_bytes", 10*gib, s3)
	mustSample(t, samples, "ecs_namespace_quota_soft_bytes", 8*gib, s3)
	// Billing total_size is KB (sizeunit=KB) → bytes.
	mustSample(t, samples, "ecs_namespace_used_bytes", 107*kib, s3)
	mustSample(t, samples, "ecs_namespace_objects", 8, s3)
	mustSample(t, samples, "ecs_namespace_mpu_used_bytes", 10*kib, s3)
	mustSample(t, samples, "ecs_namespace_mpu_parts", 2, s3)

	swift := Label{"namespace", "swift"}
	// Unset quotas (-1) must be absent, not negative.
	if _, ok := findSample(samples, "ecs_namespace_quota_hard_bytes", swift); ok {
		t.Error("swift hard quota should be absent (unset)")
	}
	if _, ok := findSample(samples, "ecs_namespace_quota_soft_bytes", swift); ok {
		t.Error("swift soft quota should be absent (unset)")
	}
	mustSample(t, samples, "ecs_namespace_used_bytes", 0, swift)
	mustSample(t, samples, "ecs_namespace_objects", 0, swift)
}

// Quotas are fetched concurrently; the emitted order must still follow the
// namespace inventory so --once --debug output is stable between cycles.
func TestMeteringQuotaOrderFollowsInventory(t *testing.T) {
	samples, err := Metering{Quotas: true}.Collect(context.Background(), mockClient(t))
	if err != nil {
		t.Fatal(err)
	}
	var quotaNamespaces []string
	for _, s := range samples {
		if strings.HasPrefix(s.Name, "ecs_namespace_quota_") {
			quotaNamespaces = append(quotaNamespaces, s.LabelValue("namespace"))
		}
	}
	// namespaces.json lists s3 before swift, and swift's quotas are both unset.
	if want := []string{"s3", "s3"}; !slices.Equal(quotaNamespaces, want) {
		t.Errorf("quota namespaces = %v, want %v", quotaNamespaces, want)
	}
}

func TestMeteringWithoutQuotas(t *testing.T) {
	c := mockClient(t)
	samples, err := Metering{}.Collect(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}

	// Usage still comes from the single bulk billing POST...
	mustSample(t, samples, "ecs_namespace_used_bytes", 107*kib, Label{"namespace", "s3"})
	// ...while the per-namespace quota requests are not issued at all. Asserting
	// on the call log, not just on absent samples: the point of the flag is to
	// drop one request per namespace per cycle.
	for _, path := range c.Calls() {
		if strings.HasSuffix(path, "/quota") {
			t.Errorf("quota request issued with quotas disabled: %s", path)
		}
	}
	if _, ok := findSample(samples, "ecs_namespace_quota_hard_bytes", Label{"namespace", "s3"}); ok {
		t.Error("quota samples should be absent with quotas disabled")
	}
}

func TestNewMeteringHonorsClusterFlag(t *testing.T) {
	off := false
	if got := NewMetering(config.Cluster{}); !got.Quotas {
		t.Error("quotas should default to enabled when collectQuotas is unset")
	}
	if got := NewMetering(config.Cluster{CollectQuotas: &off}); got.Quotas {
		t.Error("collectQuotas: false should disable the quota fetch")
	}
}
