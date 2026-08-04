package hlsapi

import (
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
)

func writeError(w http.ResponseWriter, status int, err error) {
	http.Error(w, err.Error(), status)
}

func parseUint16(s string) (uint16, error) {
	v, err := strconv.ParseUint(s, 10, 16)
	if err != nil {
		return 0, fmt.Errorf("hlsapi: invalid value %q: %w", s, err)
	}
	return uint16(v), nil
}

func parseUint64(s string) (uint64, error) {
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("hlsapi: invalid value %q: %w", s, err)
	}
	return v, nil
}

func parseInt(s string) (int, error) {
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("hlsapi: invalid value %q: %w", s, err)
	}
	return v, nil
}

// parseUUID parses a hex-encoded (no dashes) UUIDv4 path value, matching
// internal/api's own convention for the same wire format.
func parseUUID(s string) ([16]byte, error) {
	var out [16]byte
	b, err := hex.DecodeString(s)
	if err != nil {
		return out, fmt.Errorf("hlsapi: invalid uuid %q: %w", s, err)
	}
	if len(b) != 16 {
		return out, fmt.Errorf("hlsapi: invalid uuid %q: want 16 bytes, got %d", s, len(b))
	}
	copy(out[:], b)
	return out, nil
}
