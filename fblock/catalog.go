package fblock

import (
	"encoding/binary"
	"fmt"
)

// State is a fblock's state, encoded in bits 0-1 of its catalog flags byte
// (docs/docs/archive/03-storage-format.md §6.3).
type State uint8

const (
	Uninitialized State = 0
	InProgress    State = 1
	Ready         State = 2
	Bad           State = 3
)

func (s State) String() string {
	switch s {
	case Uninitialized:
		return "uninitialized"
	case InProgress:
		return "in_progress"
	case Ready:
		return "ready"
	case Bad:
		return "bad"
	default:
		return fmt.Sprintf("State(%d)", uint8(s))
	}
}

const (
	stateMask     = 0x03
	protectedBit  = 0x04
	reservedFlags = 0xF8 // bits 3-7, must be 0
)

// EntrySize is the per-fblock catalog entry size in bytes: flags(1) +
// uuid(16) + begin(8) + end(8) + channel_bitmap(ceil(C/8)) — 33+ceil(C/8)
// (docs/docs/archive/03-storage-format.md §6.2).
func EntrySize(c uint16) uint32 {
	return 33 + uint32(rowBytes(c))
}

func rowBytes(c uint16) int {
	return (int(c) + 7) / 8
}

// CatalogSize computes catalog_size = C×2 + N×(33+ceil(C/8))
// (docs/docs/archive/03-storage-format.md §6.2).
func CatalogSize(c uint16, n uint32) uint32 {
	return uint32(c)*2 + n*EntrySize(c)
}

// Catalog is the per-fblock Structure-of-Arrays snapshot of every fblock's
// state, plus the storage-wide compact channel registry (ADR-014).
type Catalog struct {
	MaxChannels uint16 // C
	N           uint32 // catalog_entry_count

	ChannelRegistry []uint16 // len C; position -> channel number, 0 = never allocated

	Flags         []uint8    // len N
	UUID          [][16]byte // len N
	Begin         []uint64   // len N, ns
	End           []uint64   // len N, ns
	ChannelBitmap []byte     // len N * rowBytes(C), row i occupies [i*rowBytes:(i+1)*rowBytes]
}

// NewCatalog allocates a zeroed catalog for C channel-registry positions and
// N fblocks — the state every fblock has immediately after Storage init
// (all-zero = all uninitialized, ADR-006).
func NewCatalog(c uint16, n uint32) *Catalog {
	return &Catalog{
		MaxChannels:     c,
		N:               n,
		ChannelRegistry: make([]uint16, c),
		Flags:           make([]uint8, n),
		UUID:            make([][16]byte, n),
		Begin:           make([]uint64, n),
		End:             make([]uint64, n),
		ChannelBitmap:   make([]byte, int(n)*rowBytes(c)),
	}
}

// State returns fblock i's state.
func (c *Catalog) State(i uint32) State {
	return State(c.Flags[i] & stateMask)
}

// SetState sets fblock i's state, preserving the protected bit.
func (c *Catalog) SetState(i uint32, s State) {
	c.Flags[i] = (c.Flags[i] &^ stateMask) | uint8(s)
}

// Protected reports whether fblock i's protected flag is set.
func (c *Catalog) Protected(i uint32) bool {
	return c.Flags[i]&protectedBit != 0
}

// SetProtected sets or clears fblock i's protected flag. Only meaningful for
// Ready fblocks (docs/docs/archive/00-requirements.md §4.6) — callers must
// enforce that precondition, this method does not.
func (c *Catalog) SetProtected(i uint32, protected bool) {
	if protected {
		c.Flags[i] |= protectedBit
	} else {
		c.Flags[i] &^= protectedBit
	}
}

// ChannelBit reports whether compact position pos is set in fblock i's
// channel_bitmap.
func (c *Catalog) ChannelBit(i uint32, pos uint16) bool {
	rb := rowBytes(c.MaxChannels)
	byteIdx := int(i)*rb + int(pos)/8
	return c.ChannelBitmap[byteIdx]&(1<<(pos%8)) != 0
}

// SetChannelBit sets or clears compact position pos in fblock i's
// channel_bitmap.
func (c *Catalog) SetChannelBit(i uint32, pos uint16, set bool) {
	rb := rowBytes(c.MaxChannels)
	byteIdx := int(i)*rb + int(pos)/8
	mask := byte(1 << (pos % 8))
	if set {
		c.ChannelBitmap[byteIdx] |= mask
	} else {
		c.ChannelBitmap[byteIdx] &^= mask
	}
}

// RefCount returns how many fblocks currently reference compact position
// pos — used to decide whether a channel-registry position is safe to reuse
// (ADR-014, docs/docs/archive/00-requirements.md §4.2/§4.12). Recomputed on
// load, never persisted.
func (c *Catalog) RefCount(pos uint16) int {
	n := 0
	for i := uint32(0); i < c.N; i++ {
		if c.ChannelBit(i, pos) {
			n++
		}
	}
	return n
}

// AllocatedPrefix returns k such that ChannelRegistry[0:k] are the
// ever-allocated positions (a contiguous prefix by construction, since
// positions are always allocated in increasing order of lowest-free-index)
// and ChannelRegistry[k:] are all still 0 ("never allocated").
func (c *Catalog) AllocatedPrefix() int {
	k := 0
	for k < len(c.ChannelRegistry) && c.ChannelRegistry[k] != 0 {
		k++
	}
	return k
}

