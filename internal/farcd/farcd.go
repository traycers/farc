// Package farcd is Phase 11's process wiring: load config -> open every
// configured Storage -> build IngestManager (one ChannelIngest per
// configured channel) -> wire the StorageUnit -> CapturePolicy backpressure
// signal -> start HttpApiServer/EventPushServer/MetricsEndpoint as three
// separate listeners (docs/docs/archive/04-storage-operations.md §2.1: "это
// разные серверы") -> graceful shutdown on context cancellation.
//
// farcd never runs Initializer. internal/config's own doc comment explains
// why: the config file's storages list carries only id/path/catalog_path,
// no geometry or write_mode/retention.days -- those are set once at init
// time (Phase 10's `POST /storages`, which registers into a *running*
// process's StorageRegistry) and thereafter live in the Storage's own
// on-disk header (§2.2). A Storage only belongs in this file *after* it has
// already been initialized once; farcd's job at startup is exclusively to
// Open (docs/docs/archive/04-storage-operations.md §4-5's Startup
// algorithm, already implemented as storage.Open) every Storage the config
// names.
//
// No JobRunner exists here (or anywhere in this codebase) -- v1 scope
// deliberately excludes GeometryManager/Importer (and therefore any
// orchestrator over them), matching the same decision already reflected in
// Phase 8's storage.Init/Open and Phase 10's HttpApiServer.
package farcd

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"traycers/farc/internal/api"
	"traycers/farc/internal/config"
	"traycers/farc/internal/ingest"
	"traycers/farc/internal/ioengine"
	"traycers/farc/internal/storage"
	"traycers/farc/internal/storageengine"
)

// rtspTimeout is ChannelIngest's RTSP read/write timeout. Not part of the
// documented config schema (04-storage-operations.md §2.1's channel entry
// has no timeout fields) -- a fixed v1 default rather than an undocumented
// config addition.
const rtspTimeout = 10 * time.Second

// shutdownTimeout bounds how long graceful HTTP shutdown waits for
// in-flight requests before Run returns anyway.
const shutdownTimeout = 10 * time.Second

// readHeaderTimeout bounds how long each http.Server waits for a client to
// finish sending request headers, closing the connection past that point
// (mitigates Slowloris-style slow-header-write attacks; net/http.Server has
// no timeout by default).
const readHeaderTimeout = 10 * time.Second

// Farcd is one running farcd process: every open Storage, the
// IngestManager driving all configured channels, and the three servers
// (docs/docs/archive/11-service-composition.md §5.1.2-5.1.4).
type Farcd struct {
	units    []*storage.Unit
	registry *api.StorageRegistry
	ing      *ingest.IngestManager
	channels []ingest.ChannelConfig

	// cfg/configPath/cfgMu back persistNewStorage: a storage created at
	// runtime via POST /storages only lives in registry's in-memory map
	// unless it's also appended here and saved back to configPath (PLAN.md's
	// Gap 3) -- cfgMu serializes concurrent creates' read-modify-write of
	// cfg.Storages plus the file write.
	cfg        *config.Config
	configPath string
	cfgMu      sync.Mutex

	// push publishes journal events (api.JournalEvent) to any "global"
	// /events/ws subscriber -- internal/hlsd's reconciliation loop is the
	// intended consumer of channel.created/channel.removed specifically,
	// kept in sync with farcd's live channel list without polling
	// GET /channels on every change; the /journal UI page consumes the
	// full event vocabulary.
	push *api.EventPushServer

	// bridgeStops cancels each bridgeFblockEvents goroutine (one per open
	// Storage, forwarding its NotificationBus into push as fblock.created/
	// fblock.deleted), keyed by storage id so persistRemovedStorage can stop
	// exactly one of them without touching the rest. bridgeMu guards
	// inserts/deletes from persistNewStorage/persistRemovedStorage, which can
	// run concurrently with other requests.
	bridgeStops map[string]func()
	bridgeMu    sync.Mutex

	// liveCursors tracks, per currently-recording channel, how many of its
	// live segment's tree elements have already been published via
	// PublishLiveProgress (the fblock-live page's WS feed) -- populated on
	// SetOnRecordingChange(recording=true), advanced by
	// runLiveProgressTicker, dropped on recording=false. No final flush is
	// needed on stop: the segment's own fblock.ready (published moments
	// later by bridgeFblockEvents once WriteFcontainer finishes) carries the
	// complete, authoritative tree via GET .../tree, which is what the
	// fblock-live page switches to on that event anyway.
	liveCursors map[uint16]int
	liveMu      sync.Mutex

	httpSrv    *http.Server
	wsSrv      *http.Server
	metricsSrv *http.Server

	logf func(format string, args ...any)
}

