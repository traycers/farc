package api

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("api: decode request body: %w", err)
	}
	return nil
}

// parseUUID parses a hex-encoded (no dashes) UUIDv4 path value, matching how
// storage.newUUIDv4 produces uuid.Bytes for on-disk storage.
func parseUUID(s string) ([16]byte, error) {
	var out [16]byte
	b, err := hex.DecodeString(s)
	if err != nil {
		return out, fmt.Errorf("api: invalid uuid %q: %w", s, err)
	}
	if len(b) != 16 {
		return out, fmt.Errorf("api: invalid uuid %q: want 16 bytes, got %d", s, len(b))
	}
	copy(out[:], b)
	return out, nil
}
