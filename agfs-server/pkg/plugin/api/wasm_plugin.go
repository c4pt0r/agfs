package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/c4pt0r/agfs/agfs-server/pkg/filesystem"
	"github.com/c4pt0r/agfs/agfs-server/pkg/plugin"
	log "github.com/sirupsen/logrus"
	wazeroapi "github.com/tetratelabs/wazero/api"
)

// WASMPlugin represents a plugin loaded from a WASM module
// It uses an instance pool for concurrent access
type WASMPlugin struct {
	name         string
	instancePool *WASMInstancePool
	fileSystem   *PooledWASMFileSystem
}

// PooledWASMFileSystem implements filesystem.FileSystem using an instance pool
type PooledWASMFileSystem struct {
	pool *WASMInstancePool

	// Handle management: maps handle ID to the handle object
	// This ensures handle operations use the same handle instance with its bound WASM instance
	handles      map[int64]*PooledWASMFileHandle
	handleMu     sync.RWMutex
	nextHandleID int64
}

// WASMFileSystem implements filesystem.FileSystem by delegating to WASM functions
// This version is used for individual instances within the pool
type WASMFileSystem struct {
	ctx          context.Context
	module       wazeroapi.Module
	sharedBuffer *SharedBufferInfo // Shared memory buffer info (can be nil)
	mu           *sync.Mutex       // Mutex for single instance (can be nil if instance is not shared)
}

// NewWASMPluginWithPool creates a new WASM plugin wrapper with an instance pool
func NewWASMPluginWithPool(pool *WASMInstancePool, name string) (*WASMPlugin, error) {
	if pool == nil {
		return nil, fmt.Errorf("instance pool cannot be nil")
	}

	wp := &WASMPlugin{
		name:         name,
		instancePool: pool,
		fileSystem: &PooledWASMFileSystem{
			pool:         pool,
			handles:      make(map[int64]*PooledWASMFileHandle),
			nextHandleID: 1,
		},
	}

	return wp, nil
}

// NewWASMPlugin creates a new WASM plugin wrapper (legacy, for backward compatibility)
// For new code, use NewWASMPluginWithPool for better concurrency
func NewWASMPlugin(ctx context.Context, module wazeroapi.Module) (*WASMPlugin, error) {
	// This is kept for backward compatibility but should be migrated to pool-based approach
	// For now, return an error suggesting to use the pool-based approach
	return nil, fmt.Errorf("NewWASMPlugin is deprecated, use NewWASMPluginWithPool for concurrent access")
}

// Name returns the plugin name
func (wp *WASMPlugin) Name() string {
	return wp.name
}

// Validate validates the plugin configuration
func (wp *WASMPlugin) Validate(config map[string]interface{}) error {
	return wp.instancePool.Execute(func(instance *WASMModuleInstance) error {
		validateFunc := instance.module.ExportedFunction("plugin_validate")
		if validateFunc == nil {
			// If validate function is not exported, assume validation passes
			return nil
		}

		// Convert config to JSON
		configJSON, err := json.Marshal(config)
		if err != nil {
			return fmt.Errorf("failed to marshal config: %w", err)
		}

		// Write config to WASM memory
		configPtr, configPtrSize, err := writeStringToMemory(instance.module, string(configJSON))
		if err != nil {
			return fmt.Errorf("failed to write config to memory: %w", err)
		}
		defer freeWASMMemory(instance.module, configPtr, configPtrSize)

		// Call validate function
		results, err := validateFunc.Call(wp.instancePool.ctx, uint64(configPtr))
		if err != nil {
			return fmt.Errorf("validate call failed: %w", err)
		}

		// Check for error return (non-zero means error)
		if len(results) > 0 && results[0] != 0 {
			errPtr := uint32(results[0])
			if errMsg, ok := readStringFromMemory(instance.module, errPtr); ok {
				freeWASMMemory(instance.module, errPtr, 0)
				return fmt.Errorf("validation failed: %s", errMsg)
			}
			freeWASMMemory(instance.module, errPtr, 0)
			return fmt.Errorf("validation failed")
		}

		return nil
	})
}

// Initialize initializes the plugin with configuration
func (wp *WASMPlugin) Initialize(config map[string]interface{}) error {
	return wp.instancePool.Execute(func(instance *WASMModuleInstance) error {
		initFunc := instance.module.ExportedFunction("plugin_initialize")
		if initFunc == nil {
			// If initialize function is not exported, assume initialization succeeds
			return nil
		}

		// Convert config to JSON
		configJSON, err := json.Marshal(config)
		if err != nil {
			return fmt.Errorf("failed to marshal config: %w", err)
		}

		// Write config to WASM memory
		configPtr, configPtrSize, err := writeStringToMemory(instance.module, string(configJSON))
		if err != nil {
			return fmt.Errorf("failed to write config to memory: %w", err)
		}
		defer freeWASMMemory(instance.module, configPtr, configPtrSize)

		// Call initialize function
		results, err := initFunc.Call(wp.instancePool.ctx, uint64(configPtr))
		if err != nil {
			return fmt.Errorf("initialize call failed: %w", err)
		}

		// Check for error return
		if len(results) > 0 && results[0] != 0 {
			errPtr := uint32(results[0])
			if errMsg, ok := readStringFromMemory(instance.module, errPtr); ok {
				freeWASMMemory(instance.module, errPtr, 0)
				return fmt.Errorf("initialization failed: %s", errMsg)
			}
			freeWASMMemory(instance.module, errPtr, 0)
			return fmt.Errorf("initialization failed")
		}

		return nil
	})
}

// GetFileSystem returns the file system implementation
func (wp *WASMPlugin) GetFileSystem() filesystem.FileSystem {
	return wp.fileSystem
}

// GetReadme returns the plugin README
func (wp *WASMPlugin) GetReadme() string {
	var readme string
	wp.instancePool.Execute(func(instance *WASMModuleInstance) error {
		readmeFunc := instance.module.ExportedFunction("plugin_get_readme")
		if readmeFunc == nil {
			readme = ""
			return nil
		}

		results, err := readmeFunc.Call(wp.instancePool.ctx)
		if err != nil {
			log.Warnf("Failed to get readme: %v", err)
			readme = ""
			return nil
		}

		if len(results) > 0 && results[0] != 0 {
			ptr := uint32(results[0])
			if r, ok := readStringFromMemory(instance.module, ptr); ok {
				readme = r
			}
			freeWASMMemory(instance.module, ptr, 0)
		}

		return nil
	})

	return readme
}

