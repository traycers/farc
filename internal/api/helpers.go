package api

import (
	"encoding/hex"
	"encoding/json"
	"errors"
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

// apiError pairs an error with the HTTP status it should produce. Handlers
// that call into a shared non-HTTP helper (createStorage/createChannel/
// removeChannel and archives.go's batch operations built on top of them)
// use this so the helper can request a specific status (400/404/409) instead
// of every caller hard-coding the same default.
type apiError struct {
	status int
	err    error
}

func (e *apiError) Error() string { return e.err.Error() }
func (e *apiError) Unwrap() error { return e.err }

func apiErr(status int, err error) error { return &apiError{status: status, err: err} }

// writeAPIError writes err in this package's usual {"error":"..."} shape,
// using the status apiErr wrapped it with if any, else defaultStatus.
func writeAPIError(w http.ResponseWriter, err error, defaultStatus int) {
	var ae *apiError
	if errors.As(err, &ae) {
		writeError(w, ae.status, ae.err)
		return
	}
	writeError(w, defaultStatus, err)
}

func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	err := dec.Decode(v)
	if err != nil {
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
