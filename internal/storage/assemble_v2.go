package storage

import (
	"hash/crc32"
	"time"

	fblockv2 "github.com/traycers/farc/fblock/v2"
	"github.com/traycers/farc/internal/storageengine"
)

// writeNodeAndEpilogRow writes one v2.0 node at offset (relative to the
// fblock's own base offset) through engine, then rewrites the fixed-offset
// epilog to reflect it — no new StorageEngine API is needed for this:
// the epilog is just another ordinary write-verified job at a fixed
// offset, queued right after the node's own (ADR-023 §"Решение" — a
// crash between nodes leaves a valid epilog whose Count reports exactly
// how many nodes are confirmed on disk, not just v1.0's binary
// ready/in_progress). epilog is mutated in place (Count incremented,
// Rows[slot] filled) so callers can chain calls across nodes.
func writeNodeAndEpilogRow(engine *storageengine.Engine, base, epilogOffset int64, offset uint64, n fblockv2.Node, alignment int, slot int, epilog *fblockv2.Epilog) error {
	encoded, err := fblockv2.EncodeNode(n, alignment)
	if err != nil {
		return err
	}
	_, err = engine.EnqueueWrite(base+int64(offset), encoded).Wait()
	if err != nil {
		return err
	}

	epilog.Rows[slot] = fblockv2.EpilogRow{
		Type:   n.Type,
		ID:     n.ID,
		Offset: offset,
		Size:   uint64(len(encoded)),
		CRC32:  crc32.ChecksumIEEE(n.Value), // mirrors the node's own crc32_value (fblockv2.EpilogRow doc)
	}
	epilog.Count++

	_, err = engine.EnqueueWrite(epilogOffset, fblockv2.EncodeEpilog(*epilog)).Wait()
	return err
}

// writeFblockV2Progressive writes a complete v2.0 fblock at idx through
// engine (StorageEngine — ADR-005's sole disk owner): fixed prolog, then
// root/params/catalog/content/toc in order, rewriting the epilog after
// each. This is a one-shot helper (all node values known upfront) — the
// truly incremental content-append path (EnqueueOpenWrite/Append/Close,
// ADR-017) is wired in during the real write-path cutover, not here; this
// proves the progressive-epilog mechanism itself works through the real
// engine.
func writeFblockV2Progressive(engine *storageengine.Engine, geo Geometry, idx uint32, alignment int, prolog fblockv2.FixedProlog, params, catalog, content, toc []byte) error {
	base := int64(fblockOffset(geo, idx))
	epilogOffset := base + int64(geo.FblockSize) - int64(fblockv2.EpilogSizeV2)

	prologBuf := fblockv2.EncodeFixedProlog(prolog)
	_, err := engine.EnqueueWrite(base, prologBuf).Wait()
	if err != nil {
		return err
	}

	nodes := []fblockv2.Node{
		{Type: fblockv2.NodeTypeRoot, ID: 0, Parent: 0, Sibling: 0},
		{Type: fblockv2.NodeTypeParams, ID: 1, Parent: 0, Sibling: 0, Value: params},
		{Type: fblockv2.NodeTypeCatalog, ID: 2, Parent: 0, Sibling: 1, Value: catalog},
		{Type: fblockv2.NodeTypeContent, ID: 3, Parent: 0, Sibling: 2, Value: content},
		{Type: fblockv2.NodeTypeTOC, ID: 4, Parent: 0, Sibling: 3, Value: toc},
	}

	var epilog fblockv2.Epilog
	offset := uint64(len(prologBuf))
	for i, n := range nodes {
		err := writeNodeAndEpilogRow(engine, base, epilogOffset, offset, n, alignment, i, &epilog)
		if err != nil {
			return err
		}
		offset += epilog.Rows[i].Size
	}
	return nil
}

// roundUpV2 rounds n up to the nearest multiple of alignment. alignment<=1
// means "no alignment requirement" (a no-op) — same convention as
// fblockv2's own internal roundUp.
func roundUpV2(n, alignment int) int {
	if alignment <= 1 {
		return n
	}
	if rem := n % alignment; rem != 0 {
		return n + (alignment - rem)
	}
	return n
}

