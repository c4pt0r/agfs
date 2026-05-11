package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// DefaultMaxRequestBodyBytes caps request bodies accepted by write and JSON
// endpoints. The limit is deliberately conservative until large writes have a
// true streaming path through every backend.
const DefaultMaxRequestBodyBytes int64 = 64 * 1024 * 1024

func normalizeMaxRequestBodyBytes(maxBytes int64) int64 {
	if maxBytes <= 0 {
		return DefaultMaxRequestBodyBytes
	}
	return maxBytes
}

func requestBodyTooLargeMessage(maxBytes int64) string {
	return fmt.Sprintf("request body exceeds maximum size of %d bytes", maxBytes)
}

func readLimitedRequestBody(w http.ResponseWriter, r *http.Request, maxBytes int64) ([]byte, error) {
	return io.ReadAll(http.MaxBytesReader(w, r.Body, normalizeMaxRequestBodyBytes(maxBytes)))
}

func decodeLimitedJSON(w http.ResponseWriter, r *http.Request, maxBytes int64, dst interface{}) error {
	data, err := readLimitedRequestBody(w, r, maxBytes)
	if err != nil {
		return err
	}
	return json.NewDecoder(bytes.NewReader(data)).Decode(dst)
}

func isRequestBodyTooLarge(err error) bool {
	var maxBytesErr *http.MaxBytesError
	return errors.As(err, &maxBytesErr)
}

func writeRequestBodyError(w http.ResponseWriter, err error, maxBytes int64, invalidMessage string) {
	if isRequestBodyTooLarge(err) {
		writeError(w, http.StatusRequestEntityTooLarge, requestBodyTooLargeMessage(normalizeMaxRequestBodyBytes(maxBytes)))
		return
	}
	writeError(w, http.StatusBadRequest, invalidMessage)
}

// SetMaxRequestBodyBytes sets the maximum accepted request body size in bytes.
// Values <= 0 reset the handler to DefaultMaxRequestBodyBytes.
func (h *Handler) SetMaxRequestBodyBytes(maxBytes int64) {
	h.maxRequestBodyBytes = normalizeMaxRequestBodyBytes(maxBytes)
}

// MaxRequestBodyBytes returns the effective request body size limit in bytes.
func (h *Handler) MaxRequestBodyBytes() int64 {
	return h.maxRequestBodyBytes
}

// SetMaxRequestBodyBytes sets the maximum accepted request body size in bytes.
// Values <= 0 reset the plugin handler to DefaultMaxRequestBodyBytes.
func (ph *PluginHandler) SetMaxRequestBodyBytes(maxBytes int64) {
	ph.maxRequestBodyBytes = normalizeMaxRequestBodyBytes(maxBytes)
}

// MaxRequestBodyBytes returns the effective request body size limit in bytes.
func (ph *PluginHandler) MaxRequestBodyBytes() int64 {
	return ph.maxRequestBodyBytes
}
