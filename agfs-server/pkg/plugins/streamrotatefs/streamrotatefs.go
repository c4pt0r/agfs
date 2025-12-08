package streamrotatefs

import (
	"bytes"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/c4pt0r/agfs/agfs-server/pkg/filesystem"
	"github.com/c4pt0r/agfs/agfs-server/pkg/plugin"
	"github.com/c4pt0r/agfs/agfs-server/pkg/plugin/config"
	log "github.com/sirupsen/logrus"
)

const (
	PluginName = "streamrotatefs" // Name of this plugin
)

// parseSize parses a size string like "512KB", "1MB", "100MB" and returns bytes
func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToUpper(s))

	// Handle pure numbers (bytes)
	if val, err := strconv.ParseInt(s, 10, 64); err == nil {
		return val, nil
	}

	// Parse with unit suffix
	units := map[string]int64{
		"B":  1,
		"KB": 1024,
		"MB": 1024 * 1024,
		"GB": 1024 * 1024 * 1024,
	}

	for suffix, multiplier := range units {
		if strings.HasSuffix(s, suffix) {
			numStr := strings.TrimSuffix(s, suffix)
			numStr = strings.TrimSpace(numStr)

			// Try parsing as float first (for "1.5MB")
			if val, err := strconv.ParseFloat(numStr, 64); err == nil {
				return int64(val * float64(multiplier)), nil
			}
		}
	}

	return 0, fmt.Errorf("invalid size format: %s (expected format: 512KB, 1MB, etc)", s)
}

// parseDuration parses a duration string like "5m", "1h", "30s"
func parseDuration(s string) (time.Duration, error) {
	return time.ParseDuration(s)
}

// formatSize formats bytes into human-readable format
func formatSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%dB", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	units := []string{"KB", "MB", "GB", "TB"}
	if exp >= len(units) {
		exp = len(units) - 1
	}
	return fmt.Sprintf("%.1f%s", float64(bytes)/float64(div), units[exp])
}

// Reader represents a single reader with its channel and metadata
type Reader struct {
	id           string
	ch           chan []byte
	registered   time.Time
	droppedCount int64
	readIndex    int64
}

// streamReader wraps a registered reader and implements filesystem.StreamReader
type streamReader struct {
	rsf      *RotateStreamFile
	readerID string
	ch       <-chan []byte
}

// ReadChunk implements filesystem.StreamReader
func (sr *streamReader) ReadChunk(timeout time.Duration) ([]byte, bool, error) {
	return sr.rsf.ReadChunk(sr.readerID, sr.ch, timeout)
}

// Close implements filesystem.StreamReader
func (sr *streamReader) Close() error {
	sr.rsf.UnregisterReader(sr.readerID)
	return nil
}

// RotationConfig holds configuration for file rotation
type RotationConfig struct {
	RotationInterval time.Duration // Time-based rotation interval
	RotationSize     int64         // Size-based rotation threshold (bytes)
	OutputPath       string        // Output directory path (agfs path like /s3fs/bucket)
	FilenamePattern  string        // Filename pattern with variables
}

// RotateStreamFile represents a streaming file with rotation support
type RotateStreamFile struct {
	name          string
	channel       string // Channel name (extracted from path)
	mu            sync.RWMutex
	offset        int64              // Total bytes written
	closed        bool               // Whether the stream is closed
	modTime       time.Time          // Last modification time
	readers       map[string]*Reader // All registered readers
	nextReaderID  int                // Auto-increment reader ID
	channelBuffer int                // Buffer size for each reader channel

	// Ring buffer for storing recent chunks
	ringBuffer  [][]byte
	ringSize    int
	writeIndex  int64
	totalChunks int64

	// Rotation-specific fields
	config            RotationConfig
	currentWriter     io.WriteCloser // Current output file writer (can be os.File or agfs writer)
	currentFileSize   int64          // Size of current rotation file
	fileIndex         int64          // Rotation file index
	rotationTimer     *time.Timer    // Timer for time-based rotation
	stopRotation      chan bool      // Signal to stop rotation goroutine
	currentFilePath   string         // Current output file path
	parentFS          filesystem.FileSystem // Reference to parent agfs filesystem
}