// GetConfigParams returns the list of configuration parameters
func (wp *WASMPlugin) GetConfigParams() []plugin.ConfigParameter {
	var params []plugin.ConfigParameter
	wp.instancePool.Execute(func(instance *WASMModuleInstance) error {
		// Check if the plugin exports plugin_get_config_params
		configParamsFunc := instance.module.ExportedFunction("plugin_get_config_params")
		if configParamsFunc == nil {
			// Plugin doesn't export config params, return empty list
			params = []plugin.ConfigParameter{}
			return nil
		}

		// Call the function to get config params JSON
		results, err := configParamsFunc.Call(wp.instancePool.ctx)
		if err != nil {
			log.Warnf("Failed to get config params: %v", err)
			params = []plugin.ConfigParameter{}
			return nil
		}

		if len(results) > 0 && results[0] != 0 {
			ptr := uint32(results[0])
			// Read JSON string from WASM memory
			if jsonStr, ok := readStringFromMemory(instance.module, ptr); ok {
				// Parse JSON into ConfigParameter array
				if err := json.Unmarshal([]byte(jsonStr), &params); err != nil {
					log.Warnf("Failed to unmarshal config params JSON: %v", err)
					params = []plugin.ConfigParameter{}
				}
			}
			freeWASMMemory(instance.module, ptr, 0)
		}

		return nil
	})

	return params
}

// Shutdown shuts down the plugin
func (wp *WASMPlugin) Shutdown() error {
	// Close the instance pool
	return wp.instancePool.Close()
}

// PooledWASMFileSystem implementation
// All methods delegate to the instance pool

func (pfs *PooledWASMFileSystem) Create(path string) error {
	return pfs.pool.ExecuteFS(func(fs filesystem.FileSystem) error {
		return fs.Create(path)
	})
}

func (pfs *PooledWASMFileSystem) Mkdir(path string, perm uint32) error {
	return pfs.pool.ExecuteFS(func(fs filesystem.FileSystem) error {
		return fs.Mkdir(path, perm)
	})
}

func (pfs *PooledWASMFileSystem) Remove(path string) error {
	return pfs.pool.ExecuteFS(func(fs filesystem.FileSystem) error {
		return fs.Remove(path)
	})
}

func (pfs *PooledWASMFileSystem) RemoveAll(path string) error {
	return pfs.pool.ExecuteFS(func(fs filesystem.FileSystem) error {
		return fs.RemoveAll(path)
	})
}

func (pfs *PooledWASMFileSystem) Read(path string, offset int64, size int64) ([]byte, error) {
	var data []byte
	err := pfs.pool.ExecuteFS(func(fs filesystem.FileSystem) error {
		var readErr error
		data, readErr = fs.Read(path, offset, size)
		return readErr
	})
	return data, err
}

func (pfs *PooledWASMFileSystem) Write(path string, data []byte, offset int64, flags filesystem.WriteFlag) (int64, error) {
	var bytesWritten int64
	err := pfs.pool.ExecuteFS(func(fs filesystem.FileSystem) error {
		var writeErr error
		bytesWritten, writeErr = fs.Write(path, data, offset, flags)
		return writeErr
	})
	return bytesWritten, err
}

func (pfs *PooledWASMFileSystem) ReadDir(path string) ([]filesystem.FileInfo, error) {
	var infos []filesystem.FileInfo
	err := pfs.pool.ExecuteFS(func(fs filesystem.FileSystem) error {
		var readErr error
		infos, readErr = fs.ReadDir(path)
		return readErr
	})
	return infos, err
}

func (pfs *PooledWASMFileSystem) Stat(path string) (*filesystem.FileInfo, error) {
	var info *filesystem.FileInfo
	err := pfs.pool.ExecuteFS(func(fs filesystem.FileSystem) error {
		var statErr error
		info, statErr = fs.Stat(path)
		return statErr
	})
	return info, err
}

func (pfs *PooledWASMFileSystem) Rename(oldPath, newPath string) error {
	return pfs.pool.ExecuteFS(func(fs filesystem.FileSystem) error {
		return fs.Rename(oldPath, newPath)
	})
}

func (pfs *PooledWASMFileSystem) Chmod(path string, mode uint32) error {
	return pfs.pool.ExecuteFS(func(fs filesystem.FileSystem) error {
		return fs.Chmod(path, mode)
	})
}

func (pfs *PooledWASMFileSystem) Open(path string) (io.ReadCloser, error) {
	var reader io.ReadCloser
	err := pfs.pool.ExecuteFS(func(fs filesystem.FileSystem) error {
		var openErr error
		reader, openErr = fs.Open(path)
		return openErr
	})
	return reader, err
}

func (pfs *PooledWASMFileSystem) OpenWrite(path string) (io.WriteCloser, error) {
	var writer io.WriteCloser
	err := pfs.pool.ExecuteFS(func(fs filesystem.FileSystem) error {
		var openErr error
		writer, openErr = fs.OpenWrite(path)
		return openErr
	})
	return writer, err
}

// HandleFS interface for PooledWASMFileSystem

// SupportsHandleFS checks if the underlying WASM plugin supports HandleFS
func (pfs *PooledWASMFileSystem) SupportsHandleFS() bool {
	var supports bool
	pfs.pool.Execute(func(instance *WASMModuleInstance) error {
		openFunc := instance.module.ExportedFunction("handle_open")
		supports = openFunc != nil
		return nil
	})
	return supports
}

// OpenHandle opens a file and returns a handle
// This acquires a WASM instance and keeps it bound to this handle until close
func (pfs *PooledWASMFileSystem) OpenHandle(path string, flags filesystem.OpenFlag, mode uint32) (filesystem.FileHandle, error) {
	// Acquire an instance from the pool (and don't release it until handle is closed)
	instance, err := pfs.pool.Acquire()
	if err != nil {
		return nil, fmt.Errorf("failed to acquire WASM instance: %w", err)
	}

	// Call OpenHandle on the WASM instance
	handle, err := instance.fileSystem.OpenHandle(path, flags, mode)
	if err != nil {
		// Release the instance back to pool on error
		pfs.pool.Release(instance)
		return nil, err
	}

	// Assign a new int64 handle ID
	pfs.handleMu.Lock()
	handleID := pfs.nextHandleID
	pfs.nextHandleID++

	// Create a wrapped handle that routes operations through our tracking
	pooledHandle := &PooledWASMFileHandle{
		id:       handleID,
		inner:    handle.(*WASMFileHandle),
		pfs:      pfs,
		instance: instance,
	}

	// Store the handle object so GetHandle can return it
	pfs.handles[handleID] = pooledHandle
	pfs.handleMu.Unlock()

	return pooledHandle, nil
}

// GetHandle retrieves an existing handle by ID
// Returns the same handle object that was created by OpenHandle
func (pfs *PooledWASMFileSystem) GetHandle(id int64) (filesystem.FileHandle, error) {
	pfs.handleMu.RLock()
	handle, ok := pfs.handles[id]
	pfs.handleMu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("handle not found: %d", id)
	}

	if handle.closed {
		return nil, fmt.Errorf("handle is closed: %d", id)
	}

	return handle, nil
}

