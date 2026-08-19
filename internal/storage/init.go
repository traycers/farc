package storage

import (
	"errors"
	"fmt"

	"github.com/traycers/farc/fblock"
	"github.com/traycers/farc/internal/ioengine"
	"github.com/traycers/farc/internal/storageengine"
)

// ErrAlreadyInitialized is step 1's "already initialized" refusal
// (docs/docs/archive/04-storage-operations.md §3.1).
var ErrAlreadyInitialized = errors.New("storage: already initialized (valid magic_prolog present); pass Force to reinitialize")

// InitConfig configures a one-time Storage initialization.
type InitConfig struct {
	Geometry Geometry
	Params   fblock.Params
	Now      uint64 // Unix ns, used for catalog_time

	// Force bypasses the step-1 emptiness check (an explicit operator
	// parameter per the docs, not a default).
	Force bool

	// CatalogPath, if non-empty, creates the SSD catalog mirror (ADR-007)
	// alongside the main write.
	CatalogPath string

	Tuning EngineTuning
}

// Init performs the Storage initialization algorithm end to end: step 1
// (emptiness check), steps 3-7 (geometry/share check, fblock 0 assembly and
// write-verify, optional SSD catalog). Step 2 (file/device sizing) is the
// caller's job — e.g. CreateSizedFile before opening backend — since it
// differs between file and block-device targets.
func Init(backend ioengine.Backend, cfg InitConfig) error {
	if !cfg.Force {
		probe := make([]byte, 8)
		_, err := backend.ReadAt(probe, 0)
		if err != nil {
			return fmt.Errorf("storage: init: probe read: %w", err)
		}
		if fblock.HasValidMagicProlog(probe) {
			return ErrAlreadyInitialized
		}
	}

	if cfg.Geometry.N == 0 {
		return errors.New("storage: init: N (fblock count) must be positive")
	}
	if cfg.Geometry.FblockSize == 0 {
		return errors.New("storage: init: FblockSize must be positive")
	}

	catalogSize := fblock.CatalogSize(cfg.Geometry.MaxChannels, cfg.Geometry.N)
	paramsBuf, err := fblock.EncodeParams(cfg.Params)
	if err != nil {
		return fmt.Errorf("storage: init: encode params: %w", err)
	}
	err = fblock.CheckMinContainerShare(cfg.Geometry.FblockSize, uint32(len(paramsBuf)), catalogSize, cfg.Params.MinContainerShare, backend.Alignment())
	if err != nil {
		return fmt.Errorf("storage: init: %w", err)
	}

	// Step 4: fblock 0 in memory. Index 0 stays Uninitialized in its own
	// snapshot, even though it's the fblock this call physically writes:
	// the bootstrap write only exists to persist a valid prolog/catalog so
	// the disk is readable at all, it carries no real fcontainer (content/
	// toc both absent, see this package's doc comment on why that's still
	// a full fblock_size write), and it must never be treated as "real"
	// content by SelectNextIndex/the write cursor — see docs/docs/archive/
	// 04-storage-operations.md §3.1 step 4 and §6.1.
	cat := fblock.NewCatalog(cfg.Geometry.MaxChannels, cfg.Geometry.N)

	h := &fblock.Header{
		Prolog: fblock.FixedProlog{
			FormatVersionMajor: 1,
			FormatVersionMinor: 0,
			MaxChannels:        cfg.Geometry.MaxChannels,
			WriteSequence:      1,
			CatalogTime:        cfg.Now,
			FblockSize:         cfg.Geometry.FblockSize,
		},
		Params:  cfg.Params,
		Catalog: cat,
	}
	buf, err := assembleFblock(h, nil, nil, backend.Alignment())
	if err != nil {
		return fmt.Errorf("storage: init: assemble fblock 0: %w", err)
	}

	// Step 5: write with verify. A throwaway Engine is enough for this one
	// bootstrap write — Init runs before any Recorder/Reader exists.
	eng := storageengine.New(backend, engineConfig(cfg.Params.FchunkSize, cfg.Params.ReadChunkSize, cfg.Tuning))
	ticket := eng.EnqueueWrite(0, buf)
	for eng.Step() {
	}
	res, werr := ticket.Wait()
	if werr != nil {
		return fmt.Errorf("storage: init: write fblock 0: %w", werr)
	}
	if res.Corrupted {
		return fmt.Errorf("storage: init: write-verify failed for fblock 0 at offset %d", res.FailedOffset)
	}

	// Step 6: optional SSD catalog — must mirror the main disk exactly, so
	// index 0 stays Uninitialized here too (cat is cloned as-is, no
	// override).
	if cfg.CatalogPath != "" {
		ssd := cat.Clone()
		meta := SSDCatalogMeta{WriteSequence: 1, CatalogTime: cfg.Now, Cursor: 0}
		err := SaveSSDCatalog(cfg.CatalogPath, ssd, meta)
		if err != nil {
			return fmt.Errorf("storage: init: create SSD catalog: %w", err)
		}
	}

	return nil
}