// NewRotateStreamFile creates a new rotate stream file
func NewRotateStreamFile(name string, channelBuffer int, ringSize int, config RotationConfig, parentFS filesystem.FileSystem) *RotateStreamFile {
	if channelBuffer <= 0 {
		channelBuffer = 100
	}
	if ringSize <= 0 {
		ringSize = 100
	}

	// Extract channel name from path
	channel := path.Base(name)

	rsf := &RotateStreamFile{
		name:          name,
		channel:       channel,
		modTime:       time.Now(),
		readers:       make(map[string]*Reader),
		nextReaderID:  0,
		channelBuffer: channelBuffer,
		ringBuffer:    make([][]byte, ringSize),
		ringSize:      ringSize,
		writeIndex:    0,
		totalChunks:   0,
		config:        config,
		fileIndex:     0,
		stopRotation:  make(chan bool),
		parentFS:      parentFS,
	}

	// Start rotation timer if interval is configured
	if config.RotationInterval > 0 {
		rsf.startRotationTimer()
	}

	return rsf
}

// startRotationTimer starts a goroutine for time-based rotation
func (rsf *RotateStreamFile) startRotationTimer() {
	go func() {
		for {
			select {
			case <-time.After(rsf.config.RotationInterval):
				rsf.mu.Lock()
				if !rsf.closed && rsf.currentWriter != nil {
					log.Infof("[streamrotatefs] Time-based rotation triggered for %s", rsf.name)
					rsf.rotateFile()
				}
				rsf.mu.Unlock()
			case <-rsf.stopRotation:
				return
			}
		}
	}()
}

// generateFilename generates a filename based on the pattern
func (rsf *RotateStreamFile) generateFilename() string {
	pattern := rsf.config.FilenamePattern
	if pattern == "" {
		pattern = "{channel}_{timestamp}.dat"
	}

	now := time.Now()
	replacements := map[string]string{
		"{channel}":   rsf.channel,
		"{timestamp}": fmt.Sprintf("%d", now.Unix()),
		"{date}":      now.Format("20060102"),
		"{time}":      now.Format("150405"),
		"{index}":     fmt.Sprintf("%06d", rsf.fileIndex),
		"{datetime}":  now.Format("20060102_150405"),
	}

	filename := pattern
	for key, value := range replacements {
		filename = strings.ReplaceAll(filename, key, value)
	}

	return filename
}

// rotateFile closes current file and creates a new one
func (rsf *RotateStreamFile) rotateFile() error {
	// Close current file if exists
	if rsf.currentWriter != nil {
		if err := rsf.currentWriter.Close(); err != nil {
			log.Errorf("[streamrotatefs] Error closing current file: %v", err)
		}
		rsf.currentWriter = nil
		rsf.currentFileSize = 0
	}

	if rsf.parentFS == nil {
		return fmt.Errorf("parent filesystem not set, cannot write rotation files")
	}

	// Generate new filename
	filename := rsf.generateFilename()
	outputPath := path.Join(rsf.config.OutputPath, filename)

	// Create parent directories if needed (for patterns like {date}/{channel}.dat)
	parentDir := path.Dir(outputPath)
	if parentDir != rsf.config.OutputPath && parentDir != "/" {
		// Check if parent directory exists in agfs
		if _, err := rsf.parentFS.Stat(parentDir); err != nil {
			// Try to create parent directory
			if err := rsf.parentFS.Mkdir(parentDir, 0755); err != nil {
				log.Warnf("[streamrotatefs] Could not create parent directory %s: %v", parentDir, err)
			}
		}
	}

	// Create file in agfs
	if err := rsf.parentFS.Create(outputPath); err != nil {
		log.Errorf("[streamrotatefs] Error creating agfs file %s: %v", outputPath, err)
		return err
	}

	// Open for writing
	writer, err := rsf.parentFS.OpenWrite(outputPath)
	if err != nil {
		log.Errorf("[streamrotatefs] Error opening agfs file for write %s: %v", outputPath, err)
		return err
	}

	rsf.currentWriter = writer
	rsf.currentFilePath = outputPath
	rsf.fileIndex++

	log.Infof("[streamrotatefs] Rotated to new file: %s (index: %d)", outputPath, rsf.fileIndex)
	return nil
}

