//go:build !linux

package ioengine

import "fmt"

// Open opens path with the backend selected by opts (default: standard —
// `direct` is Linux-only, ADR-010).
func Open(path string, opts Options) (Backend, error) {
	flag, perm := defaultFlagPerm(opts)
	switch opts.Backend {
	case "", "standard":
		return OpenStandard(path, flag, perm)
	case "direct":
		return nil, fmt.Errorf("ioengine: direct backend not supported on this platform")
	default:
		return nil, fmt.Errorf("ioengine: unknown backend %q", opts.Backend)
	}
}
