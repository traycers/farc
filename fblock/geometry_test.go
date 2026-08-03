package fblock

import (
	"errors"
	"testing"
)

func TestComputeOffsets(t *testing.T) {
	o := ComputeOffsets(100, 2000)
	if o.ParamsOffset != 56 {
		t.Errorf("ParamsOffset = %d, want 56", o.ParamsOffset)
	}
	if o.MagicCatalogOffset != 156 { // 56+100
		t.Errorf("MagicCatalogOffset = %d, want 156", o.MagicCatalogOffset)
	}
	if o.CatalogOffset != 164 { // 156+8
		t.Errorf("CatalogOffset = %d, want 164", o.CatalogOffset)
	}
	if o.ChecksumsOffset != 2164 { // 164+2000
		t.Errorf("ChecksumsOffset = %d, want 2164", o.ChecksumsOffset)
	}
	if o.MagicContentOffset != 2176 { // 2164+12
		t.Errorf("MagicContentOffset = %d, want 2176", o.MagicContentOffset)
	}
	if o.ContentOffset != 2184 { // 2176+8
		t.Errorf("ContentOffset = %d, want 2184", o.ContentOffset)
	}
}

func TestMaxContainerSizeAndContentSize(t *testing.T) {
	const fblockSize = 1_000_000
	const paramsSize, catalogSize, tocSize = 100, 2000, 500
	// 112 fixed overhead per §8.3.
	wantMax := int64(fblockSize) - 112 - paramsSize - catalogSize
	if got := MaxContainerSize(fblockSize, paramsSize, catalogSize); got != wantMax {
		t.Errorf("MaxContainerSize = %d, want %d", got, wantMax)
	}
	wantContent := wantMax - tocSize
	if got := ContentSize(fblockSize, paramsSize, catalogSize, tocSize); got != wantContent {
		t.Errorf("ContentSize = %d, want %d", got, wantContent)
	}
}

func TestCheckMinContainerShare(t *testing.T) {
	const fblockSize = 1_000_000
	const share = 0.7
	// Choose catalog/params sizes such that max container size is exactly
	// at the boundary, then nudge across it in both directions.
	maxAllowedOverhead := fblockSize - 112 - int64(share*fblockSize) // overhead budget before violating the share

	okCatalogSize := uint32(maxAllowedOverhead) // max == share*fblockSize exactly
	if err := CheckMinContainerShare(fblockSize, 0, okCatalogSize, share); err != nil {
		t.Errorf("expected boundary case to pass, got %v", err)
	}

	tooMuchOverhead := okCatalogSize + 1
	err := CheckMinContainerShare(fblockSize, 0, tooMuchOverhead, share)
	if !errors.Is(err, ErrContainerShareTooSmall) {
		t.Errorf("expected ErrContainerShareTooSmall just past the boundary, got %v", err)
	}
}

func TestCheckMinContainerShareRealisticInitCase(t *testing.T) {
	// A plausible small Storage: 64MiB fblocks, C=256, few thousand fblocks.
	const fblockSize = 64 << 20
	const c = 256
	const n = 4096
	catalogSize := CatalogSize(c, n)
	if err := CheckMinContainerShare(fblockSize, 200, catalogSize, DefaultMinContainerShare); err != nil {
		t.Errorf("realistic small-storage geometry should satisfy default share: %v", err)
	}
}