// New opens every Storage cfg names and builds the channel configuration
// for IngestManager, but starts nothing yet -- call Run to actually start
// serving. On error, every Storage opened so far is closed before
// returning, so a caller never has to clean up a partial Farcd itself.
// configPath is the file cfg was loaded from -- kept so a storage created
// later via POST /storages can be persisted back into it (persistNewStorage).
func New(cfg *config.Config, configPath string) (*Farcd, error) {
	f := &Farcd{
		registry:    api.NewStorageRegistry(),
		ing:         ingest.NewIngestManager(),
		cfg:         cfg,
		configPath:  configPath,
		bridgeStops: make(map[string]func()),
		liveCursors: make(map[uint16]int),
		logf:        func(string, ...any) {},
	}

	push := api.NewEventPushServer(f.registry)
	f.push = push

	for _, sc := range cfg.Storages {
		unit, err := openStorage(sc)
		if err != nil {
			f.closeUnits()
			return nil, fmt.Errorf("farcd: open storage %q: %w", sc.ID, err)
		}
		f.units = append(f.units, unit)
		err = f.registry.Register(sc.ID, unit, sc.Path, sc.Name)
		if err != nil {
			f.closeUnits()
			return nil, fmt.Errorf("farcd: register storage %q: %w", sc.ID, err)
		}
		f.bridgeFblockEvents(sc.ID, unit)
	}

	for _, cc := range cfg.Channels {
		chCfg, err := f.buildChannelConfig(cc)
		if err != nil {
			f.closeUnits()
			return nil, err
		}
		f.channels = append(f.channels, chCfg)
	}

	f.ing.SetOnRecordingChange(func(channel uint16, recording bool, t uint64) {
		// StorageOf, not List: this fires with the channel's own
		// CapturePolicy mutex already held, and List's Policy() call (or
		// LiveElementsSince, which locks the same mutex) would self-deadlock
		// on it (see StorageOf's own doc comment). msm_server needs Storage
		// (its "archive id") to call started_add/finished_add.
		storageID, _ := f.ing.StorageOf(channel)

		f.liveMu.Lock()
		if recording {
			f.liveCursors[channel] = 0
		} else {
			delete(f.liveCursors, channel)
		}
		f.liveMu.Unlock()

		if recording {
			f.push.Publish(api.JournalEvent{Name: api.EventRecordingStarted, Channel: channel, Storage: storageID, Begin: t})
			return
		}
		f.push.Publish(api.JournalEvent{Name: api.EventRecordingStopped, Channel: channel, Storage: storageID, End: t})
	})

	// push IS passed into NewHttpApiServer (unlike WS's own listener on
	// cfg.WS below, matching §2.1's "разные серверы" for actually *serving*
	// /events/ws) so channels.go's command handlers (trigger/start/stop
	// recording) can publish directly, without a farcd-side hook -- unlike
	// channel create/update/remove, those commands persist nothing to
	// config, so there's no need to route their publish through farcd.
	apiServer := api.NewHttpApiServer(f.registry, f.ing, push)
	apiServer.SetOnStorageCreated(f.persistNewStorage)
	apiServer.SetOnStorageUpdated(f.persistUpdatedStorage)
	apiServer.SetOnStorageRemoved(f.persistRemovedStorage)
	apiServer.SetOnChannelCreated(f.persistNewChannel)
	apiServer.SetOnChannelUpdated(f.persistUpdatedChannel)
	apiServer.SetOnChannelRemoved(f.persistRemovedChannel)

	f.httpSrv = &http.Server{Addr: cfg.HTTP.String(), Handler: apiServer.Handler(), ReadHeaderTimeout: readHeaderTimeout}
	f.wsSrv = &http.Server{Addr: cfg.WS.String(), Handler: push, ReadHeaderTimeout: readHeaderTimeout}
	f.metricsSrv = &http.Server{Addr: cfg.Metrics.String(), Handler: apiServer.MetricsHandler(), ReadHeaderTimeout: readHeaderTimeout}

	return f, nil
}

// SetLogger sets a callback for non-fatal diagnostics, forwarded to
// IngestManager and used for this package's own startup/shutdown logging.
func (f *Farcd) SetLogger(logf func(format string, args ...any)) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	f.logf = logf
	f.ing.SetLogger(logf)
}