// CloseHandle closes a handle by ID
func (pfs *PooledWASMFileSystem) CloseHandle(id int64) error {
	pfs.handleMu.Lock()
	handle, ok := pfs.handles[id]
	if ok {
		delete(pfs.handles, id)
	}
	pfs.handleMu.Unlock()

	if !ok {
		return fmt.Errorf("handle not found: %d", id)
	}

	return handle.Close()
}

// PooledWASMFileHandle wraps WASMFileHandle to manage instance lifecycle
type PooledWASMFileHandle struct {
	id       int64
	inner    *WASMFileHandle
	pfs      *PooledWASMFileSystem
	instance *WASMModuleInstance
	closed   bool
	mu       sync.Mutex
}

func (h *PooledWASMFileHandle) ID() int64 {
	return h.id
}

func (h *PooledWASMFileHandle) Path() string {
	return h.inner.Path()
}

func (h *PooledWASMFileHandle) Flags() filesystem.OpenFlag {
	return h.inner.Flags()
}

func (h *PooledWASMFileHandle) Read(buf []byte) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return 0, fmt.Errorf("handle is closed")
	}
	return h.inner.Read(buf)
}

func (h *PooledWASMFileHandle) ReadAt(buf []byte, offset int64) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return 0, fmt.Errorf("handle is closed")
	}
	return h.inner.ReadAt(buf, offset)
}

func (h *PooledWASMFileHandle) Write(data []byte) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return 0, fmt.Errorf("handle is closed")
	}
	return h.inner.Write(data)
}

func (h *PooledWASMFileHandle) WriteAt(data []byte, offset int64) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return 0, fmt.Errorf("handle is closed")
	}
	return h.inner.WriteAt(data, offset)
}

func (h *PooledWASMFileHandle) Seek(offset int64, whence int) (int64, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return 0, fmt.Errorf("handle is closed")
	}
	return h.inner.Seek(offset, whence)
}

func (h *PooledWASMFileHandle) Sync() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return fmt.Errorf("handle is closed")
	}
	return h.inner.Sync()
}

func (h *PooledWASMFileHandle) Stat() (*filesystem.FileInfo, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, fmt.Errorf("handle is closed")
	}
	return h.inner.Stat()
}

func (h *PooledWASMFileHandle) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return fmt.Errorf("handle is already closed")
	}
	h.closed = true

	// Close the inner handle
	err := h.inner.Close()

	// Remove from tracking (if not already removed by CloseHandle)
	h.pfs.handleMu.Lock()
	delete(h.pfs.handles, h.id)
	h.pfs.handleMu.Unlock()

	// Release the WASM instance back to the pool
	h.pfs.pool.Release(h.instance)

	return err
}

// WASMFileSystem implementations

func (wfs *WASMFileSystem) Create(path string) error {
	createFunc := wfs.module.ExportedFunction("fs_create")
	if createFunc == nil {
		return fmt.Errorf("fs_create not implemented")
	}

	pathPtr, pathPtrSize, err := writeStringToMemoryWithBuffer(wfs.module, path, wfs.sharedBuffer)
	if err != nil {
		return err
	}
	defer freeWASMMemoryWithBuffer(wfs.module, pathPtr, pathPtrSize, wfs.sharedBuffer)

	results, err := createFunc.Call(wfs.ctx, uint64(pathPtr))
	if err != nil {
		return fmt.Errorf("fs_create failed: %w", err)
	}

	if len(results) > 0 && results[0] != 0 {
		errPtr := uint32(results[0])
		if errMsg, ok := readStringFromMemory(wfs.module, errPtr); ok {
			freeWASMMemory(wfs.module, errPtr, 0)
			return fmt.Errorf("%s", errMsg)
		}
		freeWASMMemory(wfs.module, errPtr, 0)
		return fmt.Errorf("create failed")
	}

	return nil
}

func (wfs *WASMFileSystem) Mkdir(path string, perm uint32) error {
	mkdirFunc := wfs.module.ExportedFunction("fs_mkdir")
	if mkdirFunc == nil {
		return fmt.Errorf("fs_mkdir not implemented")
	}

	pathPtr, pathPtrSize, err := writeStringToMemory(wfs.module, path)
	if err != nil {
		return err
	}
	defer freeWASMMemory(wfs.module, pathPtr, pathPtrSize)

	results, err := mkdirFunc.Call(wfs.ctx, uint64(pathPtr), uint64(perm))
	if err != nil {
		return fmt.Errorf("fs_mkdir failed: %w", err)
	}

	if len(results) > 0 && results[0] != 0 {
		errPtr := uint32(results[0])
		if errMsg, ok := readStringFromMemory(wfs.module, errPtr); ok {
			freeWASMMemory(wfs.module, errPtr, 0)
			return fmt.Errorf("%s", errMsg)
		}
		freeWASMMemory(wfs.module, errPtr, 0)
		return fmt.Errorf("mkdir failed")
	}

	return nil
}

func (wfs *WASMFileSystem) Remove(path string) error {
	removeFunc := wfs.module.ExportedFunction("fs_remove")
	if removeFunc == nil {
		return fmt.Errorf("fs_remove not implemented")
	}

	pathPtr, pathPtrSize, err := writeStringToMemory(wfs.module, path)
	if err != nil {
		return err
	}
	defer freeWASMMemory(wfs.module, pathPtr, pathPtrSize)

	results, err := removeFunc.Call(wfs.ctx, uint64(pathPtr))
	if err != nil {
		return fmt.Errorf("fs_remove failed: %w", err)
	}

	if len(results) > 0 && results[0] != 0 {
		errPtr := uint32(results[0])
		if errMsg, ok := readStringFromMemory(wfs.module, errPtr); ok {
			freeWASMMemory(wfs.module, errPtr, 0)
			return fmt.Errorf("%s", errMsg)
		}
		freeWASMMemory(wfs.module, errPtr, 0)
		return fmt.Errorf("remove failed")
	}

	return nil
}

func (wfs *WASMFileSystem) RemoveAll(path string) error {
	removeAllFunc := wfs.module.ExportedFunction("fs_remove_all")
	if removeAllFunc == nil {
		// Fall back to Remove if RemoveAll not implemented
		return wfs.Remove(path)
	}

	pathPtr, pathPtrSize, err := writeStringToMemory(wfs.module, path)
	if err != nil {
		return err
	}
	defer freeWASMMemory(wfs.module, pathPtr, pathPtrSize)

	results, err := removeAllFunc.Call(wfs.ctx, uint64(pathPtr))
	if err != nil {
		return fmt.Errorf("fs_remove_all failed: %w", err)
	}

	if len(results) > 0 && results[0] != 0 {
		errPtr := uint32(results[0])
		if errMsg, ok := readStringFromMemory(wfs.module, errPtr); ok {
			freeWASMMemory(wfs.module, errPtr, 0)
			return fmt.Errorf("%s", errMsg)
		}
		freeWASMMemory(wfs.module, errPtr, 0)
		return fmt.Errorf("remove_all failed")
	}

	return nil
}