// RegisterReader registers a new reader and returns reader ID and channel
func (rsf *RotateStreamFile) RegisterReader() (string, <-chan []byte) {
	rsf.mu.Lock()
	defer rsf.mu.Unlock()

	readerID := fmt.Sprintf("reader_%d_%d", rsf.nextReaderID, time.Now().UnixNano())
	rsf.nextReaderID++

	historyStart := rsf.totalChunks - int64(rsf.ringSize)
	if historyStart < 0 {
		historyStart = 0
	}

	reader := &Reader{
		id:           readerID,
		ch:           make(chan []byte, rsf.channelBuffer),
		registered:   time.Now(),
		droppedCount: 0,
		readIndex:    historyStart,
	}
	rsf.readers[readerID] = reader

	log.Infof("[streamrotatefs] Registered reader %s for stream %s", readerID, rsf.name)

	// Send historical data
	go rsf.sendHistoricalData(reader)

	return readerID, reader.ch
}

// sendHistoricalData sends historical chunks from ring buffer to a new reader
func (rsf *RotateStreamFile) sendHistoricalData(reader *Reader) {
	rsf.mu.RLock()
	defer rsf.mu.RUnlock()

	historyStart := rsf.totalChunks - int64(rsf.ringSize)
	if historyStart < 0 {
		historyStart = 0
	}

	if reader.readIndex < rsf.totalChunks && rsf.totalChunks > 0 {
		for i := historyStart; i < rsf.totalChunks; i++ {
			ringIdx := int(i % int64(rsf.ringSize))
			if rsf.ringBuffer[ringIdx] != nil {
				select {
				case reader.ch <- rsf.ringBuffer[ringIdx]:
					// Sent successfully
				default:
					log.Warnf("[streamrotatefs] Reader %s channel full during historical data send", reader.id)
					return
				}
			}
		}
	}
}

// UnregisterReader unregisters a reader and closes its channel
func (rsf *RotateStreamFile) UnregisterReader(readerID string) {
	rsf.mu.Lock()
	defer rsf.mu.Unlock()

	if reader, exists := rsf.readers[readerID]; exists {
		close(reader.ch)
		delete(rsf.readers, readerID)
		log.Infof("[streamrotatefs] Unregistered reader %s for stream %s", readerID, rsf.name)
	}
}

// Write appends data to the stream, writes to rotation file, and fanout to all readers
func (rsf *RotateStreamFile) Write(data []byte) error {
	rsf.mu.Lock()

	if rsf.closed {
		rsf.mu.Unlock()
		return fmt.Errorf("stream is closed")
	}

	// Check if we need to rotate based on size
	if rsf.config.RotationSize > 0 && rsf.currentWriter != nil {
		if rsf.currentFileSize+int64(len(data)) > rsf.config.RotationSize {
			log.Infof("[streamrotatefs] Size-based rotation triggered for %s (current: %d, threshold: %d)",
				rsf.name, rsf.currentFileSize, rsf.config.RotationSize)
			rsf.rotateFile()
		}
	}

	// Create first rotation file if needed
	if rsf.currentWriter == nil {
		if err := rsf.rotateFile(); err != nil {
			rsf.mu.Unlock()
			return fmt.Errorf("failed to create rotation file: %w", err)
		}
	}

	// Write to rotation file
	if rsf.currentWriter != nil {
		n, err := rsf.currentWriter.Write(data)
		if err != nil {
			log.Errorf("[streamrotatefs] Error writing to rotation file: %v", err)
		} else {
			rsf.currentFileSize += int64(n)
		}
	}

	// Copy data to avoid external modification
	chunk := make([]byte, len(data))
	copy(chunk, data)

	rsf.offset += int64(len(data))
	rsf.modTime = time.Now()

	// Store in ring buffer
	ringIdx := int(rsf.writeIndex % int64(rsf.ringSize))
	rsf.ringBuffer[ringIdx] = chunk
	rsf.writeIndex++
	rsf.totalChunks++

	// Take snapshot of readers
	readerSnapshot := make([]*Reader, 0, len(rsf.readers))
	for _, reader := range rsf.readers {
		readerSnapshot = append(readerSnapshot, reader)
	}

	rsf.mu.Unlock()

	// Fanout to all readers
	for _, reader := range readerSnapshot {
		select {
		case reader.ch <- chunk:
			// Sent successfully
		default:
			reader.droppedCount++
			log.Warnf("[streamrotatefs] Reader %s is slow, dropped chunk", reader.id)
		}
	}

	return nil
}