// writeEpilogAligned returns the offset and bytes to physically write the
// epilog at, padded so the write itself satisfies the backend's
// Alignment() requirement (both offset and length must be a multiple of
// it, ADR-010) — the epilog's own EpilogSizeV2 logical bytes always end
// exactly at fblockEnd (base+fblock_size), placed at the tail of this
// padded block, so a reader locating it by that same fixed
// offset-from-end is unaffected; only where the *write* begins moves
// earlier to cover the padding.
func writeEpilogAligned(fblockEnd int64, epilog fblockv2.Epilog, alignment int) (offset int64, block []byte) {
	logical := fblockv2.EncodeEpilog(epilog)
	blockSize := roundUpV2(len(logical), alignment)
	block = make([]byte, blockSize)
	copy(block[blockSize-len(logical):], logical)
	return fblockEnd - int64(blockSize), block
}

// writeStaticNodesV2 writes prolog+root+params+catalog — everything a
// v2.0 fblock needs before content can start, always fully known upfront
// — as one combined buffer through engine, then brings the epilog to
// Count==3. This is promoteLocked's v2.0 equivalent of v1.0's
// assembleHeaderAndMagic one-shot header blob (ADR-023 §"Решение"):
// content (and later TOC) are the only nodes not known at this point, so
// they're handled separately (nodeWriterV2, and the final TOC+epilog
// write at Close). Returns the epilog (Count==3) and the absolute offset
// (relative to base) where the content node must start.
func writeStaticNodesV2(engine *storageengine.Engine, base int64, fblockSize uint64, prolog fblockv2.FixedProlog, paramsBuf, catalogBuf []byte, alignment int) (epilog fblockv2.Epilog, contentOffset uint64, err error) {
	prologBuf := fblockv2.EncodeFixedProlog(prolog)
	// The fixed prolog isn't a node and so isn't self-padding like
	// EncodeNode's output — pad it to alignment here so every node after
	// it (and the combined write below) starts/ends on an alignment
	// boundary too (ADR-010), mirroring v1.0's own header-pad gap.
	prologPad := roundUpV2(len(prologBuf), alignment)

	nodes := []fblockv2.Node{
		{Type: fblockv2.NodeTypeRoot, ID: 0, Parent: 0, Sibling: 0},
		{Type: fblockv2.NodeTypeParams, ID: 1, Parent: 0, Sibling: 0, Value: paramsBuf},
		{Type: fblockv2.NodeTypeCatalog, ID: 2, Parent: 0, Sibling: 1, Value: catalogBuf},
	}
	capacity := prologPad
	for _, n := range nodes {
		capacity += int(fblockv2.NodeTotalSize(int64(len(n.Value)), alignment))
	}

	buf := make([]byte, 0, capacity)
	buf = append(buf, prologBuf...)
	buf = append(buf, make([]byte, prologPad-len(prologBuf))...)

	offset := uint64(len(buf))
	for i, n := range nodes {
		encoded, err := fblockv2.EncodeNode(n, alignment)
		if err != nil {
			return fblockv2.Epilog{}, 0, err
		}
		epilog.Rows[i] = fblockv2.EpilogRow{
			Type:   n.Type,
			ID:     n.ID,
			Offset: offset,
			Size:   uint64(len(encoded)),
			CRC32:  crc32.ChecksumIEEE(n.Value),
		}
		buf = append(buf, encoded...)
		offset += uint64(len(encoded))
	}
	epilog.Count = 3

	_, err = engine.EnqueueWrite(base, buf).Wait()
	if err != nil {
		return fblockv2.Epilog{}, 0, err
	}

	epilogWriteOffset, epilogBlock := writeEpilogAligned(base+int64(fblockSize), epilog, alignment)
	_, err = engine.EnqueueWrite(epilogWriteOffset, epilogBlock).Wait()
	if err != nil {
		return fblockv2.Epilog{}, 0, err
	}

	return epilog, offset, nil
}