func (wfs *WASMFileSystem) Read(path string, offset int64, size int64) ([]byte, error) {
	// Only lock if mutex is not nil (for backward compatibility)
	// Pooled instances don't need mutex as they're single-threaded
	if wfs.mu != nil {
		wfs.mu.Lock()
		defer wfs.mu.Unlock()
	}

	readFunc := wfs.module.ExportedFunction("fs_read")
	if readFunc == nil {
		return nil, fmt.Errorf("fs_read not implemented")
	}

	pathPtr, pathPtrSize, err := writeStringToMemoryWithBuffer(wfs.module, path, wfs.sharedBuffer)
	if err != nil {
		return nil, err
	}
	defer freeWASMMemoryWithBuffer(wfs.module, pathPtr, pathPtrSize, wfs.sharedBuffer)

	results, err := readFunc.Call(wfs.ctx, uint64(pathPtr), uint64(offset), uint64(size))
	if err != nil {
		return nil, fmt.Errorf("fs_read failed: %w", err)
	}

	if len(results) < 1 {
		return nil, fmt.Errorf("fs_read returned invalid results")
	}

	// Unpack u64: lower 32 bits = pointer, upper 32 bits = size
	packed := results[0]
	dataPtr := uint32(packed & 0xFFFFFFFF)
	dataSize := uint32((packed >> 32) & 0xFFFFFFFF)

	if dataPtr == 0 {
		return nil, fmt.Errorf("read failed")
	}

	data, ok := wfs.module.Memory().Read(dataPtr, dataSize)
	if !ok {
		freeWASMMemory(wfs.module, dataPtr, 0)
		return nil, fmt.Errorf("failed to read data from memory")
	}

	// Free WASM memory after copying data
	freeWASMMemory(wfs.module, dataPtr, 0)

	return data, nil
}

func (wfs *WASMFileSystem) Write(path string, data []byte, offset int64, flags filesystem.WriteFlag) (int64, error) {
	writeFunc := wfs.module.ExportedFunction("fs_write")
	if writeFunc == nil {
		return 0, fmt.Errorf("fs_write not implemented")
	}

	pathPtr, pathPtrSize, err := writeStringToMemoryWithBuffer(wfs.module, path, wfs.sharedBuffer)
	if err != nil {
		return 0, err
	}
	defer freeWASMMemoryWithBuffer(wfs.module, pathPtr, pathPtrSize, wfs.sharedBuffer)

	dataPtr, dataPtrSize, err := writeBytesToMemoryWithBuffer(wfs.module, data, wfs.sharedBuffer)
	if err != nil {
		return 0, err
	}
	defer freeWASMMemoryWithBuffer(wfs.module, dataPtr, dataPtrSize, wfs.sharedBuffer)

	// Call WASM plugin with new signature: fs_write(path, data, len, offset, flags) -> packed u64
	results, err := writeFunc.Call(wfs.ctx, uint64(pathPtr), uint64(dataPtr), uint64(len(data)), uint64(offset), uint64(flags))
	if err != nil {
		return 0, fmt.Errorf("fs_write failed: %w", err)
	}

	if len(results) < 1 {
		return 0, fmt.Errorf("fs_write returned invalid results")
	}

	// New return format: packed u64 with high 32 bits = bytes written, low 32 bits = error ptr
	packed := results[0]
	bytesWritten := uint32(packed >> 32)
	errPtr := uint32(packed & 0xFFFFFFFF)

	if errPtr != 0 {
		// Read error message from WASM memory
		errMsg, ok := readStringFromMemory(wfs.module, errPtr)
		freeWASMMemory(wfs.module, errPtr, 0)
		if ok && errMsg != "" {
			return 0, fmt.Errorf("write failed: %s", errMsg)
		}
		return 0, fmt.Errorf("write failed")
	}

	return int64(bytesWritten), nil
}

func (wfs *WASMFileSystem) ReadDir(path string) ([]filesystem.FileInfo, error) {
	readDirFunc := wfs.module.ExportedFunction("fs_readdir")
	if readDirFunc == nil {
		return nil, fmt.Errorf("fs_readdir not implemented")
	}

	pathPtr, pathPtrSize, err := writeStringToMemory(wfs.module, path)
	if err != nil {
		return nil, err
	}
	defer freeWASMMemory(wfs.module, pathPtr, pathPtrSize)

	results, err := readDirFunc.Call(wfs.ctx, uint64(pathPtr))
	if err != nil {
		return nil, fmt.Errorf("fs_readdir failed: %w", err)
	}

	if len(results) < 1 {
		return nil, fmt.Errorf("fs_readdir returned invalid results")
	}

	// Unpack u64: lower 32 bits = json pointer, upper 32 bits = error pointer
	packed := results[0]
	jsonPtr := uint32(packed & 0xFFFFFFFF)
	errPtr := uint32((packed >> 32) & 0xFFFFFFFF)

	// Check for error
	if errPtr != 0 {
		if errMsg, ok := readStringFromMemory(wfs.module, errPtr); ok {
			freeWASMMemory(wfs.module, errPtr, 0)
			return nil, fmt.Errorf("%s", errMsg)
		}
		freeWASMMemory(wfs.module, errPtr, 0)
		return nil, fmt.Errorf("readdir failed")
	}

	if jsonPtr == 0 {
		return []filesystem.FileInfo{}, nil
	}

	jsonStr, ok := readStringFromMemory(wfs.module, jsonPtr)
	if !ok {
		freeWASMMemory(wfs.module, jsonPtr, 0)
		return nil, fmt.Errorf("failed to read readdir result")
	}

	// Free WASM memory after reading
	freeWASMMemory(wfs.module, jsonPtr, 0)

	var fileInfos []filesystem.FileInfo
	if err := json.Unmarshal([]byte(jsonStr), &fileInfos); err != nil {
		return nil, fmt.Errorf("failed to unmarshal readdir result: %w", err)
	}

	return fileInfos, nil
}