// ReadChunk reads data from a reader's channel
func (rsf *RotateStreamFile) ReadChunk(readerID string, ch <-chan []byte, timeout time.Duration) ([]byte, bool, error) {
	select {
	case data, ok := <-ch:
		if !ok {
			return nil, true, io.EOF
		}
		return data, false, nil
	case <-time.After(timeout):
		rsf.mu.RLock()
		closed := rsf.closed
		rsf.mu.RUnlock()

		if closed {
			return nil, true, io.EOF
		}
		return nil, false, fmt.Errorf("read timeout")
	}
}

// Close closes the stream and all reader channels
func (rsf *RotateStreamFile) Close() error {
	rsf.mu.Lock()
	defer rsf.mu.Unlock()

	rsf.closed = true

	// Stop rotation timer
	if rsf.config.RotationInterval > 0 {
		close(rsf.stopRotation)
	}

	// Close current rotation file
	if rsf.currentWriter != nil {
		rsf.currentWriter.Close()
		rsf.currentWriter = nil
	}

	// Close all reader channels
	for id, reader := range rsf.readers {
		close(reader.ch)
		log.Infof("[streamrotatefs] Closed reader %s for stream %s", id, rsf.name)
	}
	rsf.readers = make(map[string]*Reader)

	log.Infof("[streamrotatefs] Stream %s closed", rsf.name)
	return nil
}

// GetInfo returns file info
func (rsf *RotateStreamFile) GetInfo() filesystem.FileInfo {
	rsf.mu.RLock()
	defer rsf.mu.RUnlock()

	name := rsf.name
	if len(name) > 0 && name[0] == '/' {
		name = name[1:]
	}

	return filesystem.FileInfo{
		Name:    name,
		Size:    rsf.offset,
		Mode:    0644,
		ModTime: rsf.modTime,
		IsDir:   false,
		Meta: filesystem.MetaData{
			Name: PluginName,
			Type: "rotate-stream",
			Content: map[string]string{
				"total_written":      fmt.Sprintf("%d", rsf.offset),
				"active_readers":     fmt.Sprintf("%d", len(rsf.readers)),
				"current_file_size":  fmt.Sprintf("%d", rsf.currentFileSize),
				"rotation_file_idx":  fmt.Sprintf("%d", rsf.fileIndex),
				"rotation_threshold": formatSize(rsf.config.RotationSize),
			},
		},
	}
}

// StreamRotateFS implements FileSystem interface for rotating streaming files
type StreamRotateFS struct {
	streams       map[string]*RotateStreamFile
	mu            sync.RWMutex
	channelBuffer int
	ringSize      int
	rotationCfg   RotationConfig
	pluginName    string
	parentFS      filesystem.FileSystem // Reference to parent agfs filesystem
}

// NewStreamRotateFS creates a new StreamRotateFS
func NewStreamRotateFS(channelBuffer int, ringSize int, rotationCfg RotationConfig, parentFS filesystem.FileSystem) *StreamRotateFS {
	if channelBuffer <= 0 {
		channelBuffer = 100
	}
	if ringSize <= 0 {
		ringSize = 100
	}
	return &StreamRotateFS{
		streams:       make(map[string]*RotateStreamFile),
		channelBuffer: channelBuffer,
		ringSize:      ringSize,
		rotationCfg:   rotationCfg,
		pluginName:    PluginName,
		parentFS:      parentFS,
	}
}

