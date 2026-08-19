// Package fcontainer implements the Filler — the only public write-path API
// for a fcontainer's Content tree (docs/docs/archive/09-fcontainer-write-path.md).
// Everything about how frames/params are internally placed (id/offset
// allocation, sibling-chain continuation under concurrent producer calls)
// is deliberately not specified by the docs; this package resolves that gap
// with a single Filler-wide mutex held for the duration of each public
// call — simpler and easier to prove correct than the theoretically-minimal
// per-id/per-parent contention the docs gesture at (08-array-trees.md §11.2),
// at the cost of serializing all producer goroutines on one Filler. Revisit
// if profiling ever shows this serialization matters in practice.
package fcontainer

import (
	"encoding/binary"
	"math"
	"sync"

	"github.com/traycers/farc/mediatree"
)

// Filler accumulates one fcontainer's Content tree. Not safe for use after
// Freeze has returned — a Filler is single-use, matching the write-once
// fcontainer model (docs/docs/archive/05-data-format.md §2).
type Filler struct {
	mu        sync.Mutex
	elems     []mediatree.Element
	lastChild map[uint32]uint32 // parent id -> most recently added child id

	rootID     uint32
	channelsID uint32
	haveRoot   bool

	channelIDs map[uint32]uint32     // channel number -> channel node id
	streamsIDs map[uint32]uint32     // channel node id -> streams container id
	streamIDs  map[streamKey]uint32  // (channel node id, stream number) -> stream node id
	kindIDs    map[kindKey]uint32    // (stream node id, kind) -> video/audio tag node id
	configsIDs map[uint32]uint32     // kind node id -> configs container id
	framesIDs  map[uint32]uint32     // config node id -> frames container id
	configKind map[uint32]StreamKind // config node id -> kind, for AddFrames validation
}

type streamKey struct {
	channelID uint32
	stream    uint32
}

type kindKey struct {
	streamID uint32
	kind     StreamKind
}

// New creates an empty Filler.
func New() *Filler {
	return &Filler{
		lastChild:  make(map[uint32]uint32),
		channelIDs: make(map[uint32]uint32),
		streamsIDs: make(map[uint32]uint32),
		streamIDs:  make(map[streamKey]uint32),
		kindIDs:    make(map[kindKey]uint32),
		configsIDs: make(map[uint32]uint32),
		framesIDs:  make(map[uint32]uint32),
		configKind: make(map[uint32]StreamKind),
	}
}

// append adds one node and returns its id. Caller must hold f.mu. sibling is
// derived from lastChild[parent] (self-reference if parent has no children
// yet), and lastChild[parent] is updated to the new id — the exact append
// mutation from docs/docs/archive/08-array-trees.md §11.2.
func (f *Filler) append(parent uint32, typ mediatree.NodeType, role mediatree.Role, value []byte) uint32 {
	id := uint32(len(f.elems))
	sibling := id
	if lc, ok := f.lastChild[parent]; ok {
		sibling = lc
	}
	f.elems = append(f.elems, mediatree.Element{Type: typ, Role: role, Parent: parent, Sibling: sibling, Value: value})
	f.lastChild[parent] = id
	return id
}

func (f *Filler) ensureRoot() {
	if f.haveRoot {
		return
	}
	f.rootID = 0
	f.elems = append(f.elems, mediatree.Element{Type: mediatree.TypeVoid, Role: mediatree.RoleRoot, Parent: 0, Sibling: 0})
	f.haveRoot = true
	f.channelsID = f.append(f.rootID, mediatree.TypeVoid, mediatree.RoleChannels, nil)
}

func (f *Filler) getOrCreateChannel(channel uint32) uint32 {
	if id, ok := f.channelIDs[channel]; ok {
		return id
	}
	id := f.append(f.channelsID, mediatree.TypeUint32, mediatree.RoleChannel, u32(channel))
	f.channelIDs[channel] = id
	f.streamsIDs[id] = f.append(id, mediatree.TypeVoid, mediatree.RoleStreams, nil)
	return id
}

func (f *Filler) getOrCreateStream(channelID, stream uint32) uint32 {
	key := streamKey{channelID, stream}
	if id, ok := f.streamIDs[key]; ok {
		return id
	}
	streamsID := f.streamsIDs[channelID]
	id := f.append(streamsID, mediatree.TypeUint32, mediatree.RoleStream, u32(stream))
	f.streamIDs[key] = id
	return id
}

func (f *Filler) getOrCreateKindBranch(streamID uint32, kind StreamKind) uint32 {
	key := kindKey{streamID, kind}
	if id, ok := f.kindIDs[key]; ok {
		return id
	}
	role := mediatree.RoleVideo
	if kind == KindAudio {
		role = mediatree.RoleAudio
	}
	id := f.append(streamID, mediatree.TypeVoid, role, nil)
	f.kindIDs[key] = id
	f.configsIDs[id] = f.append(id, mediatree.TypeVoid, configsRole(kind), nil)
	return id
}

func configsRole(kind StreamKind) mediatree.Role {
	if kind == KindVideo {
		return mediatree.RoleConfigsVideo
	}
	return mediatree.RoleConfigsAudio
}

