package fusefs

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	agfs "github.com/c4pt0r/agfs/agfs-sdk/go"
)

// handleType indicates whether a handle is remote (server-side) or local (client-side fallback)
type handleType int

const (
	handleTypeRemote handleType = iota // Server supports HandleFS
	handleTypeLocal                    // Server doesn't support HandleFS, use local wrapper
)

// handleInfo stores information about an open handle
type handleInfo struct {
	htype      handleType
	agfsHandle int64 // For remote handles: server-side handle ID
	path       string
	flags      agfs.OpenFlag
	mode       uint32
	// Read buffer for local handles - caches first read to avoid multiple server requests
	readBuffer []byte
}

// HandleManager manages the mapping between FUSE handles and AGFS handles
type HandleManager struct {
	client *agfs.Client
	mu     sync.RWMutex
	// Map FUSE handle ID to handle info
	handles map[uint64]*handleInfo
	// Counter for generating unique FUSE handle IDs
	nextHandle uint64
}

// NewHandleManager creates a new handle manager
func NewHandleManager(client *agfs.Client) *HandleManager {
	return &HandleManager{
		client:     client,
		handles:    make(map[uint64]*handleInfo),
		nextHandle: 1,
	}
}

// Open opens a file and returns a FUSE handle ID
// If the server supports HandleFS, it uses server-side handles
// Otherwise, it falls back to local handle management
func (hm *HandleManager) Open(path string, flags agfs.OpenFlag, mode uint32) (uint64, error) {
	// Try to open handle on server first
	agfsHandle, err := hm.client.OpenHandle(path, flags, mode)

	// Generate FUSE handle ID
	fuseHandle := atomic.AddUint64(&hm.nextHandle, 1)

	hm.mu.Lock()
	defer hm.mu.Unlock()

	if err != nil {
		// Check if error is because HandleFS is not supported
		if isHandleNotSupportedError(err) {
			// Fall back to local handle management
			fmt.Printf("DEBUG: HandleFS not supported for %s, using local handle\n", path)
			hm.handles[fuseHandle] = &handleInfo{
				htype: handleTypeLocal,
				path:  path,
				flags: flags,
				mode:  mode,
			}
			return fuseHandle, nil
		}
		fmt.Printf("DEBUG: Failed to open handle for %s: %v\n", path, err)
		return 0, fmt.Errorf("failed to open handle: %w", err)
	}

	fmt.Printf("DEBUG: Opened remote handle for %s (handle=%d)\n", path, agfsHandle)

	// Server supports HandleFS
	hm.handles[fuseHandle] = &handleInfo{
		htype:      handleTypeRemote,
		agfsHandle: agfsHandle,
		path:       path,
		flags:      flags,
		mode:       mode,
	}

	return fuseHandle, nil
}

// Close closes a handle
func (hm *HandleManager) Close(fuseHandle uint64) error {
	hm.mu.Lock()
	info, ok := hm.handles[fuseHandle]
	if !ok {
		hm.mu.Unlock()
		return fmt.Errorf("handle %d not found", fuseHandle)
	}
	delete(hm.handles, fuseHandle)
	hm.mu.Unlock()

	// Remote handles: close on server
	if info.htype == handleTypeRemote {
		if err := hm.client.CloseHandle(info.agfsHandle); err != nil {
			return fmt.Errorf("failed to close handle: %w", err)
		}
		return nil
	}

	// Local handles: nothing to do on close since writes are sent immediately
	return nil
}

