package handlers

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/c4pt0r/agfs/agfs-server/pkg/filesystem"
)

// HandleOpenRequest represents the request to open a file handle
type HandleOpenRequest struct {
	Path  string `json:"path"`
	Flags int    `json:"flags"` // Numeric flags: 0=O_RDONLY, 1=O_WRONLY, 2=O_RDWR, etc.
	Mode  uint32 `json:"mode"`  // File mode for creation (octal)
}

// HandleOpenResponse represents the response when opening a handle
type HandleOpenResponse struct {
	HandleID  int64     `json:"handle_id"`
	Path      string    `json:"path"`
	Flags     int       `json:"flags"`
	Lease     int       `json:"lease"`      // Lease duration in seconds
	ExpiresAt time.Time `json:"expires_at"` // When the lease expires
}

// HandleInfoResponse represents handle information
type HandleInfoResponse struct {
	HandleID   int64     `json:"handle_id"`
	Path       string    `json:"path"`
	Flags      int       `json:"flags"`
	Lease      int       `json:"lease"`
	ExpiresAt  time.Time `json:"expires_at"`
	CreatedAt  time.Time `json:"created_at"`
	LastAccess time.Time `json:"last_access"`
}

// HandleListResponse represents the list of active handles
type HandleListResponse struct {
	Handles []HandleInfoResponse `json:"handles"`
	Count   int                  `json:"count"`
	Max     int                  `json:"max"`
}

// HandleReadResponse represents the response for read operations
type HandleReadResponse struct {
	BytesRead int   `json:"bytes_read"`
	Position  int64 `json:"position"` // Current position after read
}

// HandleWriteResponse represents the response for write operations
type HandleWriteResponse struct {
	BytesWritten int   `json:"bytes_written"`
	Position     int64 `json:"position"` // Current position after write
}

// HandleSeekResponse represents the response for seek operations
type HandleSeekResponse struct {
	Position int64 `json:"position"`
}

// HandleRenewResponse represents the response for lease renewal
type HandleRenewResponse struct {
	ExpiresAt time.Time `json:"expires_at"`
	Lease     int       `json:"lease"`
}

// parseOpenFlags parses numeric flag parameter to OpenFlag
func parseOpenFlags(flagStr string) (filesystem.OpenFlag, error) {
	if flagStr == "" {
		return filesystem.O_RDONLY, nil
	}

	num, err := strconv.ParseInt(flagStr, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid flags parameter: must be a number")
	}
	return filesystem.OpenFlag(num), nil
}


// getHandleFS checks if the filesystem supports HandleFS and returns it
func (h *Handler) getHandleFS() (filesystem.HandleFS, error) {
	handleFS, ok := h.fs.(filesystem.HandleFS)
	if !ok {
		return nil, fmt.Errorf("filesystem does not support file handles")
	}
	return handleFS, nil
}

// OpenHandle handles POST /api/v1/handles/open?path=<path>&flags=<flags>&mode=<mode>
func (h *Handler) OpenHandle(w http.ResponseWriter, r *http.Request) {
	handleFS, err := h.getHandleFS()
	if err != nil {
		writeError(w, http.StatusNotImplemented, err.Error())
		return
	}

	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "path parameter is required")
		return
	}

	flagStr := r.URL.Query().Get("flags")
	flags, err := parseOpenFlags(flagStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	modeStr := r.URL.Query().Get("mode")
	mode := uint32(0644)
	if modeStr != "" {
		m, err := strconv.ParseUint(modeStr, 8, 32)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid mode parameter")
			return
		}
		mode = uint32(m)
	}

	handle, err := handleFS.OpenHandle(path, flags, mode)
	if err != nil {
		status := mapErrorToStatus(err)
		writeError(w, status, err.Error())
		return
	}

	// Handle opened successfully
	response := HandleOpenResponse{
		HandleID:  handle.ID(),
		Path:      handle.Path(),
		Flags:     int(handle.Flags()),
		Lease:     60,
		ExpiresAt: time.Now().Add(60 * time.Second),
	}

	writeJSON(w, http.StatusOK, response)
}

// GetHandle handles GET /api/v1/handles/<id>
func (h *Handler) GetHandle(w http.ResponseWriter, r *http.Request, handleIDStr string) {
	handleFS, err := h.getHandleFS()
	if err != nil {
		writeError(w, http.StatusNotImplemented, err.Error())
		return
	}

	handleID, err := strconv.ParseInt(handleIDStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid handle ID: must be a number")
		return
	}

	handle, err := handleFS.GetHandle(handleID)
	if err != nil {
		status := mapErrorToStatus(err)
		writeError(w, status, err.Error())
		return
	}

	response := HandleInfoResponse{
		HandleID:   handle.ID(),
		Path:       handle.Path(),
		Flags:      int(handle.Flags()),
		Lease:      60,
		ExpiresAt:  time.Now().Add(60 * time.Second),
		CreatedAt:  time.Now(), // Placeholder - actual implementation would track this
		LastAccess: time.Now(),
	}

	writeJSON(w, http.StatusOK, response)
}