// AddStreamParams records a codec-parameter change for (channel, stream,
// kind), creating the channel/stream/kind branch on first reference
// (docs/docs/archive/09-fcontainer-write-path.md §3). Returns the new
// config node's id, to be passed to every AddFrames call for frames
// captured under these parameters.
func (f *Filler) AddStreamParams(channel, stream uint32, kind StreamKind, params StreamParams) (uint32, error) {
	err := params.Validate(kind)
	if err != nil {
		return 0, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.ensureRoot()
	channelID := f.getOrCreateChannel(channel)
	streamID := f.getOrCreateStream(channelID, stream)
	kindID := f.getOrCreateKindBranch(streamID, kind)
	configsID := f.configsIDs[kindID]

	configRole := mediatree.RoleConfigVideo
	dataRole := mediatree.RoleDataVideo
	framesRole := mediatree.RoleFramesVideo
	if kind == KindAudio {
		configRole = mediatree.RoleConfigAudio
		dataRole = mediatree.RoleDataAudio
		framesRole = mediatree.RoleFramesAudio
	}

	configID := f.append(configsID, mediatree.TypeTimestamp, configRole, u64(params.Time))
	dataID := f.append(configID, mediatree.TypeVoid, dataRole, nil)
	f.framesIDs[configID] = f.append(configID, mediatree.TypeVoid, framesRole, nil)
	f.configKind[configID] = kind

	if kind == KindVideo {
		f.append(dataID, mediatree.TypeUint8, mediatree.RoleCodecVideo, []byte{params.CodecVideo})
		f.append(dataID, mediatree.TypeBytes, mediatree.RoleParamSPS, params.ParamSPS)
		f.append(dataID, mediatree.TypeBytes, mediatree.RoleParamPPS, params.ParamPPS)
		if len(params.ParamVPS) > 0 {
			f.append(dataID, mediatree.TypeBytes, mediatree.RoleParamVPS, params.ParamVPS)
		}
		if params.Width != 0 {
			f.append(dataID, mediatree.TypeUint32, mediatree.RoleWidth, u32(params.Width))
		}
		if params.Height != 0 {
			f.append(dataID, mediatree.TypeUint32, mediatree.RoleHeight, u32(params.Height))
		}
		if params.HasFramerate {
			f.append(dataID, mediatree.TypeFloat64, mediatree.RoleFramerate, f64(params.Framerate))
		}
		if len(params.CodecProfile) > 0 {
			f.append(dataID, mediatree.TypeBytes, mediatree.RoleCodecProfileVideo, params.CodecProfile)
		}
	} else {
		f.append(dataID, mediatree.TypeUint8, mediatree.RoleCodecAudio, []byte{params.CodecAudio})
		f.append(dataID, mediatree.TypeUint32, mediatree.RoleSampleRate, u32(params.SampleRate))
		f.append(dataID, mediatree.TypeUint8, mediatree.RoleChannelCount, []byte{params.ChannelCount})
		if len(params.ParamAudioConfig) > 0 {
			f.append(dataID, mediatree.TypeBytes, mediatree.RoleParamAudioConfig, params.ParamAudioConfig)
		}
		if len(params.CodecProfile) > 0 {
			f.append(dataID, mediatree.TypeBytes, mediatree.RoleCodecProfileAudio, params.CodecProfile)
		}
	}

	return configID, nil
}

// AddFrames appends frames, in the exact order given, under configID's
// frames container (docs/docs/archive/09-fcontainer-write-path.md §4).
// configID must be a value previously returned by AddStreamParams on this
// same Filler.
func (f *Filler) AddFrames(configID uint32, frames []Frame) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	kind, ok := f.configKind[configID]
	if !ok {
		return ErrStaleConfigID
	}
	framesID := f.framesIDs[configID]

	frameRole := mediatree.RoleFrameVideo
	dataRole := mediatree.RoleFrameDataVideo
	timeRole := mediatree.RoleFrameTimeVideo
	if kind == KindAudio {
		frameRole = mediatree.RoleFrameAudio
		dataRole = mediatree.RoleFrameDataAudio
		timeRole = mediatree.RoleFrameTimeAudio
	}

	for _, fr := range frames {
		frameID := f.append(framesID, mediatree.TypeVoid, frameRole, nil)
		f.append(frameID, mediatree.TypeBytes, dataRole, fr.Data)
		f.append(frameID, mediatree.TypeTimestamp, timeRole, u64(fr.Time))
		if kind == KindVideo {
			f.append(frameID, mediatree.TypeUint8, mediatree.RoleFrameKind, []byte{fr.Kind})
		}
	}
	return nil
}

// Len returns the number of nodes appended so far.
func (f *Filler) Len() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.elems)
}

// Elements returns a snapshot of the tree built so far, safe to use after
// the Filler is done being written to (e.g. at fcontainer close). The
// returned slice is a copy; mutating it does not affect the Filler.
func (f *Filler) Elements() []mediatree.Element {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]mediatree.Element, len(f.elems))
	copy(out, f.elems)
	return out
}

// ElementsFrom returns a copy of the elements appended since index from (0
// initially), plus the new high-water index to pass next time. Elements
// are append-only, so this is O(new elements), never a full re-scan —
// backs Segment's (internal/storage) incremental push of newly-ready
// content to StorageEngine (ADR-017's periodic partial flush), which must
// encode only the tail, not re-encode the whole tree on every trigger.
func (f *Filler) ElementsFrom(from int) (tail []mediatree.Element, upto int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if from >= len(f.elems) {
		return nil, len(f.elems)
	}
	tail = make([]mediatree.Element, len(f.elems)-from)
	copy(tail, f.elems[from:])
	return tail, len(f.elems)
}

// Content encodes the current tree as Content bytes
// (docs/docs/archive/05-data-format.md §3). Intended for use once the
// fcontainer is closed (no further AddStreamParams/AddFrames calls).
func (f *Filler) Content() []byte {
	return mediatree.EncodeContent(f.Elements())
}

func u32(v uint32) []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, v)
	return b
}

func u64(v uint64) []byte {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, v)
	return b
}

func f64(v float64) []byte {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, math.Float64bits(v))
	return b
}
