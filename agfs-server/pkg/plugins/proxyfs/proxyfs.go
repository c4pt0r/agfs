package proxyfs

import (
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	agfs "github.com/c4pt0r/agfs/agfs-sdk/go"
	"github.com/c4pt0r/agfs/agfs-server/pkg/filesystem"
	"github.com/c4pt0r/agfs/agfs-server/pkg/plugin"
)

const (
	PluginName = "proxyfs" // Name of this plugin
)

// Convert SDK FileInfo to server FileInfo
func convertFileInfo(src agfs.FileInfo) filesystem.FileInfo {
	return filesystem.FileInfo{
		Name:    src.Name,
		Size:    src.Size,
		Mode:    src.Mode,
		ModTime: src.ModTime,
		IsDir:   src.IsDir,
		Meta: filesystem.MetaData{
			Name:    src.Meta.Name,
			Type:    src.Meta.Type,
			Content: src.Meta.Content,
		},
	}
}

// Convert SDK FileInfo slice to server FileInfo slice
func convertFileInfos(src []agfs.FileInfo) []filesystem.FileInfo {
	result := make([]filesystem.FileInfo, len(src))
	for i, f := range src {
		result[i] = convertFileInfo(f)
	}
	return result
}

// ProxyFS implements filesystem.FileSystem by proxying to a remote AGFS HTTP API
// All file system operations are transparently forwarded to the remote server
type ProxyFS struct {
	client     atomic.Pointer[agfs.Client]
	pluginName string
	baseURL    string // Store base URL for reload
}

// NewProxyFS creates a new ProxyFS that redirects to a remote AGFS server
// baseURL should include the API version, e.g., "http://localhost:8080/api/v1"
func NewProxyFS(baseURL string, pluginName string) *ProxyFS {
	p := &ProxyFS{
		pluginName: pluginName,
		baseURL:    baseURL,
	}
	p.client.Store(agfs.NewClient(baseURL))
	return p
}

// Reload recreates the HTTP client, useful for refreshing connections
func (p *ProxyFS) Reload() error {
	// Create a new client to refresh the connection
	newClient := agfs.NewClient(p.baseURL)

	// Test the new connection
	if err := newClient.Health(); err != nil {
		return fmt.Errorf("failed to connect after reload: %w", err)
	}

	// Atomically replace the client
	p.client.Store(newClient)

	return nil
}

func (p *ProxyFS) Create(path string) error {
	return p.client.Load().Create(path)
}

func (p *ProxyFS) Mkdir(path string, perm uint32) error {
	return p.client.Load().Mkdir(path, perm)
}

func (p *ProxyFS) Remove(path string) error {
	return p.client.Load().Remove(path)
}

func (p *ProxyFS) RemoveAll(path string) error {
	return p.client.Load().RemoveAll(path)
}

func (p *ProxyFS) Read(path string, offset int64, size int64) ([]byte, error) {
	// Special handling for /reload
	if path == "/reload" {
		data := []byte("Write to this file to reload the proxy connection\n")
		return plugin.ApplyRangeRead(data, offset, size)
	}
	return p.client.Load().Read(path, offset, size)
}

func (p *ProxyFS) Write(path string, data []byte, offset int64, flags filesystem.WriteFlag) (int64, error) {
	// Special handling for /reload - trigger hot reload
	if path == "/reload" {
		if err := p.Reload(); err != nil {
			return 0, fmt.Errorf("reload failed: %w", err)
		}
		return int64(len(data)), nil
	}
	// Note: SDK client doesn't support new Write signature yet
	// For now, we ignore offset and flags and use the legacy method
	// TODO: Update SDK to support new Write signature
	_, err := p.client.Load().Write(path, data)
	if err != nil {
		return 0, err
	}
	return int64(len(data)), nil
}

func (p *ProxyFS) ReadDir(path string) ([]filesystem.FileInfo, error) {
	sdkFiles, err := p.client.Load().ReadDir(path)
	if err != nil {
		return nil, err
	}

	files := convertFileInfos(sdkFiles)

	// Add /reload virtual file to root directory listing
	if path == "/" {
		reloadFile := filesystem.FileInfo{
			Name:    "reload",
			Size:    0,
			Mode:    0o200,            // write-only
			ModTime: files[0].ModTime, // Use same time as first file
			IsDir:   false,
			Meta: filesystem.MetaData{
				Type: "control",
				Content: map[string]string{
					"description": "Write to this file to reload proxy connection",
				},
			},
		}
		files = append(files, reloadFile)
	}

	return files, nil
}

