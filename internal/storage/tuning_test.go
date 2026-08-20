package storage

import "testing"

func TestPoolTuning_Resolved(t *testing.T) {
	cases := []struct {
		name   string
		tuning PoolTuning
		want   PoolTuning
	}{
		{"zero value resolves to defaults", PoolTuning{}, DefaultPoolTuning()},
		{"partial group -- only Size set", PoolTuning{Size: 8}, PoolTuning{Size: 8, WarningAt: 2, BackpressureAt: 4}},
		{"fully specified is left as-is", PoolTuning{Size: 8, WarningAt: 4, BackpressureAt: 8}, PoolTuning{Size: 8, WarningAt: 4, BackpressureAt: 8}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.tuning.Resolved(); got != c.want {
				t.Fatalf("Resolved(%+v) = %+v, want %+v", c.tuning, got, c.want)
			}
		})
	}
}

func TestPoolTuning_Validate(t *testing.T) {
	cases := []struct {
		name    string
		tuning  PoolTuning
		wantErr bool
	}{
		{"zero value resolves to defaults, valid", PoolTuning{}, false},
		{"explicit valid ordering", PoolTuning{Size: 8, WarningAt: 4, BackpressureAt: 8}, false},
		{"warning equals backpressure equals size, valid", PoolTuning{Size: 4, WarningAt: 4, BackpressureAt: 4}, false},
		{"warning_at below 1 rejected", PoolTuning{Size: 4, WarningAt: -1, BackpressureAt: 4}, true},
		{"backpressure_at below warning_at rejected", PoolTuning{Size: 8, WarningAt: 6, BackpressureAt: 4}, true},
		{"size below backpressure_at rejected", PoolTuning{Size: 4, WarningAt: 2, BackpressureAt: 8}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.tuning.Validate()
			if c.wantErr && err == nil {
				t.Fatalf("Validate(%+v) = nil, want error", c.tuning)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("Validate(%+v) = %v, want nil", c.tuning, err)
			}
		})
	}
}