// CloseHandle handles DELETE /api/v1/handles/<id>
func (h *Handler) CloseHandle(w http.ResponseWriter, r *http.Request, handleIDStr string) {
	handleFS, err := h.getHandleFS()
	if err != nil {
		writeError(w, http.StatusNotImplemented, err.Error())
		return
	}

	handleID, err := strconv.ParseInt(handleIDStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid handle ID: must be a number")
		return
	}

	if err := handleFS.CloseHandle(handleID); err != nil {
		status := mapErrorToStatus(err)
		writeError(w, status, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, SuccessResponse{Message: "handle closed"})
}

// HandleRead handles GET /api/v1/handles/<id>/read?offset=<offset>&size=<size>
func (h *Handler) HandleRead(w http.ResponseWriter, r *http.Request, handleIDStr string) {
	handleFS, err := h.getHandleFS()
	if err != nil {
		writeError(w, http.StatusNotImplemented, err.Error())
		return
	}

	handleID, err := strconv.ParseInt(handleIDStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid handle ID: must be a number")
		return
	}

	handle, err := handleFS.GetHandle(handleID)
	if err != nil {
		status := mapErrorToStatus(err)
		writeError(w, status, err.Error())
		return
	}

	// Parse size parameter (required for read)
	sizeStr := r.URL.Query().Get("size")
	size := int64(4096) // Default read size
	if sizeStr != "" {
		s, err := strconv.ParseInt(sizeStr, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid size parameter")
			return
		}
		if s < 0 {
			// -1 means read all, use a reasonable default
			size = 1024 * 1024 // 1MB max for "read all"
		} else {
			size = s
		}
	}

	// Check if offset is specified (use ReadAt)
	offsetStr := r.URL.Query().Get("offset")
	var data []byte
	var n int

	if offsetStr != "" {
		offset, err := strconv.ParseInt(offsetStr, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid offset parameter")
			return
		}
		buf := make([]byte, size)
		n, err = handle.ReadAt(buf, offset)
		if err != nil && err != io.EOF {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		data = buf[:n]
	} else {
		buf := make([]byte, size)
		n, err = handle.Read(buf)
		if err != nil && err != io.EOF {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		data = buf[:n]
	}

	// Record traffic
	if h.trafficMonitor != nil && n > 0 {
		h.trafficMonitor.RecordRead(int64(n))
	}

	// Return binary data
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Bytes-Read", strconv.Itoa(n))
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// HandleWrite handles PUT /api/v1/handles/<id>/write?offset=<offset>
func (h *Handler) HandleWrite(w http.ResponseWriter, r *http.Request, handleIDStr string) {
	handleFS, err := h.getHandleFS()
	if err != nil {
		writeError(w, http.StatusNotImplemented, err.Error())
		return
	}

	handleID, err := strconv.ParseInt(handleIDStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid handle ID: must be a number")
		return
	}

	handle, err := handleFS.GetHandle(handleID)
	if err != nil {
		status := mapErrorToStatus(err)
		writeError(w, status, err.Error())
		return
	}

	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	// Record traffic
	if h.trafficMonitor != nil && len(data) > 0 {
		h.trafficMonitor.RecordWrite(int64(len(data)))
	}

	var n int

	// Check if offset is specified (use WriteAt)
	offsetStr := r.URL.Query().Get("offset")
	if offsetStr != "" {
		offset, err := strconv.ParseInt(offsetStr, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid offset parameter")
			return
		}
		n, err = handle.WriteAt(data, offset)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	} else {
		n, err = handle.Write(data)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	response := HandleWriteResponse{
		BytesWritten: n,
	}
	writeJSON(w, http.StatusOK, response)
}

// HandleSeek handles POST /api/v1/handles/<id>/seek?offset=<offset>&whence=<0|1|2>
func (h *Handler) HandleSeek(w http.ResponseWriter, r *http.Request, handleIDStr string) {
	handleFS, err := h.getHandleFS()
	if err != nil {
		writeError(w, http.StatusNotImplemented, err.Error())
		return
	}

	handleID, err := strconv.ParseInt(handleIDStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid handle ID: must be a number")
		return
	}

	handle, err := handleFS.GetHandle(handleID)
	if err != nil {
		status := mapErrorToStatus(err)
		writeError(w, status, err.Error())
		return
	}

	offsetStr := r.URL.Query().Get("offset")
	if offsetStr == "" {
		writeError(w, http.StatusBadRequest, "offset parameter is required")
		return
	}
	offset, err := strconv.ParseInt(offsetStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid offset parameter")
		return
	}

	whenceStr := r.URL.Query().Get("whence")
	whence := io.SeekStart // Default
	if whenceStr != "" {
		wh, err := strconv.Atoi(whenceStr)
		if err != nil || wh < 0 || wh > 2 {
			writeError(w, http.StatusBadRequest, "invalid whence parameter (must be 0, 1, or 2)")
			return
		}
		whence = wh
	}

	pos, err := handle.Seek(offset, whence)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	response := HandleSeekResponse{
		Position: pos,
	}
	writeJSON(w, http.StatusOK, response)
}

// HandleSync handles POST /api/v1/handles/<id>/sync
func (h *Handler) HandleSync(w http.ResponseWriter, r *http.Request, handleIDStr string) {
	handleFS, err := h.getHandleFS()
	if err != nil {
		writeError(w, http.StatusNotImplemented, err.Error())
		return
	}

	handleID, err := strconv.ParseInt(handleIDStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid handle ID: must be a number")
		return
	}

	handle, err := handleFS.GetHandle(handleID)
	if err != nil {
		status := mapErrorToStatus(err)
		writeError(w, status, err.Error())
		return
	}

	if err := handle.Sync(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, SuccessResponse{Message: "synced"})
}

// HandleStat handles GET /api/v1/handles/<id>/stat
func (h *Handler) HandleStat(w http.ResponseWriter, r *http.Request, handleIDStr string) {
	handleFS, err := h.getHandleFS()
	if err != nil {
		writeError(w, http.StatusNotImplemented, err.Error())
		return
	}

	handleID, err := strconv.ParseInt(handleIDStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid handle ID: must be a number")
		return
	}

	handle, err := handleFS.GetHandle(handleID)
	if err != nil {
		status := mapErrorToStatus(err)
		writeError(w, status, err.Error())
		return
	}

	info, err := handle.Stat()
	if err != nil {
		status := mapErrorToStatus(err)
		writeError(w, status, err.Error())
		return
	}

	response := FileInfoResponse{
		Name:    info.Name,
		Size:    info.Size,
		Mode:    info.Mode,
		ModTime: info.ModTime.Format(time.RFC3339Nano),
		IsDir:   info.IsDir,
		Meta:    info.Meta,
	}

	writeJSON(w, http.StatusOK, response)
}

// SetupHandleRoutes sets up routes for file handle operations
func (h *Handler) SetupHandleRoutes(mux *http.ServeMux) {
	// POST /api/v1/handles/open - Open a new handle
	mux.HandleFunc("/api/v1/handles/open", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		h.OpenHandle(w, r)
	})

	// Handle operations on specific handles: /api/v1/handles/<id>/*
	mux.HandleFunc("/api/v1/handles/", func(w http.ResponseWriter, r *http.Request) {
		// Extract handle ID and operation from path
		// Path format: /api/v1/handles/<id> or /api/v1/handles/<id>/<operation>
		path := strings.TrimPrefix(r.URL.Path, "/api/v1/handles/")

		// Skip if this is the /open endpoint (handled separately)
		if path == "open" || strings.HasPrefix(path, "open?") {
			return
		}

		parts := strings.SplitN(path, "/", 2)
		if len(parts) == 0 || parts[0] == "" {
			// List all handles: GET /api/v1/handles/
			if r.Method == http.MethodGet {
				h.ListHandles(w, r)
				return
			}
			writeError(w, http.StatusBadRequest, "handle ID required")
			return
		}

		handleID := parts[0]
		operation := ""
		if len(parts) > 1 {
			operation = parts[1]
		}

		// Route based on operation
		switch operation {
		case "":
			// Operations on the handle itself
			switch r.Method {
			case http.MethodGet:
				h.GetHandle(w, r, handleID)
			case http.MethodDelete:
				h.CloseHandle(w, r, handleID)
			default:
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			}
		case "read":
			if r.Method != http.MethodGet {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			h.HandleRead(w, r, handleID)
		case "write":
			if r.Method != http.MethodPut {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			h.HandleWrite(w, r, handleID)
		case "seek":
			if r.Method != http.MethodPost {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			h.HandleSeek(w, r, handleID)
		case "sync":
			if r.Method != http.MethodPost {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			h.HandleSync(w, r, handleID)
		case "stat":
			if r.Method != http.MethodGet {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			h.HandleStat(w, r, handleID)
		default:
			writeError(w, http.StatusNotFound, "unknown operation: "+operation)
		}
	})
}

// ListHandles handles GET /api/v1/handles - list all active handles
// Note: This returns an empty list as handles are managed per-request
// and there is no central registry. Handles are tracked within each
// mounted filesystem instance.
func (h *Handler) ListHandles(w http.ResponseWriter, r *http.Request) {
	// Return empty list - handles are managed by individual filesystem instances
	response := HandleListResponse{
		Handles: []HandleInfoResponse{},
		Count:   0,
		Max:     10000,
	}
	writeJSON(w, http.StatusOK, response)
}
