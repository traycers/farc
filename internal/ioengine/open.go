package ioengine

import "os"

// Options selects and configures a Backend (ADR-010).
type Options struct {
	// Backend selects the implementation: "direct", "standard", or "" for
	// the platform default (direct on Linux, standard elsewhere). "direct"
	// is also selectable on Linux for debugging, filesystems without
	// O_DIRECT support, or side-by-side comparison against `standard` on
	// the same hardware.
	Backend string
	Flag    int         // os.OpenFile flags; 0 defaults to O_RDWR|O_CREATE
	Perm    os.FileMode // 0 defaults to 0600
}

func defaultFlagPerm(o Options) (int, os.FileMode) {
	flag := o.Flag
	if flag == 0 {
		flag = os.O_RDWR | os.O_CREATE
	}
	perm := o.Perm
	if perm == 0 {
		perm = 0o600
	}
	return flag, perm
}
