//go:build linux

package ioengine

import "fmt"

// Open opens path with the backend selected by opts (default: direct).
func Open(path string, opts Options) (Backend, error) {
	flag, perm := defaultFlagPerm(opts)
	switch opts.Backend {
	case "", "direct":
		return OpenDirect(path, flag, perm)
	case "standard":
		return OpenStandard(path, flag, perm)
	default:
		return nil, fmt.Errorf("ioengine: unknown backend %q", opts.Backend)
	}
}
