package v2

import "testing"

func TestContentSizeAndMaxContainerSize(t *testing.T) {
	const fblockSize = 1 << 20
	const alignment = 4096

	maxContainer := MaxContainerSize(fblockSize, 100, 5000, alignment)
	contentWithTOC := ContentSize(fblockSize, 100, 5000, 100000, alignment)

	if contentWithTOC >= maxContainer {
		t.Fatalf("content capacity with a nonzero TOC (%d) should be smaller than the zero-TOC max (%d)", contentWithTOC, maxContainer)
	}
	if maxContainer <= 0 || maxContainer >= fblockSize {
		t.Fatalf("maxContainer = %d, want in (0, %d)", maxContainer, fblockSize)
	}

	// MaxContainerSize is defined as ContentSize with tocSize=0.
	if got := ContentSize(fblockSize, 100, 5000, 0, alignment); got != maxContainer {
		t.Fatalf("ContentSize(tocSize=0) = %d, want MaxContainerSize = %d", got, maxContainer)
	}
}

func TestCheckMinContainerShare(t *testing.T) {
	const fblockSize = 1 << 20
	const alignment = 4096

	if err := CheckMinContainerShare(fblockSize, 100, 5000, 0.7, alignment); err != nil {
		t.Fatalf("CheckMinContainerShare: unexpected error for a realistic geometry: %v", err)
	}

	// A fblock too small to give 90% of itself to the container should fail.
	err := CheckMinContainerShare(fblockSize, 100, 5000, 0.999999, alignment)
	if err == nil {
		t.Fatalf("expected error for an unreachable min_container_share, got nil")
	}
}