// nodeWriterV2 streams a v2.0 node's value through StorageEngine's
// open-write mechanism (ADR-017's periodic flush + MagicTrailer,
// unchanged) before the node's own header — which needs the final
// value_size, not known until the stream ends — or trailer can be
// written (ADR-023 §"Решение"). It retains the value bytes appended so
// far only because writeTailLocked's existing tail-write contract already
// needs "whatever wasn't physically flushed yet" resupplied by the
// caller (exactly as v1.0's assembleTail does today); since that buffer
// is already in memory, crc32_value is computed from it once at close —
// no disk re-read, and no separate running hash needed on top of a buffer
// already held for this other reason.
type nodeWriterV2 struct {
	engine     *storageengine.Engine
	handle     *storageengine.WriteHandle
	buffered   []byte
	nodeType   fblockv2.NodeType
	id         uint32
	parent     uint32
	sibling    uint32
	base       int64
	nodeOffset uint64
}

// openNodeWriterV2 starts nodeType's open-write job at the node's *value*
// region — base+nodeOffset+FixedHeaderSize — skipping the node's own
// fixedHeaderSize-byte header, which isn't written until close.
func openNodeWriterV2(engine *storageengine.Engine, base int64, nodeOffset uint64, nodeType fblockv2.NodeType, id, parent, sibling uint32, timeout time.Duration) *nodeWriterV2 {
	valueBase := base + int64(nodeOffset) + int64(fblockv2.FixedHeaderSize)
	return &nodeWriterV2{
		engine:     engine,
		handle:     engine.EnqueueOpenWrite(valueBase, nil, timeout),
		nodeType:   nodeType,
		id:         id,
		parent:     parent,
		sibling:    sibling,
		base:       base,
		nodeOffset: nodeOffset,
	}
}

// Append supplies more of the node's value. Must not be called after close.
func (w *nodeWriterV2) Append(data []byte) error {
	err := w.handle.Append(data)
	if err != nil {
		return err
	}
	w.buffered = append(w.buffered, data...)
	return nil
}

// close finishes the node: waits for the open job, then writes the fixed
// header (now that value_size is final) at the node's own start, and the
// tail (any still-unflushed value bytes + padding + crc32_header +
// crc32_value + magic_finish) at the job's current TrailerOffset — the
// same "overwrite the last magic trailer" pattern segment.go's
// writeTailLocked already uses for v1.0. Returns the epilog row
// describing the finished node, for the caller to fold into the
// progressive epilog rewrite (writeNodeAndEpilogRow above is for nodes
// whose value is already fully known, not this streamed path).
func (w *nodeWriterV2) close(alignment int) (fblockv2.EpilogRow, error) {
	ticket := w.handle.Close()
	_, err := ticket.Wait()
	if err != nil {
		return fblockv2.EpilogRow{}, err
	}

	valueSize := int64(len(w.buffered))
	header, crc32Header, err := fblockv2.EncodeNodeHeader(w.nodeType, w.id, w.parent, w.sibling, valueSize)
	if err != nil {
		return fblockv2.EpilogRow{}, err
	}
	_, err = w.engine.EnqueueWrite(w.base+int64(w.nodeOffset), header).Wait()
	if err != nil {
		return fblockv2.EpilogRow{}, err
	}

	crc32Value := crc32.ChecksumIEEE(w.buffered)
	remaining := w.buffered[w.handle.Written():]
	tail := append(append([]byte{}, remaining...), fblockv2.EncodeNodeTail(valueSize, alignment, crc32Header, crc32Value)...)

	valueBase := w.base + int64(w.nodeOffset) + int64(fblockv2.FixedHeaderSize)
	_, err = w.engine.EnqueueWrite(valueBase+w.handle.TrailerOffset(), tail).Wait()
	if err != nil {
		return fblockv2.EpilogRow{}, err
	}

	return fblockv2.EpilogRow{
		Type:   w.nodeType,
		ID:     w.id,
		Offset: w.nodeOffset,
		Size:   uint64(fblockv2.NodeTotalSize(valueSize, alignment)),
		CRC32:  crc32Value,
	}, nil
}