func openStorage(sc config.Storage) (*storage.Unit, error) {
	backend, err := ioengine.Open(sc.Path, ioengine.Options{})
	if err != nil {
		return nil, err
	}
	unit, err := storage.Open(storage.OpenConfig{
		Backend:     backend,
		CatalogPath: sc.CatalogPath,
		Tuning:      storage.DefaultEngineTuning(),
	})
	if err != nil {
		_ = backend.Close()
		return nil, err
	}
	return unit, nil
}

// buildChannelConfig resolves cc.Storage to its already-open Unit and
// translates the config-file capture_policy shape into internal/ingest's
// runtime one. cc.Storage is guaranteed to name a Storage in cfg.Storages
// (internal/config.Load already validated this at parse time), and
// cc.Storage must therefore already be registered by the time this runs
// (New opens all storages before building any channel config).
func (f *Farcd) buildChannelConfig(cc config.Channel) (ingest.ChannelConfig, error) {
	unit, ok := f.registry.Get(cc.Storage)
	if !ok {
		return ingest.ChannelConfig{}, fmt.Errorf("farcd: channel %d: storage %q not open", cc.ID, cc.Storage)
	}

	var policyType ingest.PolicyType
	var queueDepth uint64
	switch cc.CapturePolicy.Type {
	case config.CapturePolicyContinuous:
		policyType = ingest.PolicyContinuous
		queueDepth = uint64(cc.CapturePolicy.MaxDeferredStart.Duration().Nanoseconds())
	case config.CapturePolicyEvent:
		policyType = ingest.PolicyEvent
		queueDepth = uint64(cc.CapturePolicy.Prerecord.Duration().Nanoseconds())
	default:
		// internal/config.Load already rejects anything else (including
		// "schedule") at parse time -- reaching here would be this
		// package's own bug, not a bad config file.
		return ingest.ChannelConfig{}, fmt.Errorf("farcd: channel %d: unhandled capture_policy.type %q", cc.ID, cc.CapturePolicy.Type)
	}

	return ingest.ChannelConfig{
		Channel:    cc.ID,
		RTSPURL:    cc.RTSPURL,
		StorageID:  cc.Storage,
		Recorder:   unit,
		QueueDepth: queueDepth,
		PolicyType: policyType,
		PolicyParams: ingest.PolicyParams{
			Prerecord:  uint64(cc.CapturePolicy.Prerecord.Duration().Nanoseconds()),
			Postrecord: uint64(cc.CapturePolicy.Postrecord.Duration().Nanoseconds()),
		},
		ReadTimeout:  rtspTimeout,
		WriteTimeout: rtspTimeout,
		Name:         cc.Name,
		// StorageUnit -> CapturePolicy backpressure signal (10-capture-
		// policy.md §8, resolved in PLAN.md's gap-resolutions section):
		// polled live off unit.EngineLevel() rather than tracked via a
		// separate atomic flag updated on transitions -- Level() is
		// already a cheap, mutex-guarded read, so there's nothing to cache.
		BackpressureSignal: func() bool { return unit.EngineLevel() == storageengine.LevelBackpressure },
	}, nil
}

// persistNewStorage appends a storage created via POST /storages to farcd's
// own config file, wired into HttpApiServer via SetOnStorageCreated -- the
// only reason a runtime-created storage survives farcd's next restart
// (config.Load is the only thing New's own storage-opening loop ever reads;
// it never Inits, see this package's own doc comment). If Save fails, the
// in-memory append is rolled back so cfg still matches what's on disk.
func (f *Farcd) persistNewStorage(id, path, catalogPath, name string) error {
	f.cfgMu.Lock()
	defer f.cfgMu.Unlock()

	for _, s := range f.cfg.Storages {
		if s.ID == id {
			return nil
		}
	}
	f.cfg.Storages = append(f.cfg.Storages, config.Storage{ID: id, Path: path, CatalogPath: catalogPath, Name: name})
	err := config.Save(f.configPath, f.cfg)
	if err != nil {
		f.cfg.Storages = f.cfg.Storages[:len(f.cfg.Storages)-1]
		return fmt.Errorf("farcd: persist storage %q to %s: %w", id, f.configPath, err)
	}
	if unit, ok := f.registry.Get(id); ok {
		f.bridgeFblockEvents(id, unit)
	}
	return nil
}