func (p *ProxyFS) Stat(path string) (*filesystem.FileInfo, error) {
	// Special handling for /reload
	if path == "/reload" {
		return &filesystem.FileInfo{
			Name:    "reload",
			Size:    0,
			Mode:    0o200, // write-only
			ModTime: time.Now(),
			IsDir:   false,
			Meta: filesystem.MetaData{
				Type: "control",
				Content: map[string]string{
					"description": "Write to this file to reload proxy connection",
					"remote-url":  p.baseURL,
				},
			},
		}, nil
	}

	// Get stat from remote
	sdkStat, err := p.client.Load().Stat(path)
	if err != nil {
		return nil, err
	}

	// Convert SDK FileInfo to server FileInfo
	stat := convertFileInfo(*sdkStat)

	// Add remote URL to metadata
	if stat.Meta.Content == nil {
		stat.Meta.Content = make(map[string]string)
	}
	stat.Meta.Content["remote-url"] = p.baseURL

	return &stat, nil
}

func (p *ProxyFS) Rename(oldPath, newPath string) error {
	return p.client.Load().Rename(oldPath, newPath)
}

func (p *ProxyFS) Chmod(path string, mode uint32) error {
	return p.client.Load().Chmod(path, mode)
}

func (p *ProxyFS) Open(path string) (io.ReadCloser, error) {
	data, err := p.client.Load().Read(path, 0, -1)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(io.Reader(newBytesReader(data))), nil
}

func (p *ProxyFS) OpenWrite(path string) (io.WriteCloser, error) {
	return filesystem.NewBufferedWriter(path, p.Write), nil
}

// OpenStream implements filesystem.Streamer interface
func (p *ProxyFS) OpenStream(path string) (filesystem.StreamReader, error) {
	// Use the client's ReadStream to get a streaming connection
	streamReader, err := p.client.Load().ReadStream(path)
	if err != nil {
		return nil, err
	}

	// Return a ProxyStreamReader that implements filesystem.StreamReader
	return &ProxyStreamReader{
		reader: streamReader,
		path:   path,
		buf:    make([]byte, 64*1024), // 64KB buffer for chunked reads
	}, nil
}

// GetStream returns a streaming reader for remote streamfs files
// Deprecated: Use OpenStream instead
func (p *ProxyFS) GetStream(path string) (interface{}, error) {
	// Use the client's ReadStream to get a streaming connection
	streamReader, err := p.client.Load().ReadStream(path)
	if err != nil {
		return nil, err
	}

	// Wrap the io.ReadCloser in a ProxyStream for backward compatibility
	return &ProxyStream{
		reader: streamReader,
		path:   path,
	}, nil
}

// ProxyStreamReader adapts an io.ReadCloser to filesystem.StreamReader
// It reads chunks from the remote stream with timeout support
type ProxyStreamReader struct {
	reader io.ReadCloser
	path   string
	buf    []byte // Buffer for reading chunks
}

// ReadChunk implements filesystem.StreamReader
func (psr *ProxyStreamReader) ReadChunk(timeout time.Duration) ([]byte, bool, error) {
	// Set read deadline if possible
	// Note: HTTP response bodies don't support deadlines, so timeout is best-effort

	// Read a chunk from the stream
	n, err := psr.reader.Read(psr.buf)

	if n > 0 {
		// Make a copy of the data to return
		chunk := make([]byte, n)
		copy(chunk, psr.buf[:n])
		return chunk, false, nil
	}

	if err == io.EOF {
		return nil, true, io.EOF
	}

	if err != nil {
		return nil, false, err
	}

	// No data and no error - unlikely but handle it
	return nil, false, fmt.Errorf("read timeout")
}

// Close implements filesystem.StreamReader
func (psr *ProxyStreamReader) Close() error {
	return psr.reader.Close()
}

// ProxyStream wraps an io.ReadCloser to provide streaming functionality
// Deprecated: Used for backward compatibility with old GetStream interface
type ProxyStream struct {
	reader io.ReadCloser
	path   string
}

// Read implements io.Reader
func (ps *ProxyStream) Read(p []byte) (n int, err error) {
	return ps.reader.Read(p)
}

// Close implements io.Closer
func (ps *ProxyStream) Close() error {
	return ps.reader.Close()
}

// bytesReader wraps a byte slice to implement io.Reader
type bytesReader struct {
	data []byte
	pos  int
}

func newBytesReader(data []byte) *bytesReader {
	return &bytesReader{data: data, pos: 0}
}