func (wfs *WASMFileSystem) Stat(path string) (*filesystem.FileInfo, error) {
	log.Debugf("WASM Stat called with path: %s", path)
	statFunc := wfs.module.ExportedFunction("fs_stat")
	if statFunc == nil {
		return nil, fmt.Errorf("fs_stat not implemented")
	}

	pathPtr, pathPtrSize, err := writeStringToMemoryWithBuffer(wfs.module, path, wfs.sharedBuffer)
	if err != nil {
		log.Errorf("Failed to write path to memory: %v", err)
		return nil, err
	}
	defer freeWASMMemoryWithBuffer(wfs.module, pathPtr, pathPtrSize, wfs.sharedBuffer)

	log.Debugf("Calling fs_stat WASM function with pathPtr=%d", pathPtr)
	results, err := statFunc.Call(wfs.ctx, uint64(pathPtr))
	if err != nil {
		log.Errorf("fs_stat WASM call failed: %v", err)
		return nil, fmt.Errorf("fs_stat failed: %w", err)
	}
	log.Debugf("fs_stat returned %d results", len(results))

	if len(results) < 1 {
		return nil, fmt.Errorf("fs_stat returned invalid results")
	}

	// Unpack u64: lower 32 bits = json pointer, upper 32 bits = error pointer
	packed := results[0]
	jsonPtr := uint32(packed & 0xFFFFFFFF)
	errPtr := uint32((packed >> 32) & 0xFFFFFFFF)

	// Check for error
	if errPtr != 0 {
		if errMsg, ok := readStringFromMemory(wfs.module, errPtr); ok {
			freeWASMMemory(wfs.module, errPtr, 0)
			return nil, fmt.Errorf("%s", errMsg)
		}
		freeWASMMemory(wfs.module, errPtr, 0)
		return nil, fmt.Errorf("stat failed")
	}

	if jsonPtr == 0 {
		return nil, fmt.Errorf("stat returned null")
	}

	jsonStr, ok := readStringFromMemory(wfs.module, jsonPtr)
	if !ok {
		freeWASMMemory(wfs.module, jsonPtr, 0)
		return nil, fmt.Errorf("failed to read stat result")
	}

	// Free WASM memory after reading
	freeWASMMemory(wfs.module, jsonPtr, 0)

	var fileInfo filesystem.FileInfo
	if err := json.Unmarshal([]byte(jsonStr), &fileInfo); err != nil {
		return nil, fmt.Errorf("failed to unmarshal stat result: %w", err)
	}

	return &fileInfo, nil
}

func (wfs *WASMFileSystem) Rename(oldPath, newPath string) error {
	renameFunc := wfs.module.ExportedFunction("fs_rename")
	if renameFunc == nil {
		return fmt.Errorf("fs_rename not implemented")
	}

	oldPathPtr, oldPathPtrSize, err := writeStringToMemory(wfs.module, oldPath)
	if err != nil {
		return err
	}
	defer freeWASMMemory(wfs.module, oldPathPtr, oldPathPtrSize)

	newPathPtr, newPathPtrSize, err := writeStringToMemory(wfs.module, newPath)
	if err != nil {
		return err
	}
	defer freeWASMMemory(wfs.module, newPathPtr, newPathPtrSize)

	results, err := renameFunc.Call(wfs.ctx, uint64(oldPathPtr), uint64(newPathPtr))
	if err != nil {
		return fmt.Errorf("fs_rename failed: %w", err)
	}

	if len(results) > 0 && results[0] != 0 {
		errPtr := uint32(results[0])
		if errMsg, ok := readStringFromMemory(wfs.module, errPtr); ok {
			freeWASMMemory(wfs.module, errPtr, 0)
			return fmt.Errorf("%s", errMsg)
		}
		freeWASMMemory(wfs.module, errPtr, 0)
		return fmt.Errorf("rename failed")
	}

	return nil
}

func (wfs *WASMFileSystem) Chmod(path string, mode uint32) error {
	chmodFunc := wfs.module.ExportedFunction("fs_chmod")
	if chmodFunc == nil {
		// Chmod is optional, silently ignore if not implemented
		return nil
	}

	pathPtr, pathPtrSize, err := writeStringToMemory(wfs.module, path)
	if err != nil {
		return err
	}
	defer freeWASMMemory(wfs.module, pathPtr, pathPtrSize)

	results, err := chmodFunc.Call(wfs.ctx, uint64(pathPtr), uint64(mode))
	if err != nil {
		return fmt.Errorf("fs_chmod failed: %w", err)
	}

	if len(results) > 0 && results[0] != 0 {
		errPtr := uint32(results[0])
		if errMsg, ok := readStringFromMemory(wfs.module, errPtr); ok {
			freeWASMMemory(wfs.module, errPtr, 0)
			return fmt.Errorf("%s", errMsg)
		}
		freeWASMMemory(wfs.module, errPtr, 0)
		return fmt.Errorf("chmod failed")
	}

	return nil
}

func (wfs *WASMFileSystem) Open(path string) (io.ReadCloser, error) {
	// For WASM plugins, we can implement Open by reading the entire file
	// This is a simple implementation; more sophisticated implementations
	// could use streaming or chunked reads
	data, err := wfs.Read(path, 0, -1)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(io.NewSectionReader(&bytesReaderAt{data}, 0, int64(len(data)))), nil
}

func (wfs *WASMFileSystem) OpenWrite(path string) (io.WriteCloser, error) {
	// For WASM plugins, we return a WriteCloser that buffers writes
	// and flushes on close
	return &wasmWriteCloser{
		fs:   wfs,
		path: path,
		buf:  make([]byte, 0),
	}, nil
}

// HandleFS interface implementation for WASM plugins

// SupportsHandleFS checks if the WASM plugin exports handle functions
func (wfs *WASMFileSystem) SupportsHandleFS() bool {
	openFunc := wfs.module.ExportedFunction("handle_open")
	return openFunc != nil
}

// OpenHandle opens a file and returns a handle
func (wfs *WASMFileSystem) OpenHandle(path string, flags filesystem.OpenFlag, mode uint32) (filesystem.FileHandle, error) {
	openFunc := wfs.module.ExportedFunction("handle_open")
	if openFunc == nil {
		return nil, fmt.Errorf("handle_open not implemented in WASM plugin")
	}

	pathPtr, pathPtrSize, err := writeStringToMemoryWithBuffer(wfs.module, path, wfs.sharedBuffer)
	if err != nil {
		return nil, err
	}
	defer freeWASMMemoryWithBuffer(wfs.module, pathPtr, pathPtrSize, wfs.sharedBuffer)

	results, err := openFunc.Call(wfs.ctx, uint64(pathPtr), uint64(flags), uint64(mode))
	if err != nil {
		return nil, fmt.Errorf("handle_open failed: %w", err)
	}

	if len(results) < 1 {
		return nil, fmt.Errorf("handle_open returned invalid results")
	}

	// Unpack u64: low 32 bits = error ptr, high 32 bits = handle_id (as i64)
	// When successful: packed = (handle_id << 32) | 0
	// When error: packed = 0 | error_ptr
	packed := results[0]
	errPtr := uint32(packed & 0xFFFFFFFF)
	handleID := int64(packed >> 32)

	if errPtr != 0 {
		errMsg, ok := readStringFromMemory(wfs.module, errPtr)
		freeWASMMemory(wfs.module, errPtr, 0)
		if ok && errMsg != "" {
			return nil, fmt.Errorf("open handle failed: %s", errMsg)
		}
		return nil, fmt.Errorf("open handle failed")
	}

	if handleID == 0 {
		return nil, fmt.Errorf("handle_open returned zero id")
	}

	return &WASMFileHandle{
		wasmID: handleID,
		path:   path,
		flags:  flags,
		wfs:    wfs,
	}, nil
}