// persistUpdatedStorage renames an existing storage's config entry (PATCH
// /storages/{id} with a name field), wired into HttpApiServer via
// SetOnStorageUpdated -- mirrors persistNewStorage's role, rolling back the
// in-memory rename if Save fails.
func (f *Farcd) persistUpdatedStorage(id, name string) error {
	f.cfgMu.Lock()
	defer f.cfgMu.Unlock()

	idx := -1
	for i, s := range f.cfg.Storages {
		if s.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("farcd: persist storage %q: not present in config", id)
	}
	old := f.cfg.Storages[idx].Name
	f.cfg.Storages[idx].Name = name
	err := config.Save(f.configPath, f.cfg)
	if err != nil {
		f.cfg.Storages[idx].Name = old
		return fmt.Errorf("farcd: persist storage %q rename to %s: %w", id, f.configPath, err)
	}
	return nil
}

// persistRemovedStorage removes a storage detached via archives.go's
// archives_detach from farcd's own config file, wired into HttpApiServer via
// SetOnStorageRemoved -- runs *before* the Storage is actually unregistered
// and closed (api.HttpApiServer.SetOnStorageRemoved's own doc comment), so a
// Save failure here leaves the archive fully intact rather than half torn
// down. On success, stops id's fblock-event bridge goroutine and drops its
// Unit from f.units, so shutdown's closeUnits doesn't later double-close a
// Unit archives.go's handler is about to Close itself.
func (f *Farcd) persistRemovedStorage(id string) error {
	f.cfgMu.Lock()
	defer f.cfgMu.Unlock()

	idx := -1
	for i, sc := range f.cfg.Storages {
		if sc.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("farcd: persist archive %q detach: not present in config", id)
	}
	removed := f.cfg.Storages[idx]
	f.cfg.Storages = append(f.cfg.Storages[:idx], f.cfg.Storages[idx+1:]...)
	err := config.Save(f.configPath, f.cfg)
	if err != nil {
		restored := make([]config.Storage, 0, len(f.cfg.Storages)+1)
		restored = append(restored, f.cfg.Storages[:idx]...)
		restored = append(restored, removed)
		restored = append(restored, f.cfg.Storages[idx:]...)
		f.cfg.Storages = restored
		return fmt.Errorf("farcd: persist archive %q detach to %s: %w", id, f.configPath, err)
	}

	f.stopBridge(id)
	if unit, ok := f.registry.Get(id); ok {
		for i, u := range f.units {
			if u == unit {
				f.units = append(f.units[:i], f.units[i+1:]...)
				break
			}
		}
	}
	return nil
}

// specToConfigChannel translates api.ChannelSpec (the HTTP wire's ns-based
// shape) into config.Channel/CapturePolicy (the config file's Go-duration-
// string shape) -- the inverse of buildChannelConfig's job.
func specToConfigChannel(spec api.ChannelSpec) config.Channel {
	return config.Channel{
		ID:      spec.ID,
		RTSPURL: spec.RTSPURL,
		Storage: spec.Storage,
		CapturePolicy: config.CapturePolicy{
			Type:             spec.PolicyType,
			MaxDeferredStart: config.Duration(spec.MaxDeferredStartNS),
			Prerecord:        config.Duration(spec.PrerecordNS),
			Postrecord:       config.Duration(spec.PostrecordNS),
		},
		Name: spec.Name,
	}
}

// persistNewChannel appends a channel created via POST /channels to farcd's
// own config file, wired into HttpApiServer via SetOnChannelCreated --
// otherwise it would only ever exist in IngestManager's in-memory map and
// be gone on the next restart, same gap POST /storages had before
// persistNewStorage (PLAN.md's Gap 3, now also closed for channels). If
// Save fails, the in-memory append is rolled back so cfg still matches
// what's on disk. On success, publishes api.EventChannelCreated so any
// "global" /events/ws subscriber (internal/hlsd's reconciliation loop) picks
// the channel up without waiting for its next GET /channels re-list.
func (f *Farcd) persistNewChannel(spec api.ChannelSpec) error {
	f.cfgMu.Lock()
	defer f.cfgMu.Unlock()

	for _, c := range f.cfg.Channels {
		if c.ID == spec.ID {
			return nil
		}
	}
	f.cfg.Channels = append(f.cfg.Channels, specToConfigChannel(spec))
	err := config.Save(f.configPath, f.cfg)
	if err != nil {
		f.cfg.Channels = f.cfg.Channels[:len(f.cfg.Channels)-1]
		return fmt.Errorf("farcd: persist channel %d to %s: %w", spec.ID, f.configPath, err)
	}
	f.push.Publish(api.JournalEvent{Name: api.EventChannelCreated, Channel: spec.ID, Storage: spec.Storage})
	return nil
}