func (srf *StreamRotateFS) Create(path string) error {
	srf.mu.Lock()
	defer srf.mu.Unlock()

	if _, exists := srf.streams[path]; exists {
		return fmt.Errorf("stream already exists: %s", path)
	}

	srf.streams[path] = NewRotateStreamFile(path, srf.channelBuffer, srf.ringSize, srf.rotationCfg, srf.parentFS)
	return nil
}

func (srf *StreamRotateFS) Mkdir(path string, perm uint32) error {
	return fmt.Errorf("streamrotatefs does not support directories")
}

func (srf *StreamRotateFS) Remove(path string) error {
	srf.mu.Lock()
	defer srf.mu.Unlock()

	stream, exists := srf.streams[path]
	if !exists {
		return fmt.Errorf("stream not found: %s", path)
	}

	stream.Close()
	delete(srf.streams, path)
	return nil
}

func (srf *StreamRotateFS) RemoveAll(path string) error {
	return srf.Remove(path)
}

func (srf *StreamRotateFS) Read(path string, offset int64, size int64) ([]byte, error) {
	if path == "/README" {
		content := []byte(getReadme())
		return plugin.ApplyRangeRead(content, offset, size)
	}

	return nil, fmt.Errorf("use stream mode for reading stream files")
}

func (srf *StreamRotateFS) Write(path string, data []byte) ([]byte, error) {
	srf.mu.Lock()
	stream, exists := srf.streams[path]
	if !exists {
		stream = NewRotateStreamFile(path, srf.channelBuffer, srf.ringSize, srf.rotationCfg, srf.parentFS)
		srf.streams[path] = stream
	}
	srf.mu.Unlock()

	err := stream.Write(data)
	if err != nil {
		return nil, err
	}

	return []byte(fmt.Sprintf("Written %d bytes to rotate stream", len(data))), nil
}

func (srf *StreamRotateFS) ReadDir(path string) ([]filesystem.FileInfo, error) {
	if path != "/" {
		return nil, fmt.Errorf("not a directory: %s", path)
	}

	srf.mu.RLock()
	defer srf.mu.RUnlock()

	readme := filesystem.FileInfo{
		Name:    "README",
		Size:    int64(len(getReadme())),
		Mode:    0444,
		ModTime: time.Now(),
		IsDir:   false,
		Meta: filesystem.MetaData{
			Name: PluginName,
			Type: "doc",
		},
	}

	files := []filesystem.FileInfo{readme}
	for _, stream := range srf.streams {
		files = append(files, stream.GetInfo())
	}

	return files, nil
}

func (srf *StreamRotateFS) Stat(path string) (*filesystem.FileInfo, error) {
	if path == "/" {
		info := &filesystem.FileInfo{
			Name:    "/",
			Size:    0,
			Mode:    0755,
			ModTime: time.Now(),
			IsDir:   true,
			Meta: filesystem.MetaData{
				Name: PluginName,
			},
		}
		return info, nil
	}

	if path == "/README" {
		readme := getReadme()
		info := &filesystem.FileInfo{
			Name:    "README",
			Size:    int64(len(readme)),
			Mode:    0444,
			ModTime: time.Now(),
			IsDir:   false,
			Meta: filesystem.MetaData{
				Name: PluginName,
				Type: "doc",
			},
		}
		return info, nil
	}

	srf.mu.RLock()
	stream, exists := srf.streams[path]
	srf.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("stream not found: %s", path)
	}

	info := stream.GetInfo()
	return &info, nil
}

func (srf *StreamRotateFS) Rename(oldPath, newPath string) error {
	return fmt.Errorf("streamrotatefs does not support rename")
}

func (srf *StreamRotateFS) Chmod(path string, mode uint32) error {
	return fmt.Errorf("streamrotatefs does not support chmod")
}

func (srf *StreamRotateFS) Open(path string) (io.ReadCloser, error) {
	if path == "/README" {
		return io.NopCloser(bytes.NewReader([]byte(getReadme()))), nil
	}
	return nil, fmt.Errorf("use stream mode for reading stream files")
}

func (srf *StreamRotateFS) OpenWrite(path string) (io.WriteCloser, error) {
	return &streamWriter{srf: srf, path: path}, nil
}

