package storage

import (
	"bytes"
	"testing"

	"github.com/traycers/farc/fblock"
)

const (
	assembleTailFblockSize  = 4096
	assembleTailParamsSize  = 64
	assembleTailCatalogSize = 128
	assembleTailAlignment   = 1
)

// expectedTail builds the trailer bytes the same way assembleFblock's own
// original inline code did, independently of assembleTail's implementation
// -- the CRC32 must cover the full content padded to capacity, never just
// whatever's actually appended to the returned tail.
func expectedTail(t *testing.T, content, tocBuf []byte, alreadyWritten int64) []byte {
	t.Helper()
	contentCap := fblock.ContentSize(assembleTailFblockSize, assembleTailParamsSize, assembleTailCatalogSize, uint32(len(tocBuf)), assembleTailAlignment)
	if contentCap < int64(len(content)) {
		t.Fatalf("test setup: content %d bytes exceeds capacity %d", len(content), contentCap)
	}
	padded := make([]byte, contentCap)
	copy(padded, content)
	epilog := fblock.Epilog{
		CRC32Content: fblock.CRC32(padded),
		CRC32TOC:     fblock.CRC32(tocBuf),
		TOCSize:      uint32(len(tocBuf)),
	}
	padLen := contentCap - int64(len(content))

	want := make([]byte, 0, int64(len(content))-alreadyWritten+padLen+int64(len(fblock.MagicTOC))+int64(len(tocBuf))+fblock.EpilogSize)
	want = append(want, content[alreadyWritten:]...)
	want = append(want, make([]byte, padLen)...)
	want = append(want, fblock.MagicTOC[:]...)
	want = append(want, tocBuf...)
	want = append(want, fblock.EncodeEpilog(epilog)...)
	return want
}

func TestAssembleTail_FullContent_AlreadyWrittenZero(t *testing.T) {
	content := []byte("hello fcontainer content")
	tocBuf := []byte("toc-bytes")

	got, err := assembleTail(content, tocBuf, assembleTailFblockSize, assembleTailParamsSize, assembleTailCatalogSize, assembleTailAlignment, 0)
	if err != nil {
		t.Fatalf("assembleTail: %v", err)
	}

	want := expectedTail(t, content, tocBuf, 0)
	if !bytes.Equal(got, want) {
		t.Fatalf("assembleTail(alreadyWritten=0) = %x, want %x", got, want)
	}
}

func TestAssembleTail_PartialAlreadyWritten_CRCStillCoversFullContent(t *testing.T) {
	content := []byte("abcdefghij")
	tocBuf := []byte("t")
	const alreadyWritten = 4

	got, err := assembleTail(content, tocBuf, assembleTailFblockSize, assembleTailParamsSize, assembleTailCatalogSize, assembleTailAlignment, alreadyWritten)
	if err != nil {
		t.Fatalf("assembleTail: %v", err)
	}

	want := expectedTail(t, content, tocBuf, alreadyWritten)
	if !bytes.Equal(got, want) {
		t.Fatalf("assembleTail(alreadyWritten=%d) = %x, want %x", alreadyWritten, got, want)
	}
	if !bytes.HasPrefix(got, content[alreadyWritten:]) {
		t.Fatalf("assembleTail(alreadyWritten=%d) does not start with the unwritten remainder", alreadyWritten)
	}
}

func TestAssembleTail_AlreadyWrittenEqualsContentLen_NoContentBytesInTail(t *testing.T) {
	content := []byte("recovered-on-disk-bytes")
	tocBuf := []byte("recovered-toc")

	got, err := assembleTail(content, tocBuf, assembleTailFblockSize, assembleTailParamsSize, assembleTailCatalogSize, assembleTailAlignment, int64(len(content)))
	if err != nil {
		t.Fatalf("assembleTail: %v", err)
	}

	want := expectedTail(t, content, tocBuf, int64(len(content)))
	if !bytes.Equal(got, want) {
		t.Fatalf("assembleTail(alreadyWritten=len(content)) = %x, want %x", got, want)
	}
	if bytes.HasPrefix(got, content) {
		t.Fatal("assembleTail(alreadyWritten=len(content)) unexpectedly re-includes content bytes")
	}
}

func TestAssembleTail_ContentExceedsCapacity_ReturnsError(t *testing.T) {
	hugeContent := make([]byte, assembleTailFblockSize*2)

	_, err := assembleTail(hugeContent, nil, assembleTailFblockSize, assembleTailParamsSize, assembleTailCatalogSize, assembleTailAlignment, 0)
	if err == nil {
		t.Fatal("assembleTail with oversized content = nil error, want error")
	}
}