// persistUpdatedChannel replaces an existing channel's config entry
// (PUT /channels/{id}), rolling back to the previous entry if Save fails.
// On success, if the channel's storage actually changed, publishes
// api.EventChannelRemoved (old storage) then api.EventChannelCreated (new
// storage) -- there's no separate "channel updated" event; a storage move
// is exactly the same transition a remove-then-create would produce, and a
// PUT that only edits rtsp_url/capture_policy (storage unchanged) publishes
// nothing, since nothing a subscriber cares about actually moved.
func (f *Farcd) persistUpdatedChannel(spec api.ChannelSpec) error {
	f.cfgMu.Lock()
	defer f.cfgMu.Unlock()

	idx := -1
	for i, c := range f.cfg.Channels {
		if c.ID == spec.ID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("farcd: persist channel %d: not present in config", spec.ID)
	}
	old := f.cfg.Channels[idx]
	f.cfg.Channels[idx] = specToConfigChannel(spec)
	err := config.Save(f.configPath, f.cfg)
	if err != nil {
		f.cfg.Channels[idx] = old
		return fmt.Errorf("farcd: persist channel %d to %s: %w", spec.ID, f.configPath, err)
	}
	if old.Storage != spec.Storage {
		f.push.Publish(api.JournalEvent{Name: api.EventChannelRemoved, Channel: spec.ID, Storage: old.Storage})
		f.push.Publish(api.JournalEvent{Name: api.EventChannelCreated, Channel: spec.ID, Storage: spec.Storage})
	}
	return nil
}

// persistRemovedChannel removes a channel's config entry (DELETE
// /channels/{id}), restoring it at the same index if Save fails. On
// success, publishes api.EventChannelRemoved (see persistNewChannel).
func (f *Farcd) persistRemovedChannel(id uint16) error {
	f.cfgMu.Lock()
	defer f.cfgMu.Unlock()

	idx := -1
	for i, c := range f.cfg.Channels {
		if c.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil
	}
	removed := f.cfg.Channels[idx]
	f.cfg.Channels = append(f.cfg.Channels[:idx], f.cfg.Channels[idx+1:]...)
	err := config.Save(f.configPath, f.cfg)
	if err != nil {
		restored := make([]config.Channel, 0, len(f.cfg.Channels)+1)
		restored = append(restored, f.cfg.Channels[:idx]...)
		restored = append(restored, removed)
		restored = append(restored, f.cfg.Channels[idx:]...)
		f.cfg.Channels = restored
		return fmt.Errorf("farcd: persist removal of channel %d from %s: %w", id, f.configPath, err)
	}
	f.push.Publish(api.JournalEvent{Name: api.EventChannelRemoved, Channel: id, Storage: removed.Storage})
	return nil
}

// bridgeFblockEvents subscribes to unit's NotificationBus and forwards
// fblock.write.started/fblock.deleted into f.push as
// api.EventFblockCreated/api.EventFblockDeleted, so a /journal subscriber
// (a "global" /events/ws client) sees fblock lifecycle without also needing
// a per-storage subscription. Runs until closeUnits calls the returned stop
// func (stored in f.bridgeStops).
func (f *Farcd) bridgeFblockEvents(id string, unit *storage.Unit) {
	events := unit.Notify().Subscribe(64)
	stop := make(chan struct{})

	f.bridgeMu.Lock()
	f.bridgeStops[id] = func() {
		close(stop)
		unit.Notify().Unsubscribe(events)
	}
	f.bridgeMu.Unlock()

	go func() {
		for {
			select {
			case <-stop:
				return
			case ev, ok := <-events:
				if !ok {
					return
				}
				var name string
				switch ev.Name {
				case storage.EventFblockWriteStarted:
					name = api.EventFblockCreated
				case storage.EventFblockWriteCompleted:
					name = api.EventFblockReady
				case storage.EventFblockDeleted:
					name = api.EventFblockDeleted
				default:
					continue
				}
				uuid := ""
				if ev.UUID != ([16]byte{}) {
					uuid = hex.EncodeToString(ev.UUID[:])
				}
				journalEv := api.JournalEvent{
					Name: name, Storage: id, Index: ev.Index, UUID: uuid,
					Severity: ev.Severity, Reason: ev.Reason,
				}
				if name == api.EventFblockReady {
					snap := unit.Index().Snapshot()
					if ev.Index < snap.N {
						journalEv.Begin, journalEv.End = snap.Begin[ev.Index], snap.End[ev.Index]
					}
				}
				f.push.Publish(journalEv)
			}
		}
	}()
}

