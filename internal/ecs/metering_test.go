package ecs

import (
	"context"
	"testing"
)

func TestMeteringCollect(t *testing.T) {
	samples, err := Metering{}.Collect(context.Background(), mockClient(t))
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