// OpenStream implements filesystem.Streamer interface
func (srf *StreamRotateFS) OpenStream(path string) (filesystem.StreamReader, error) {
	srf.mu.Lock()
	stream, exists := srf.streams[path]
	if !exists {
		stream = NewRotateStreamFile(path, srf.channelBuffer, srf.ringSize, srf.rotationCfg, srf.parentFS)
		srf.streams[path] = stream
		log.Infof("[streamrotatefs] Auto-created stream %s for reader", path)
	}
	srf.mu.Unlock()

	readerID, ch := stream.RegisterReader()
	log.Infof("[streamrotatefs] Opened stream %s with reader %s", path, readerID)

	return &streamReader{
		rsf:      stream,
		readerID: readerID,
		ch:       ch,
	}, nil
}

// SetParentFS sets the parent filesystem reference
// This must be called after the plugin is initialized to enable agfs output
func (srf *StreamRotateFS) SetParentFS(fs filesystem.FileSystem) {
	srf.mu.Lock()
	defer srf.mu.Unlock()
	srf.parentFS = fs
	log.Infof("[streamrotatefs] Parent filesystem set, agfs output enabled")
}

type streamWriter struct {
	srf  *StreamRotateFS
	path string
}

func (sw *streamWriter) Write(p []byte) (n int, err error) {
	_, err = sw.srf.Write(sw.path, p)
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

func (sw *streamWriter) Close() error {
	return nil
}

// StreamRotateFSPlugin wraps StreamRotateFS as a plugin
type StreamRotateFSPlugin struct {
	fs            *StreamRotateFS
	channelBuffer int
	ringSize      int
	rotationCfg   RotationConfig
}

// NewStreamRotateFSPlugin creates a new StreamRotateFS plugin
func NewStreamRotateFSPlugin() *StreamRotateFSPlugin {
	return &StreamRotateFSPlugin{
		channelBuffer: 100,
		ringSize:      100,
		rotationCfg: RotationConfig{
			RotationInterval: 0,
			RotationSize:     100 * 1024 * 1024, // Default: 100MB
			OutputPath:       "/localfs/rotated_files",
			FilenamePattern:  "{channel}_{timestamp}.dat",
		},
	}
}

func (p *StreamRotateFSPlugin) Name() string {
	return PluginName
}

func (p *StreamRotateFSPlugin) Validate(cfg map[string]interface{}) error {
	allowedKeys := []string{
		"channel_buffer_size", "ring_buffer_size",
		"rotation_interval", "rotation_size",
		"output_path", "filename_pattern",
		"mount_path",
	}
	if err := config.ValidateOnlyKnownKeys(cfg, allowedKeys); err != nil {
		return err
	}

	// Validate rotation_interval if provided
	if val, exists := cfg["rotation_interval"]; exists {
		if strVal, ok := val.(string); ok {
			if _, err := parseDuration(strVal); err != nil {
				return fmt.Errorf("invalid rotation_interval: %w", err)
			}
		} else {
			return fmt.Errorf("rotation_interval must be a duration string (e.g., '5m', '1h')")
		}
	}

	// Validate rotation_size if provided
	if val, exists := cfg["rotation_size"]; exists {
		switch v := val.(type) {
		case string:
			if _, err := config.ParseSize(v); err != nil {
				return fmt.Errorf("invalid rotation_size: %w", err)
			}
		case int, int64, float64:
			// Valid numeric types
		default:
			return fmt.Errorf("rotation_size must be a size string (e.g., '100MB') or number")
		}
	}

	// Validate output_path is required and must be an agfs path
	if val, exists := cfg["output_path"]; !exists {
		return fmt.Errorf("output_path is required")
	} else if strVal, ok := val.(string); !ok {
		return fmt.Errorf("output_path must be a string")
	} else if !strings.HasPrefix(strVal, "/") {
		return fmt.Errorf("output_path must be an agfs path (must start with /), e.g., /s3fs/bucket or /localfs/data")
	}

	return nil
}

func (p *StreamRotateFSPlugin) Initialize(cfg map[string]interface{}) error {
	const defaultChunkSize = 64 * 1024

	// Parse channel buffer size
	channelBufferBytes := int64(6 * 1024 * 1024)
	if val, ok := cfg["channel_buffer_size"]; ok {
		if parsed, err := config.ParseSize(fmt.Sprintf("%v", val)); err == nil {
			channelBufferBytes = parsed
		}
	}

	// Parse ring buffer size
	ringBufferBytes := int64(6 * 1024 * 1024)
	if val, ok := cfg["ring_buffer_size"]; ok {
		if parsed, err := config.ParseSize(fmt.Sprintf("%v", val)); err == nil {
			ringBufferBytes = parsed
		}
	}

	// Parse rotation interval
	p.rotationCfg.RotationInterval = 0
	if val, ok := cfg["rotation_interval"].(string); ok {
		if duration, err := parseDuration(val); err == nil {
			p.rotationCfg.RotationInterval = duration
		}
	}

	// Parse rotation size
	p.rotationCfg.RotationSize = 100 * 1024 * 1024 // Default: 100MB
	if val, ok := cfg["rotation_size"]; ok {
		if parsed, err := config.ParseSize(fmt.Sprintf("%v", val)); err == nil {
			p.rotationCfg.RotationSize = parsed
		}
	}

	// Parse output path (required, must be agfs path)
	if val, ok := cfg["output_path"].(string); ok {
		p.rotationCfg.OutputPath = val
	} else {
		return fmt.Errorf("output_path is required")
	}

	// Parse filename pattern
	p.rotationCfg.FilenamePattern = "{channel}_{timestamp}.dat"
	if val, ok := cfg["filename_pattern"].(string); ok {
		p.rotationCfg.FilenamePattern = val
	}

	// Convert bytes to chunks
	p.channelBuffer = int(channelBufferBytes / defaultChunkSize)
	if p.channelBuffer < 1 {
		p.channelBuffer = 1
	}

	p.ringSize = int(ringBufferBytes / defaultChunkSize)
	if p.ringSize < 1 {
		p.ringSize = 1
	}

	// Create filesystem (parentFS will be set later via SetParentFS)
	p.fs = NewStreamRotateFS(p.channelBuffer, p.ringSize, p.rotationCfg, nil)

	log.Infof("[streamrotatefs] Initialized with rotation_size=%s, rotation_interval=%s, output_path=%s, pattern=%s",
		formatSize(p.rotationCfg.RotationSize),
		p.rotationCfg.RotationInterval,
		p.rotationCfg.OutputPath,
		p.rotationCfg.FilenamePattern)

	return nil
}

// SetParentFileSystem sets the parent filesystem for agfs output
// This should be called by the mount system after initialization
func (p *StreamRotateFSPlugin) SetParentFileSystem(fs filesystem.FileSystem) {
	if p.fs != nil {
		p.fs.SetParentFS(fs)
	}
}

func (p *StreamRotateFSPlugin) GetFileSystem() filesystem.FileSystem {
	return p.fs
}

func (p *StreamRotateFSPlugin) GetReadme() string {
	return getReadme()
}

func (p *StreamRotateFSPlugin) GetConfigParams() []plugin.ConfigParameter {
	return []plugin.ConfigParameter{
		{
			Name:        "channel_buffer_size",
			Type:        "string",
			Required:    false,
			Default:     "6MB",
			Description: "Channel buffer size per reader (e.g., '512KB', '6MB')",
		},
		{
			Name:        "ring_buffer_size",
			Type:        "string",
			Required:    false,
			Default:     "6MB",
			Description: "Ring buffer size for historical data (e.g., '1MB', '6MB')",
		},
		{
			Name:        "rotation_interval",
			Type:        "string",
			Required:    false,
			Default:     "",
			Description: "Time-based rotation interval (e.g., '5m', '1h', '24h'). Empty = disabled",
		},
		{
			Name:        "rotation_size",
			Type:        "string",
			Required:    false,
			Default:     "100MB",
			Description: "Size-based rotation threshold (e.g., '100MB', '1GB')",
		},
		{
			Name:        "output_path",
			Type:        "string",
			Required:    true,
			Default:     "/localfs/rotated_files",
			Description: "Output agfs path (e.g., /s3fs/bucket or /localfs/data) for rotated files",
		},
		{
			Name:        "filename_pattern",
			Type:        "string",
			Required:    false,
			Default:     "{channel}_{timestamp}.dat",
			Description: "Filename pattern. Variables: {channel}, {timestamp}, {date}, {time}, {datetime}, {index}",
		},
	}
}

func (p *StreamRotateFSPlugin) Shutdown() error {
	return nil
}

func getReadme() string {
	return `StreamRotateFS Plugin - Rotating Streaming File System

This plugin extends StreamFS with automatic file rotation support.
Data is streamed to readers while being saved to rotating files on local filesystem.

FEATURES:
  - All StreamFS features (multiple readers/writers, ring buffer, fanout)
  - Time-based rotation: Rotate files at specified intervals (e.g., every 5 minutes)
  - Size-based rotation: Rotate files when reaching size threshold (e.g., 100MB)
  - Configurable output path: Save to any agfs mount point
  - Customizable filename pattern: Use variables for dynamic naming
  - Concurrent operation: Rotation doesn't interrupt streaming

ROTATION TRIGGERS:
  - Time interval: Files rotate after specified duration (rotation_interval)
  - File size: Files rotate when reaching size threshold (rotation_size)
  - Both can be enabled simultaneously (triggers on first condition met)

FILENAME PATTERN VARIABLES:
  {channel}   - Channel/stream name
  {timestamp} - Unix timestamp (seconds)
  {date}      - Date in YYYYMMDD format
  {time}      - Time in HHMMSS format
  {datetime}  - Date and time in YYYYMMDD_HHMMSS format
  {index}     - Rotation file index (6-digit zero-padded)

USAGE EXAMPLES:

  Write to rotating stream:
    cat video.mp4 | agfs write --stream /streamrotatefs/channel1

  Read from stream (live):
    agfs cat --stream /streamrotatefs/channel1 | ffplay -

  List rotated files:
    agfs ls /s3fs/bucket/streams/
    agfs ls /localfs/data/

CONFIGURATION:

  [plugins.streamrotatefs]
  enabled = true
  path = "/streamrotatefs"

    [plugins.streamrotatefs.config]
    # Stream buffer settings (same as streamfs)
    channel_buffer_size = "6MB"
    ring_buffer_size = "6MB"

    # Rotation settings
    rotation_interval = "5m"              # Rotate every 5 minutes
    rotation_size = "100MB"               # Rotate at 100MB

    # Output path - must be an agfs path
    output_path = "/s3fs/bucket/path"    # Save to S3 via s3fs
    # OR
    # output_path = "/localfs/data"      # Save via localfs

    filename_pattern = "{channel}_{datetime}_{index}.dat"

CONFIGURATION EXAMPLES:

  Time-based rotation (every hour):
    rotation_interval = "1h"
    rotation_size = ""  # Disabled

  Size-based rotation (100MB chunks):
    rotation_interval = ""  # Disabled
    rotation_size = "100MB"

  Combined (whichever comes first):
    rotation_interval = "10m"
    rotation_size = "50MB"

FILENAME PATTERN EXAMPLES:

  {channel}_{timestamp}.dat
    → channel1_1702345678.dat

  {date}/{channel}_{time}.mp4
    → 20231207/channel1_143058.mp4

  {channel}/segment_{index}.ts
    → channel1/segment_000001.ts

OUTPUT PATH:
  - Must be an AGFS path (starts with /)
  - Example: "/s3fs/bucket/path" - Save to S3
  - Example: "/localfs/data" - Save via localfs plugin
  - Supports any mounted agfs filesystem

IMPORTANT NOTES:
  - output_path must be an agfs path (e.g., /s3fs/bucket or /localfs/data)
  - The target mount point must be already mounted and writable
  - Parent directories will be created automatically if the filesystem supports it
  - Stream continues uninterrupted during rotation
  - Old rotation files are not automatically deleted
  - Readers receive live data regardless of rotation
  - File index increments with each rotation

## License

Apache License 2.0
`
}

// Ensure StreamRotateFSPlugin implements ServicePlugin
var _ plugin.ServicePlugin = (*StreamRotateFSPlugin)(nil)