// Read reads data from a handle
func (hm *HandleManager) Read(fuseHandle uint64, offset int64, size int) ([]byte, error) {
	hm.mu.Lock()
	info, ok := hm.handles[fuseHandle]
	if !ok {
		hm.mu.Unlock()
		return nil, fmt.Errorf("handle %d not found", fuseHandle)
	}

	if info.htype == handleTypeRemote {
		hm.mu.Unlock()
		// Use server-side handle
		data, err := hm.client.ReadHandle(info.agfsHandle, offset, size)
		if err != nil {
			return nil, fmt.Errorf("failed to read handle: %w", err)
		}
		return data, nil
	}

	// Local handle: cache the first read and return from cache for subsequent reads
	// This is critical for special filesystems like queuefs where each read
	// should be an independent atomic operation (e.g., each read from dequeue
	// should consume only one message, not multiple)
	if info.readBuffer == nil {
		// First read: fetch ALL data from server and cache (use size=-1 to read all)
		path := info.path
		hm.mu.Unlock()

		data, err := hm.client.Read(path, 0, -1) // Read all data
		if err != nil {
			return nil, fmt.Errorf("failed to read file: %w", err)
		}

		// Cache the data
		hm.mu.Lock()
		// Re-check if handle still exists
		info, ok = hm.handles[fuseHandle]
		if ok {
			info.readBuffer = data
		}
		hm.mu.Unlock()

		// Return requested portion
		if offset >= int64(len(data)) {
			return []byte{}, nil
		}
		end := offset + int64(size)
		if end > int64(len(data)) {
			end = int64(len(data))
		}
		return data[offset:end], nil
	}

	// Return from cache or empty for subsequent reads
	if info.readBuffer != nil {
		if offset >= int64(len(info.readBuffer)) {
			hm.mu.Unlock()
			return []byte{}, nil // EOF
		}
		end := offset + int64(size)
		if end > int64(len(info.readBuffer)) {
			end = int64(len(info.readBuffer))
		}
		result := info.readBuffer[offset:end]
		hm.mu.Unlock()
		return result, nil
	}

	// No cached data and offset > 0, return empty
	hm.mu.Unlock()
	return []byte{}, nil
}

// Write writes data to a handle
func (hm *HandleManager) Write(fuseHandle uint64, data []byte, offset int64) (int, error) {
	hm.mu.Lock()
	info, ok := hm.handles[fuseHandle]
	if !ok {
		hm.mu.Unlock()
		return 0, fmt.Errorf("handle %d not found", fuseHandle)
	}

	if info.htype == handleTypeRemote {
		hm.mu.Unlock()
		// Use server-side handle (write directly)
		written, err := hm.client.WriteHandle(info.agfsHandle, data, offset)
		if err != nil {
			return 0, fmt.Errorf("failed to write handle: %w", err)
		}
		return written, nil
	}

	// Local handle: send data directly to server for each write
	// This is critical for special filesystems like queuefs where each write
	// should be an independent atomic operation (e.g., each write to enqueue
	// should create a separate queue message)
	path := info.path
	hm.mu.Unlock()

	// Send directly to server
	_, err := hm.client.Write(path, data)
	if err != nil {
		return 0, fmt.Errorf("failed to write to server: %w", err)
	}

	return len(data), nil
}

// Sync syncs a handle
func (hm *HandleManager) Sync(fuseHandle uint64) error {
	hm.mu.Lock()
	info, ok := hm.handles[fuseHandle]
	if !ok {
		hm.mu.Unlock()
		return fmt.Errorf("handle %d not found", fuseHandle)
	}

	// Remote handles: sync on server
	if info.htype == handleTypeRemote {
		hm.mu.Unlock()
		if err := hm.client.SyncHandle(info.agfsHandle); err != nil {
			return fmt.Errorf("failed to sync handle: %w", err)
		}
		return nil
	}

	// Local handles: nothing to sync since writes are sent immediately
	hm.mu.Unlock()
	return nil
}

// CloseAll closes all open handles
func (hm *HandleManager) CloseAll() error {
	hm.mu.Lock()
	handles := make(map[uint64]*handleInfo)
	for k, v := range hm.handles {
		handles[k] = v
	}
	hm.handles = make(map[uint64]*handleInfo)
	hm.mu.Unlock()

	var lastErr error
	for _, info := range handles {
		if info.htype == handleTypeRemote {
			if err := hm.client.CloseHandle(info.agfsHandle); err != nil {
				lastErr = err
			}
		}
	}

	return lastErr
}

// Count returns the number of open handles
func (hm *HandleManager) Count() int {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	return len(hm.handles)
}

// isHandleNotSupportedError checks if the error indicates HandleFS is not supported
func isHandleNotSupportedError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "does not support file handles") ||
		strings.Contains(errStr, "not support") ||
		strings.Contains(errStr, "HandleFS")
}