func (r *bytesReader) Read(p []byte) (n int, err error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n = copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

// ProxyFSPlugin wraps ProxyFS as a plugin that can be mounted in AGFS
// It enables remote file system access through the AGFS plugin system
type ProxyFSPlugin struct {
	fs      *ProxyFS
	baseURL string
}

// NewProxyFSPlugin creates a new ProxyFS plugin
// baseURL should be the full API endpoint, e.g., "http://remote-server:8080/api/v1"
func NewProxyFSPlugin(baseURL string) *ProxyFSPlugin {
	return &ProxyFSPlugin{
		baseURL: baseURL,
		fs:      NewProxyFS(baseURL, PluginName),
	}
}

func (p *ProxyFSPlugin) Name() string {
	return PluginName
}

func (p *ProxyFSPlugin) Validate(cfg map[string]interface{}) error {
	// Check for unknown parameters
	allowedKeys := []string{"base_url", "mount_path"}
	if cfg != nil {
		for key := range cfg {
			found := false
			for _, allowed := range allowedKeys {
				if key == allowed {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("unknown configuration parameter: %s (allowed: %v)", key, allowedKeys)
			}
		}
	}

	// base_url is required (either from constructor or config)
	baseURL := p.baseURL
	if cfg != nil {
		if u, ok := cfg["base_url"].(string); ok && u != "" {
			baseURL = u
		}
	}

	if baseURL == "" {
		return fmt.Errorf("base_url is required in configuration")
	}

	// Validate URL format
	if _, err := url.Parse(baseURL); err != nil {
		return fmt.Errorf("invalid base_url format: %w", err)
	}

	return nil
}

func (p *ProxyFSPlugin) Initialize(config map[string]interface{}) error {
	// Override base URL if provided in config
	// Expected config: {"base_url": "http://remote-server:8080/api/v1"}
	if config != nil {
		if url, ok := config["base_url"].(string); ok && url != "" {
			p.baseURL = url
			p.fs = NewProxyFS(url, PluginName)
		}
	}

	// Validate that we have a base URL
	if p.baseURL == "" {
		return fmt.Errorf("base_url is required in configuration")
	}

	// Validate that the base URL is properly formatted
	// Check for protocol separator to catch common mistakes like "http:" instead of "http://host"
	if !strings.Contains(p.baseURL, "://") {
		return fmt.Errorf("invalid base_url format: %s (expected format: http://hostname:port or http://hostname:port/api/v1). Did you forget to quote the URL?", p.baseURL)
	}

	// Test connection to remote server with health check
	if err := p.fs.client.Load().Health(); err != nil {
		return fmt.Errorf("failed to connect to remote AGFS server at %s: %w", p.baseURL, err)
	}

	return nil
}

func (p *ProxyFSPlugin) GetFileSystem() filesystem.FileSystem {
	return p.fs
}

func (p *ProxyFSPlugin) GetReadme() string {
	return `ProxyFS Plugin - Remote AGFS Proxy

This plugin proxies all file system operations to a remote AGFS HTTP API server.

FEATURES:
  - Transparent proxying of all file system operations
  - Full compatibility with AGFS HTTP API
  - Connects to remote AGFS servers
  - Supports all standard file operations
  - Supports streaming operations (cat --stream)
  - Transparent proxying of remote streamfs
  - Implements filesystem.Streamer interface

CONFIGURATION:
  base_url: URL of the remote AGFS server (e.g., "http://remote:8080/api/v1")

HOT RELOAD:
  ProxyFS provides a special /reload file for hot-reloading the connection:

  Echo to /reload to refresh the proxy connection:
    echo '' > /proxyfs/reload

  This is useful when:
  - Remote server was restarted
  - Network connection was interrupted
  - Need to refresh connection pool

USAGE:
  All standard file operations are proxied to the remote server:

  Create a file:
    touch /path/to/file

  Write to a file:
    echo "content" > /path/to/file

  Read a file:
    cat /path/to/file

  Create a directory:
    mkdir /path/to/dir

  List directory:
    ls /path/to/dir

  Remove file/directory:
    rm /path/to/file
    rm -r /path/to/dir

  Move/rename:
    mv /old/path /new/path

  Change permissions:
    chmod 755 /path/to/file

STREAMING SUPPORT:
  ProxyFS transparently proxies streaming operations to remote AGFS servers.

  Access remote streamfs:
    p cat --stream /proxyfs/remote/streamfs/video | ffplay - 

  Write to remote streamfs:
    cat file.mp4 | p write --stream /proxyfs/remote/streamfs/video

  All streaming features from remote streamfs are fully supported:
  - Real-time data streaming
  - Ring buffer with historical data
  - Multiple concurrent readers (fanout)
  - Persistent connections (no timeout disconnect)

EXAMPLES:
  # Standard file operations
  agfs:/> mkdir /proxyfs/remote/data
  agfs:/> echo "hello" > /proxyfs/remote/data/file.txt
  agfs:/> cat /proxyfs/remote/data/file.txt
  hello
  agfs:/> ls /proxyfs/remote/data

  # Streaming operations (outside REPL)
  $ p cat --stream /proxyfs/remote/streamfs/logs
  $ cat video.mp4 | p write --stream /proxyfs/remote/streamfs/video

USE CASES:
  - Connect to remote AGFS instances
  - Federation of multiple AGFS servers
  - Access remote services through local mount points
  - Distributed file system scenarios
  - Stream video/audio from remote streamfs
  - Remote real-time data streaming

`
}

func (p *ProxyFSPlugin) GetConfigParams() []plugin.ConfigParameter {
	return []plugin.ConfigParameter{
		{
			Name:        "base_url",
			Type:        "string",
			Required:    true,
			Default:     "",
			Description: "Base URL of the remote AGFS server (e.g., http://localhost:8080)",
		},
	}
}

func (p *ProxyFSPlugin) Shutdown() error {
	return nil
}

// Ensure ProxyFSPlugin implements ServicePlugin
var _ plugin.ServicePlugin = (*ProxyFSPlugin)(nil)