// GetHandle retrieves an existing handle by ID
// For WASMFileSystem, this is not directly supported since we use string IDs internally
// The PooledWASMFileSystem layer handles the int64 to internal mapping
func (wfs *WASMFileSystem) GetHandle(id int64) (filesystem.FileHandle, error) {
	return nil, fmt.Errorf("WASMFileSystem.GetHandle not supported directly; use PooledWASMFileSystem")
}

// CloseHandle closes a handle by ID
// For WASMFileSystem, this is not directly supported since we use string IDs internally
func (wfs *WASMFileSystem) CloseHandle(id int64) error {
	return fmt.Errorf("WASMFileSystem.CloseHandle not supported directly; use PooledWASMFileSystem")
}

// Internal handle operation methods

func (wfs *WASMFileSystem) handleRead(id int64, buf []byte) (int, error) {
	readFunc := wfs.module.ExportedFunction("handle_read")
	if readFunc == nil {
		return 0, fmt.Errorf("handle_read not implemented")
	}

	// Allocate buffer in WASM memory (can use shared buffer)
	bufPtr, bufPtrSize, err := writeBytesToMemoryWithBuffer(wfs.module, make([]byte, len(buf)), wfs.sharedBuffer)
	if err != nil {
		return 0, err
	}
	defer freeWASMMemoryWithBuffer(wfs.module, bufPtr, bufPtrSize, wfs.sharedBuffer)

	results, err := readFunc.Call(wfs.ctx, uint64(id), uint64(bufPtr), uint64(len(buf)))
	if err != nil {
		return 0, fmt.Errorf("handle_read failed: %w", err)
	}

	if len(results) < 1 {
		return 0, fmt.Errorf("handle_read returned invalid results")
	}

	// Unpack u64: low 32 bits = bytes read, high 32 bits = error ptr
	packed := results[0]
	bytesRead := uint32(packed & 0xFFFFFFFF)
	errPtr := uint32(packed >> 32)

	if errPtr != 0 {
		errMsg, ok := readStringFromMemory(wfs.module, errPtr)
		freeWASMMemory(wfs.module, errPtr, 0)
		if ok && errMsg != "" {
			return 0, fmt.Errorf("read failed: %s", errMsg)
		}
		return 0, fmt.Errorf("read failed")
	}

	// Copy data from WASM memory to buf
	if bytesRead > 0 {
		data, ok := wfs.module.Memory().Read(bufPtr, bytesRead)
		if !ok {
			return 0, fmt.Errorf("failed to read data from WASM memory")
		}
		copy(buf, data)
	}

	return int(bytesRead), nil
}

func (wfs *WASMFileSystem) handleReadAt(id int64, buf []byte, offset int64) (int, error) {
	readAtFunc := wfs.module.ExportedFunction("handle_read_at")
	if readAtFunc == nil {
		return 0, fmt.Errorf("handle_read_at not implemented")
	}

	bufPtr, bufPtrSize, err := writeBytesToMemoryWithBuffer(wfs.module, make([]byte, len(buf)), wfs.sharedBuffer)
	if err != nil {
		return 0, err
	}
	defer freeWASMMemoryWithBuffer(wfs.module, bufPtr, bufPtrSize, wfs.sharedBuffer)

	results, err := readAtFunc.Call(wfs.ctx, uint64(id), uint64(bufPtr), uint64(len(buf)), uint64(offset))
	if err != nil {
		return 0, fmt.Errorf("handle_read_at failed: %w", err)
	}

	if len(results) < 1 {
		return 0, fmt.Errorf("handle_read_at returned invalid results")
	}

	// Unpack u64: low 32 bits = bytes read, high 32 bits = error ptr
	packed := results[0]
	bytesRead := uint32(packed & 0xFFFFFFFF)
	errPtr := uint32(packed >> 32)

	if errPtr != 0 {
		errMsg, ok := readStringFromMemory(wfs.module, errPtr)
		freeWASMMemory(wfs.module, errPtr, 0)
		if ok && errMsg != "" {
			return 0, fmt.Errorf("read at failed: %s", errMsg)
		}
		return 0, fmt.Errorf("read at failed")
	}

	if bytesRead > 0 {
		data, ok := wfs.module.Memory().Read(bufPtr, bytesRead)
		if !ok {
			return 0, fmt.Errorf("failed to read data from WASM memory")
		}
		copy(buf, data)
	}

	return int(bytesRead), nil
}

func (wfs *WASMFileSystem) handleWrite(id int64, data []byte) (int, error) {
	writeFunc := wfs.module.ExportedFunction("handle_write")
	if writeFunc == nil {
		return 0, fmt.Errorf("handle_write not implemented")
	}

	dataPtr, dataPtrSize, err := writeBytesToMemoryWithBuffer(wfs.module, data, wfs.sharedBuffer)
	if err != nil {
		return 0, err
	}
	defer freeWASMMemoryWithBuffer(wfs.module, dataPtr, dataPtrSize, wfs.sharedBuffer)

	results, err := writeFunc.Call(wfs.ctx, uint64(id), uint64(dataPtr), uint64(len(data)))
	if err != nil {
		return 0, fmt.Errorf("handle_write failed: %w", err)
	}

	if len(results) < 1 {
		return 0, fmt.Errorf("handle_write returned invalid results")
	}

	// Unpack u64: low 32 bits = bytes written, high 32 bits = error ptr
	packed := results[0]
	bytesWritten := uint32(packed & 0xFFFFFFFF)
	errPtr := uint32(packed >> 32)

	if errPtr != 0 {
		errMsg, ok := readStringFromMemory(wfs.module, errPtr)
		freeWASMMemory(wfs.module, errPtr, 0)
		if ok && errMsg != "" {
			return 0, fmt.Errorf("write failed: %s", errMsg)
		}
		return 0, fmt.Errorf("write failed")
	}

	return int(bytesWritten), nil
}

func (wfs *WASMFileSystem) handleWriteAt(id int64, data []byte, offset int64) (int, error) {
	writeAtFunc := wfs.module.ExportedFunction("handle_write_at")
	if writeAtFunc == nil {
		return 0, fmt.Errorf("handle_write_at not implemented")
	}

	dataPtr, dataPtrSize, err := writeBytesToMemoryWithBuffer(wfs.module, data, wfs.sharedBuffer)
	if err != nil {
		return 0, err
	}
	defer freeWASMMemoryWithBuffer(wfs.module, dataPtr, dataPtrSize, wfs.sharedBuffer)

	results, err := writeAtFunc.Call(wfs.ctx, uint64(id), uint64(dataPtr), uint64(len(data)), uint64(offset))
	if err != nil {
		return 0, fmt.Errorf("handle_write_at failed: %w", err)
	}

	if len(results) < 1 {
		return 0, fmt.Errorf("handle_write_at returned invalid results")
	}

	// Unpack u64: low 32 bits = bytes written, high 32 bits = error ptr
	packed := results[0]
	bytesWritten := uint32(packed & 0xFFFFFFFF)
	errPtr := uint32(packed >> 32)

	if errPtr != 0 {
		errMsg, ok := readStringFromMemory(wfs.module, errPtr)
		freeWASMMemory(wfs.module, errPtr, 0)
		if ok && errMsg != "" {
			return 0, fmt.Errorf("write at failed: %s", errMsg)
		}
		return 0, fmt.Errorf("write at failed")
	}

	return int(bytesWritten), nil
}

