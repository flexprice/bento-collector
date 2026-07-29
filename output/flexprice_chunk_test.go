package output

import "testing"

// chunkBounds mirrors the loop in sendBulkEvents, isolating the index arithmetic
// so it can be verified without an API client.
func chunkBounds(total int) [][2]int {
	var bounds [][2]int
	for start := 0; start < total; start += maxBulkBatchSize {
		end := min(start+maxBulkBatchSize, total)
		bounds = append(bounds, [2]int{start, end})
	}
	return bounds
}

func TestChunkBoundsCoverEveryEventExactlyOnce(t *testing.T) {
	for _, total := range []int{1, 999, 1000, 1001, 2000, 2500} {
		bounds := chunkBounds(total)

		if got := bounds[0][0]; got != 0 {
			t.Errorf("total=%d: first chunk starts at %d, want 0", total, got)
		}
		if got := bounds[len(bounds)-1][1]; got != total {
			t.Errorf("total=%d: last chunk ends at %d, want %d", total, got, total)
		}

		covered := 0
		for i, b := range bounds {
			size := b[1] - b[0]
			if size <= 0 {
				t.Errorf("total=%d: chunk %d is empty (%v)", total, i, b)
			}
			if size > maxBulkBatchSize {
				t.Errorf("total=%d: chunk %d has %d events, exceeds limit %d", total, i, size, maxBulkBatchSize)
			}
			if i > 0 && b[0] != bounds[i-1][1] {
				t.Errorf("total=%d: chunk %d starts at %d, want %d (gap or overlap)", total, i, b[0], bounds[i-1][1])
			}
			covered += size
		}

		if covered != total {
			t.Errorf("total=%d: chunks cover %d events, want %d", total, covered, total)
		}
	}
}

func TestChunkBoundsSingleChunkWithinLimit(t *testing.T) {
	// At or below the limit the batch must not be split.
	for _, total := range []int{1, 200, 1000} {
		if got := len(chunkBounds(total)); got != 1 {
			t.Errorf("total=%d: got %d chunks, want 1", total, got)
		}
	}

	// One past the limit must split into exactly two.
	if got := len(chunkBounds(maxBulkBatchSize + 1)); got != 2 {
		t.Errorf("total=%d: got %d chunks, want 2", maxBulkBatchSize+1, got)
	}
}