// Clone returns a deep copy of c, safe for the caller to mutate without
// affecting the original (e.g. patching a single in-flight entry before
// embedding the copy into a fblock header being written, or before writing
// it to the SSD catalog mirror, ADR-007).
func (c *Catalog) Clone() *Catalog {
	return &Catalog{
		MaxChannels:     c.MaxChannels,
		N:               c.N,
		ChannelRegistry: append([]uint16(nil), c.ChannelRegistry...),
		Flags:           append([]uint8(nil), c.Flags...),
		UUID:            append([][16]byte(nil), c.UUID...),
		Begin:           append([]uint64(nil), c.Begin...),
		End:             append([]uint64(nil), c.End...),
		ChannelBitmap:   append([]byte(nil), c.ChannelBitmap...),
	}
}

// EncodeCatalog serializes c into a new CatalogSize(c.MaxChannels, c.N)-byte
// buffer, per the exact layout in docs/docs/archive/03-storage-format.md §6.
func EncodeCatalog(c *Catalog) ([]byte, error) {
	if err := validateCatalogShape(c); err != nil {
		return nil, err
	}
	rb := rowBytes(c.MaxChannels)
	size := CatalogSize(c.MaxChannels, c.N)
	buf := make([]byte, size)

	off := 0
	for i, ch := range c.ChannelRegistry {
		binary.LittleEndian.PutUint16(buf[off+i*2:off+i*2+2], ch)
	}
	off += int(c.MaxChannels) * 2

	flagsOff := off
	copy(buf[flagsOff:flagsOff+int(c.N)], c.Flags)

	uuidOff := flagsOff + int(c.N)*1
	for i, u := range c.UUID {
		copy(buf[uuidOff+i*16:uuidOff+i*16+16], u[:])
	}

	beginOff := uuidOff + int(c.N)*16
	for i, b := range c.Begin {
		binary.LittleEndian.PutUint64(buf[beginOff+i*8:beginOff+i*8+8], b)
	}

	endOff := beginOff + int(c.N)*8
	for i, e := range c.End {
		binary.LittleEndian.PutUint64(buf[endOff+i*8:endOff+i*8+8], e)
	}

	bitmapOff := endOff + int(c.N)*8
	copy(buf[bitmapOff:bitmapOff+int(c.N)*rb], c.ChannelBitmap)

	return buf, nil
}

// DecodeCatalog parses a catalog of C channel positions and N fblock entries
// from buf. buf must be exactly CatalogSize(c, n) bytes.
func DecodeCatalog(buf []byte, c uint16, n uint32) (*Catalog, error) {
	want := CatalogSize(c, n)
	if uint32(len(buf)) != want {
		return nil, fmt.Errorf("fblock: catalog buffer size %d != expected %d (C=%d, N=%d)", len(buf), want, c, n)
	}
	rb := rowBytes(c)
	cat := &Catalog{MaxChannels: c, N: n}

	cat.ChannelRegistry = make([]uint16, c)
	off := 0
	for i := range cat.ChannelRegistry {
		cat.ChannelRegistry[i] = binary.LittleEndian.Uint16(buf[off+i*2 : off+i*2+2])
	}
	off += int(c) * 2

	flagsOff := off
	cat.Flags = make([]uint8, n)
	copy(cat.Flags, buf[flagsOff:flagsOff+int(n)])

	uuidOff := flagsOff + int(n)*1
	cat.UUID = make([][16]byte, n)
	for i := range cat.UUID {
		copy(cat.UUID[i][:], buf[uuidOff+i*16:uuidOff+i*16+16])
	}

	beginOff := uuidOff + int(n)*16
	cat.Begin = make([]uint64, n)
	for i := range cat.Begin {
		cat.Begin[i] = binary.LittleEndian.Uint64(buf[beginOff+i*8 : beginOff+i*8+8])
	}

	endOff := beginOff + int(n)*8
	cat.End = make([]uint64, n)
	for i := range cat.End {
		cat.End[i] = binary.LittleEndian.Uint64(buf[endOff+i*8 : endOff+i*8+8])
	}

	bitmapOff := endOff + int(n)*8
	cat.ChannelBitmap = make([]byte, int(n)*rb)
	copy(cat.ChannelBitmap, buf[bitmapOff:bitmapOff+int(n)*rb])

	return cat, nil
}

func validateCatalogShape(c *Catalog) error {
	rb := rowBytes(c.MaxChannels)
	switch {
	case len(c.ChannelRegistry) != int(c.MaxChannels):
		return fmt.Errorf("fblock: catalog: ChannelRegistry len %d != MaxChannels %d", len(c.ChannelRegistry), c.MaxChannels)
	case len(c.Flags) != int(c.N):
		return fmt.Errorf("fblock: catalog: Flags len %d != N %d", len(c.Flags), c.N)
	case len(c.UUID) != int(c.N):
		return fmt.Errorf("fblock: catalog: UUID len %d != N %d", len(c.UUID), c.N)
	case len(c.Begin) != int(c.N):
		return fmt.Errorf("fblock: catalog: Begin len %d != N %d", len(c.Begin), c.N)
	case len(c.End) != int(c.N):
		return fmt.Errorf("fblock: catalog: End len %d != N %d", len(c.End), c.N)
	case len(c.ChannelBitmap) != int(c.N)*rb:
		return fmt.Errorf("fblock: catalog: ChannelBitmap len %d != N*rowBytes %d", len(c.ChannelBitmap), int(c.N)*rb)
	}
	for i, f := range c.Flags {
		if f&reservedFlags != 0 {
			return fmt.Errorf("fblock: catalog: entry %d has non-zero reserved flag bits: %#x", i, f)
		}
	}
	return nil
}
