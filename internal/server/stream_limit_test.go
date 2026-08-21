package server

import "testing"

func TestBoundedChunkLimitStopsOversizedStreamingResponse(t *testing.T) {
	tests := []struct {
		written int64
		maximum int64
		chunk   int
		allowed int
		limited bool
	}{
		{written: 0, maximum: 1024, chunk: 512, allowed: 512},
		{written: 900, maximum: 1024, chunk: 512, allowed: 124, limited: true},
		{written: 1024, maximum: 1024, chunk: 1, allowed: 0, limited: true},
		{written: 1 << 30, maximum: 0, chunk: 32 << 10, allowed: 32 << 10},
	}
	for _, test := range tests {
		allowed, limited := boundedChunkLimit(test.written, test.maximum, test.chunk)
		if allowed != test.allowed || limited != test.limited {
			t.Fatalf("written=%d max=%d chunk=%d got=(%d,%v) want=(%d,%v)", test.written, test.maximum, test.chunk, allowed, limited, test.allowed, test.limited)
		}
	}
}
