package output

import (
	"testing"

	"github.com/warpstreamlabs/bento/public/service"
)

// routesToBulk mirrors the endpoint fork in WriteBatch: a flush goes to the
// single-event endpoint only when it holds exactly one event AND no batching
// policy is configured.
func routesToBulk(eventCount int, batchingConfigured bool) bool {
	return !(eventCount == 1 && !batchingConfigured)
}

// TestBatchPolicyIsNoopDrivesAlwaysBulk pins the mapping from batch policy to
// routing: an empty policy leaves single events on the single-event endpoint,
// while any configured bound opts into bulk.
func TestBatchPolicyIsNoopDrivesAlwaysBulk(t *testing.T) {
	cases := []struct {
		name       string
		policy     service.BatchPolicy
		alwaysBulk bool
	}{
		{"no batching configured", service.BatchPolicy{}, false},
		{"count set", service.BatchPolicy{Count: 200}, true},
		{"period set", service.BatchPolicy{Period: "5s"}, true},
		{"byte size set", service.BatchPolicy{ByteSize: 1024}, true},
		{"count and period set", service.BatchPolicy{Count: 200, Period: "5s"}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := !tc.policy.IsNoop(); got != tc.alwaysBulk {
				t.Errorf("alwaysBulk = %v, want %v", got, tc.alwaysBulk)
			}
		})
	}
}

func TestSingleEventRoutesToBulkOnlyWhenBatchingConfigured(t *testing.T) {
	if routesToBulk(1, false) {
		t.Error("no batching policy: a one-event flush must use the single-event endpoint")
	}
	if !routesToBulk(1, true) {
		t.Error("batching configured: a one-event flush must use the bulk endpoint")
	}
}

func TestMultiEventAlwaysRoutesToBulk(t *testing.T) {
	// Above one event the policy is irrelevant — bulk either way.
	for _, count := range []int{2, 10, 200, 1000, 2500} {
		for _, batching := range []bool{false, true} {
			if !routesToBulk(count, batching) {
				t.Errorf("count=%d batching=%v: expected bulk endpoint", count, batching)
			}
		}
	}
}
