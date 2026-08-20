package storage

import "testing"

func TestUnit_PoolTuning_EchoesTheTuningItWasOpenedWith(t *testing.T) {
	dir := t.TempDir()
	u := initAndOpenWithPoolTuning(t, dir, smallGeometry(), PoolTuning{Size: 8, WarningAt: 4, BackpressureAt: 8})
	defer u.Close()

	if got := u.PoolTuning(); got != (PoolTuning{Size: 8, WarningAt: 4, BackpressureAt: 8}) {
		t.Fatalf("PoolTuning() = %+v, want {8 4 8}", got)
	}
}

func TestUnit_PoolTuning_ZeroValueResolvesToDefaults(t *testing.T) {
	dir := t.TempDir()
	u := initAndOpen(t, dir, smallGeometry(), "")
	defer u.Close()

	if got := u.PoolTuning(); got != DefaultPoolTuning() {
		t.Fatalf("PoolTuning() = %+v, want defaults %+v", got, DefaultPoolTuning())
	}
}