// liveProgressInterval is how often runLiveProgressTicker polls each
// recording channel's Filler for newly-appended tree nodes -- frequent
// enough that the fblock-live page feels live, coarse enough that even a
// high-fps stream's per-tick delta stays a small WS frame (see
// docs/docs/archive/08-array-trees.md §3.4 on why individual frames are
// never pushed one at a time).
const liveProgressInterval = time.Second

// runLiveProgressTicker drives the fblock-live page's WS feed: once a
// second, for every channel currently recording (per liveCursors), pull
// whatever's been appended to its Filler since the last tick and publish it.
// Runs until ctx is cancelled; like bridgeFblockEvents's goroutines, Run
// doesn't wait for it to actually exit at shutdown.
func (f *Farcd) runLiveProgressTicker(ctx context.Context) {
	ticker := time.NewTicker(liveProgressInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			f.tickLiveProgress()
		}
	}
}

func (f *Farcd) tickLiveProgress() {
	f.liveMu.Lock()
	channels := make([]uint16, 0, len(f.liveCursors))
	for ch := range f.liveCursors {
		channels = append(channels, ch)
	}
	f.liveMu.Unlock()

	for _, channel := range channels {
		f.liveMu.Lock()
		cursor := f.liveCursors[channel]
		f.liveMu.Unlock()

		elems, total, contentBytes, ok := f.ing.LiveElementsSince(channel, cursor)
		if !ok || len(elems) == 0 {
			continue
		}
		storageID, _ := f.ing.StorageOf(channel)
		f.push.PublishLiveProgress(storageID, channel, total, contentBytes, elems, uint32(cursor))

		f.liveMu.Lock()
		// Only advance if the channel is still recording -- it may have
		// stopped (and been deleted from liveCursors) between this tick's
		// snapshot above and now, in which case leave the map alone rather
		// than resurrecting a now-meaningless entry.
		if _, stillRecording := f.liveCursors[channel]; stillRecording {
			f.liveCursors[channel] = total
		}
		f.liveMu.Unlock()
	}
}

func (f *Farcd) closeUnits() {
	f.bridgeMu.Lock()
	stops := f.bridgeStops
	f.bridgeStops = make(map[string]func())
	f.bridgeMu.Unlock()
	for _, stop := range stops {
		stop()
	}

	for _, u := range f.units {
		_ = u.Close()
	}
	f.units = nil
}

// stopBridge stops and forgets id's fblock-event bridge goroutine, if any --
// persistRemovedStorage's job on successful detach. A no-op if id has no
// bridge (shouldn't happen for a registered Storage, but harmless either way).
func (f *Farcd) stopBridge(id string) {
	f.bridgeMu.Lock()
	stop, ok := f.bridgeStops[id]
	if ok {
		delete(f.bridgeStops, id)
	}
	f.bridgeMu.Unlock()
	if ok {
		stop()
	}
}

// Run starts IngestManager and all three servers, then blocks until ctx is
// cancelled, at which point it shuts everything down gracefully and
// returns. A listener failing to start (e.g. port already in use) also
// triggers shutdown and is returned as this call's error.
func (f *Farcd) Run(ctx context.Context) error {
	f.ing.Start(f.channels) //nolint:contextcheck // IngestManager owns its own goroutine lifecycle via Start/Stop, not ctx propagation -- Stop() is what f.shutdown() calls below

	errCh := make(chan error, 3)
	serve := func(name string, srv *http.Server) {
		err := srv.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("farcd: %s server: %w", name, err)
			return
		}
		errCh <- nil
	}
	go serve("http", f.httpSrv)
	go serve("ws", f.wsSrv)
	go serve("metrics", f.metricsSrv)
	go f.runLiveProgressTicker(ctx)

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-errCh:
	}

	f.shutdown() //nolint:contextcheck // deliberate: ctx is already Done() here, so shutdown builds its own fresh timeout context rather than reusing a cancelled one
	return runErr
}

func (f *Farcd) shutdown() {
	f.ing.Stop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	for _, srv := range []*http.Server{f.httpSrv, f.wsSrv, f.metricsSrv} {
		err := srv.Shutdown(shutdownCtx)
		if err != nil {
			f.logf("farcd: server shutdown: %v", err)
		}
	}

	f.closeUnits()
}