func (wfs *WASMFileSystem) handleSeek(id int64, offset int64, whence int) (int64, error) {
	seekFunc := wfs.module.ExportedFunction("handle_seek")
	if seekFunc == nil {
		return 0, fmt.Errorf("handle_seek not implemented")
	}

	results, err := seekFunc.Call(wfs.ctx, uint64(id), uint64(offset), uint64(whence))
	if err != nil {
		return 0, fmt.Errorf("handle_seek failed: %w", err)
	}

	if len(results) < 1 {
		return 0, fmt.Errorf("handle_seek returned invalid results")
	}

	// Unpack u64: low 32 bits = new position, high 32 bits = error ptr
	packed := results[0]
	newPos := uint32(packed & 0xFFFFFFFF)
	errPtr := uint32(packed >> 32)

	if errPtr != 0 {
		errMsg, ok := readStringFromMemory(wfs.module, errPtr)
		freeWASMMemory(wfs.module, errPtr, 0)
		if ok && errMsg != "" {
			return 0, fmt.Errorf("seek failed: %s", errMsg)
		}
		return 0, fmt.Errorf("seek failed")
	}

	return int64(newPos), nil
}

func (wfs *WASMFileSystem) handleSync(id int64) error {
	syncFunc := wfs.module.ExportedFunction("handle_sync")
	if syncFunc == nil {
		return fmt.Errorf("handle_sync not implemented")
	}

	results, err := syncFunc.Call(wfs.ctx, uint64(id))
	if err != nil {
		return fmt.Errorf("handle_sync failed: %w", err)
	}

	if len(results) > 0 && results[0] != 0 {
		errPtr := uint32(results[0])
		errMsg, ok := readStringFromMemory(wfs.module, errPtr)
		freeWASMMemory(wfs.module, errPtr, 0)
		if ok && errMsg != "" {
			return fmt.Errorf("sync failed: %s", errMsg)
		}
		return fmt.Errorf("sync failed")
	}

	return nil
}

func (wfs *WASMFileSystem) handleClose(id int64) error {
	closeFunc := wfs.module.ExportedFunction("handle_close")
	if closeFunc == nil {
		return fmt.Errorf("handle_close not implemented")
	}

	results, err := closeFunc.Call(wfs.ctx, uint64(id))
	if err != nil {
		return fmt.Errorf("handle_close failed: %w", err)
	}

	if len(results) > 0 && results[0] != 0 {
		errPtr := uint32(results[0])
		errMsg, ok := readStringFromMemory(wfs.module, errPtr)
		freeWASMMemory(wfs.module, errPtr, 0)
		if ok && errMsg != "" {
			return fmt.Errorf("close failed: %s", errMsg)
		}
		return fmt.Errorf("close failed")
	}

	return nil
}

func (wfs *WASMFileSystem) handleStat(id int64) (*filesystem.FileInfo, error) {
	statFunc := wfs.module.ExportedFunction("handle_stat")
	if statFunc == nil {
		return nil, fmt.Errorf("handle_stat not implemented")
	}

	results, err := statFunc.Call(wfs.ctx, uint64(id))
	if err != nil {
		return nil, fmt.Errorf("handle_stat failed: %w", err)
	}

	if len(results) < 1 {
		return nil, fmt.Errorf("handle_stat returned invalid results")
	}

	// Unpack u64: low 32 bits = json ptr, high 32 bits = error ptr
	packed := results[0]
	jsonPtr := uint32(packed & 0xFFFFFFFF)
	errPtr := uint32(packed >> 32)

	if errPtr != 0 {
		errMsg, ok := readStringFromMemory(wfs.module, errPtr)
		freeWASMMemory(wfs.module, errPtr, 0)
		if ok && errMsg != "" {
			return nil, fmt.Errorf("stat failed: %s", errMsg)
		}
		return nil, fmt.Errorf("stat failed")
	}

	if jsonPtr == 0 {
		return nil, fmt.Errorf("handle_stat returned null")
	}

	jsonStr, ok := readStringFromMemory(wfs.module, jsonPtr)
	freeWASMMemory(wfs.module, jsonPtr, 0)
	if !ok {
		return nil, fmt.Errorf("failed to read stat result")
	}

	var fileInfo filesystem.FileInfo
	if err := json.Unmarshal([]byte(jsonStr), &fileInfo); err != nil {
		return nil, fmt.Errorf("failed to unmarshal stat result: %w", err)
	}

	return &fileInfo, nil
}

// Helper types for Open/OpenWrite implementation

type wasmWriteCloser struct {
	fs   *WASMFileSystem
	path string
	buf  []byte
}

// WASMFileHandle implements filesystem.FileHandle for WASM plugins
// Note: id is the internal handle ID used by the WASM plugin (string)
// The ID() method returns a placeholder since WASMFileHandle is wrapped by PooledWASMFileHandle
type WASMFileHandle struct {
	wasmID int64 // WASM plugin's internal handle ID
	path   string
	flags  filesystem.OpenFlag
	wfs    *WASMFileSystem
	closed bool
}

// ID returns -1 since WASMFileHandle is always wrapped by PooledWASMFileHandle
// The real int64 ID is provided by PooledWASMFileHandle
func (h *WASMFileHandle) ID() int64 {
	return -1 // Should never be called directly; use PooledWASMFileHandle.ID()
}

// Path returns the file path
func (h *WASMFileHandle) Path() string {
	return h.path
}

// Flags returns the open flags
func (h *WASMFileHandle) Flags() filesystem.OpenFlag {
	return h.flags
}

// Read reads from the current position
func (h *WASMFileHandle) Read(buf []byte) (int, error) {
	if h.closed {
		return 0, fmt.Errorf("handle is closed")
	}
	return h.wfs.handleRead(h.wasmID, buf)
}

// ReadAt reads at a specific offset
func (h *WASMFileHandle) ReadAt(buf []byte, offset int64) (int, error) {
	if h.closed {
		return 0, fmt.Errorf("handle is closed")
	}
	return h.wfs.handleReadAt(h.wasmID, buf, offset)
}

// Write writes at the current position
func (h *WASMFileHandle) Write(data []byte) (int, error) {
	if h.closed {
		return 0, fmt.Errorf("handle is closed")
	}
	return h.wfs.handleWrite(h.wasmID, data)
}

// WriteAt writes at a specific offset
func (h *WASMFileHandle) WriteAt(data []byte, offset int64) (int, error) {
	if h.closed {
		return 0, fmt.Errorf("handle is closed")
	}
	return h.wfs.handleWriteAt(h.wasmID, data, offset)
}

