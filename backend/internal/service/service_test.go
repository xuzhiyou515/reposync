package service

import "testing"

func TestBuildCacheKeyStable(t *testing.T) {
	left := buildCacheKey("source", "target")
	right := buildCacheKey("source", "target")
	if left != right {
		t.Fatalf("expected cache key to be stable")
	}
}
