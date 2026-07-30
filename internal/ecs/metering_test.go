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

	// Billing total_size is KB (sizeunit=KB) → bytes.
	s3 := Label{"namespace", "s3"}
	mustSample(t, samples, "ecs_namespace_used_bytes", 107*kib, s3)
	mustSample(t, samples, "ecs_namespace_objects", 8, s3)
	mustSample(t, samples, "ecs_namespace_mpu_used_bytes", 10*kib, s3)
	mustSample(t, samples, "ecs_namespace_mpu_parts", 2, s3)

	swift := Label{"namespace", "swift"}
	mustSample(t, samples, "ecs_namespace_used_bytes", 0, swift)
	mustSample(t, samples, "ecs_namespace_objects", 0, swift)

	// Quotas are the Quotas collector's job; metering must not issue their
	// requests, which is the whole point of the split.
	c := mockClient(t)
	if _, err := (Metering{}).Collect(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	for _, path := range c.Calls() {
		if path != pathNamespaces && path != pathBillingBulk {
			t.Errorf("metering issued an unexpected request: %s", path)
		}
	}
}