// Seek changes the file position
func (h *WASMFileHandle) Seek(offset int64, whence int) (int64, error) {
	if h.closed {
		return 0, fmt.Errorf("handle is closed")
	}
	return h.wfs.handleSeek(h.wasmID, offset, whence)
}

// Sync flushes data to storage
func (h *WASMFileHandle) Sync() error {
	if h.closed {
		return fmt.Errorf("handle is closed")
	}
	return h.wfs.handleSync(h.wasmID)
}

// Close closes the handle
func (h *WASMFileHandle) Close() error {
	if h.closed {
		return nil
	}
	h.closed = true
	return h.wfs.handleClose(h.wasmID)
}

// Stat returns file info
func (h *WASMFileHandle) Stat() (*filesystem.FileInfo, error) {
	if h.closed {
		return nil, fmt.Errorf("handle is closed")
	}
	return h.wfs.handleStat(h.wasmID)
}

func (w *wasmWriteCloser) Write(p []byte) (n int, err error) {
	w.buf = append(w.buf, p...)
	return len(p), nil
}

func (w *wasmWriteCloser) Close() error {
	_, err := w.fs.Write(w.path, w.buf, -1, filesystem.WriteFlagCreate|filesystem.WriteFlagTruncate)
	return err
}

// Helper functions for memory management

// freeWASMMemory frees memory allocated in WASM module
// Supports both standard free(ptr) and Rust-style free(ptr, size)
// If size is 0, tries both calling conventions
// Does not free memory from shared buffers
func freeWASMMemory(module wazeroapi.Module, ptr uint32, size uint32) {
	freeWASMMemoryWithBuffer(module, ptr, size, nil)
}

func freeWASMMemoryWithBuffer(module wazeroapi.Module, ptr uint32, size uint32, bufInfo *SharedBufferInfo) {
	if ptr == 0 {
		return
	}

	// Don't free shared buffer memory
	if bufInfo != nil && bufInfo.Enabled {
		if ptr == bufInfo.InputBufferPtr || ptr == bufInfo.OutputBufferPtr {
			return // This is shared buffer memory, don't free
		}
	}

	freeFunc := module.ExportedFunction("free")
	if freeFunc == nil {
		// free function not available, skip silently
		// Memory will be reclaimed when instance is destroyed
		return
	}

	// Try calling with two parameters first (Rust-style: ptr, size)
	_, err := freeFunc.Call(context.Background(), uint64(ptr), uint64(size))
	if err != nil {
		// If that fails and size is 0, it might be standard C free(ptr)
		// Try with single parameter
		if size == 0 {
			_, err2 := freeFunc.Call(context.Background(), uint64(ptr))
			if err2 != nil {
				log.Debugf("free failed with both signatures: two-param(%v), one-param(%v)", err, err2)
			}
		} else {
			log.Debugf("free failed: %v", err)
		}
	}
}

// ReadStringFromWASMMemory is exported for use by wasm_loader
func ReadStringFromWASMMemory(module wazeroapi.Module, ptr uint32) (string, bool) {
	return readStringFromMemory(module, ptr)
}

func readStringFromMemory(module wazeroapi.Module, ptr uint32) (string, bool) {
	if ptr == 0 {
		return "", false
	}

	mem := module.Memory()
	if mem == nil {
		return "", false
	}

	// Read until null terminator
	var length uint32
	for {
		b, ok := mem.ReadByte(ptr + length)
		if !ok {
			return "", false
		}
		if b == 0 {
			break
		}
		length++
	}

	if length == 0 {
		return "", true
	}

	data, ok := mem.Read(ptr, length)
	if !ok {
		return "", false
	}

	return string(data), true
}

func writeStringToMemory(module wazeroapi.Module, s string) (ptr uint32, size uint32, err error) {
	return writeStringToMemoryWithBuffer(module, s, nil)
}

func writeStringToMemoryWithBuffer(module wazeroapi.Module, s string, bufInfo *SharedBufferInfo) (ptr uint32, size uint32, err error) {
	size = uint32(len(s) + 1) // +1 for null terminator
	data := append([]byte(s), 0)

	// Try to use shared buffer if available and data fits
	if bufInfo != nil && bufInfo.Enabled && size <= bufInfo.BufferSize {
		mem := module.Memory()
		if mem.Write(bufInfo.InputBufferPtr, data) {
			return bufInfo.InputBufferPtr, size, nil
		}
	}

	// Fall back to malloc for large data or if shared buffer not available
	allocFunc := module.ExportedFunction("malloc")
	if allocFunc == nil {
		return 0, 0, fmt.Errorf("malloc function not found in WASM module")
	}

	results, callErr := allocFunc.Call(context.Background(), uint64(size))
	if callErr != nil {
		return 0, 0, fmt.Errorf("malloc failed: %w", callErr)
	}

	if len(results) == 0 {
		return 0, 0, fmt.Errorf("malloc returned no results")
	}

	ptr = uint32(results[0])
	if ptr == 0 {
		return 0, 0, fmt.Errorf("malloc returned null pointer")
	}

	// Write string to memory
	mem := module.Memory()
	if !mem.Write(ptr, data) {
		return 0, 0, fmt.Errorf("failed to write string to memory")
	}

	return ptr, size, nil
}

func writeBytesToMemory(module wazeroapi.Module, data []byte) (ptr uint32, size uint32, err error) {
	return writeBytesToMemoryWithBuffer(module, data, nil)
}

func writeBytesToMemoryWithBuffer(module wazeroapi.Module, data []byte, bufInfo *SharedBufferInfo) (ptr uint32, size uint32, err error) {
	size = uint32(len(data))

	// Try to use shared buffer if available and data fits
	if bufInfo != nil && bufInfo.Enabled && size <= bufInfo.BufferSize {
		mem := module.Memory()
		if mem.Write(bufInfo.InputBufferPtr, data) {
			return bufInfo.InputBufferPtr, size, nil
		}
	}

	// Fall back to malloc for large data or if shared buffer not available
	allocFunc := module.ExportedFunction("malloc")
	if allocFunc == nil {
		return 0, 0, fmt.Errorf("malloc function not found in WASM module")
	}

	results, callErr := allocFunc.Call(context.Background(), uint64(size))
	if callErr != nil {
		return 0, 0, fmt.Errorf("malloc failed: %w", callErr)
	}

	if len(results) == 0 {
		return 0, 0, fmt.Errorf("malloc returned no results")
	}

	ptr = uint32(results[0])
	if ptr == 0 {
		return 0, 0, fmt.Errorf("malloc returned null pointer")
	}

	// Write data to memory
	mem := module.Memory()
	if !mem.Write(ptr, data) {
		return 0, 0, fmt.Errorf("failed to write bytes to memory")
	}

	return ptr, size, nil
}

