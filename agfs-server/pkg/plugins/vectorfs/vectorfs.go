package vectorfs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/c4pt0r/agfs/agfs-server/pkg/filesystem"
	"github.com/c4pt0r/agfs/agfs-server/pkg/mountablefs"
	"github.com/c4pt0r/agfs/agfs-server/pkg/plugin"
	"github.com/c4pt0r/agfs/agfs-server/pkg/plugin/config"
	log "github.com/sirupsen/logrus"
)

const (
	PluginName = "vectorfs"
)

// VectorFSPlugin provides a document vector search service
type indexTask struct {
	namespace string
	digest    string
	fileName  string
	data      string
}

type VectorFSPlugin struct {
	s3Client        *S3Client
	tidbClient      *TiDBClient
	embeddingClient *EmbeddingClient
	indexer         *Indexer
	mu              sync.RWMutex
	metadata        plugin.PluginMetadata

	// Index worker pool
	indexQueue chan indexTask
	workerWg   sync.WaitGroup
	shutdown   chan struct{}
}

// NewVectorFSPlugin creates a new VectorFS plugin
func NewVectorFSPlugin() *VectorFSPlugin {
	return &VectorFSPlugin{
		metadata: plugin.PluginMetadata{
			Name:        PluginName,
			Version:     "1.0.0",
			Description: "Document vector search plugin with S3 storage and TiDB Cloud vector index",
			Author:      "AGFS Server",
		},
	}
}

func (v *VectorFSPlugin) Name() string {
	return v.metadata.Name
}

func (v *VectorFSPlugin) Validate(cfg map[string]interface{}) error {
	// Allowed configuration keys
	allowedKeys := []string{
		"mount_path",
		// S3 configuration
		"s3_access_key", "s3_secret_key", "s3_bucket", "s3_key_prefix", "s3_region", "s3_endpoint",
		// TiDB configuration
		"tidb_dsn", "tidb_host", "tidb_port", "tidb_user", "tidb_password", "tidb_database",
		// Embedding configuration
		"embedding_provider", "openai_api_key", "embedding_model", "embedding_dim",
		// Chunking configuration
		"chunk_size", "chunk_overlap",
	}
	if err := config.ValidateOnlyKnownKeys(cfg, allowedKeys); err != nil {
		return err
	}

	// Validate S3 configuration
	if config.GetStringConfig(cfg, "s3_bucket", "") == "" {
		return fmt.Errorf("s3_bucket is required")
	}

	// Validate TiDB configuration
	if config.GetStringConfig(cfg, "tidb_dsn", "") == "" {
		return fmt.Errorf("tidb_dsn is required")
	}

	// Validate embedding configuration
	provider := config.GetStringConfig(cfg, "embedding_provider", "openai")
	if provider == "openai" {
		if config.GetStringConfig(cfg, "openai_api_key", "") == "" {
			return fmt.Errorf("openai_api_key is required when using openai provider")
		}
	}

	return nil
}

func (v *VectorFSPlugin) Initialize(cfg map[string]interface{}) error {
	// Initialize S3 client
	s3Config := S3Config{
		AccessKey: config.GetStringConfig(cfg, "s3_access_key", ""),
		SecretKey: config.GetStringConfig(cfg, "s3_secret_key", ""),
		Bucket:    config.GetStringConfig(cfg, "s3_bucket", ""),
		KeyPrefix: config.GetStringConfig(cfg, "s3_key_prefix", "vectorfs"),
		Region:    config.GetStringConfig(cfg, "s3_region", "us-east-1"),
		Endpoint:  config.GetStringConfig(cfg, "s3_endpoint", ""),
	}

	s3Client, err := NewS3Client(s3Config)
	if err != nil {
		return fmt.Errorf("failed to initialize S3 client: %w", err)
	}
	v.s3Client = s3Client

	// Initialize TiDB client
	tidbConfig := TiDBConfig{
		DSN: config.GetStringConfig(cfg, "tidb_dsn", ""),
	}

	tidbClient, err := NewTiDBClient(tidbConfig)
	if err != nil {
		return fmt.Errorf("failed to initialize TiDB client: %w", err)
	}
	v.tidbClient = tidbClient

	// Initialize embedding client
	embeddingConfig := EmbeddingConfig{
		Provider: config.GetStringConfig(cfg, "embedding_provider", "openai"),
		APIKey:   config.GetStringConfig(cfg, "openai_api_key", ""),
		Model:    config.GetStringConfig(cfg, "embedding_model", "text-embedding-3-small"),
		Dimension: config.GetIntConfig(cfg, "embedding_dim", 1536),
	}

	embeddingClient, err := NewEmbeddingClient(embeddingConfig)
	if err != nil {
		return fmt.Errorf("failed to initialize embedding client: %w", err)
	}
	v.embeddingClient = embeddingClient

	// Initialize indexer
	chunkerConfig := ChunkerConfig{
		ChunkSize:    config.GetIntConfig(cfg, "chunk_size", 512),
		ChunkOverlap: config.GetIntConfig(cfg, "chunk_overlap", 50),
	}

	v.indexer = NewIndexer(v.s3Client, v.tidbClient, v.embeddingClient, chunkerConfig)

	// Initialize worker pool for async indexing
	workerCount := config.GetIntConfig(cfg, "index_workers", 4)
	v.indexQueue = make(chan indexTask, 100) // Buffer size 100
	v.shutdown = make(chan struct{})

	// Start worker pool
	for i := 0; i < workerCount; i++ {
		v.workerWg.Add(1)
		go v.indexWorker(i)
	}

	log.Infof("[vectorfs] Initialized successfully with %d index workers", workerCount)
	return nil
}

// indexWorker processes indexing tasks from the queue
func (v *VectorFSPlugin) indexWorker(id int) {
	defer v.workerWg.Done()

	for {
		select {
		case <-v.shutdown:
			log.Debugf("[vectorfs] Index worker %d shutting down", id)
			return
		case task := <-v.indexQueue:
			err := v.indexer.IndexDocument(task.namespace, task.digest, task.fileName, task.data)
			if err != nil {
				log.Errorf("[vectorfs] Worker %d failed to index document %s: %v", id, task.fileName, err)
			}
		}
	}
}

func (v *VectorFSPlugin) GetFileSystem() filesystem.FileSystem {
	return &vectorFS{plugin: v}
}

func (v *VectorFSPlugin) GetReadme() string {
	return `VectorFS Plugin - Document Vector Search

This plugin provides semantic search capabilities for documents using:
- S3 for document storage
- TiDB Cloud vector index for fast similarity search
- OpenAI embeddings (default)

STRUCTURE:
  /vectorfs/
    README              - This documentation
    <namespace>/        - Project/namespace directory
      docs/             - Document directory (auto-indexed on write)
      .indexing         - Indexing status (virtual file)

WORKFLOW:
  1. Create a namespace (project):
     mkdir /vectorfs/my_project

  2. Write documents (will be auto-indexed):
     echo "content" > /vectorfs/my_project/docs/document.txt

  3. Search documents using grep:
     grep 'how to deploy' /vectorfs/my_project/docs

     This will perform vector similarity search and return relevant chunks.

  4. Read indexed documents:
     cat /vectorfs/my_project/docs/document.txt

CONFIGURATION:
  [plugins.vectorfs]
  enabled = true
  path = "/vectorfs"

    [plugins.vectorfs.config]
    # S3 Storage
    s3_bucket = "my-docs"
    s3_key_prefix = "vectorfs"
    s3_region = "us-east-1"
    s3_access_key = "..."
    s3_secret_key = "..."

    # TiDB Cloud Vector Database
    tidb_dsn = "user:pass@tcp(host:4000)/dbname?tls=true"

    # Embeddings
    embedding_provider = "openai"
    openai_api_key = "sk-..."
    embedding_model = "text-embedding-3-small"
    embedding_dim = 1536

    # Chunking (optional)
    chunk_size = 512
    chunk_overlap = 50

FEATURES:
  - Automatic indexing on file write
  - Deduplication using file digest (SHA256)
  - Semantic search via grep command
  - S3 storage for scalability
  - TiDB Cloud vector index for fast search

NOTES:
  - Files are automatically indexed when written to docs/ directory
  - Same content (same digest) won't be indexed twice
  - grep command performs vector similarity search
  - Results include file path, chunk text, and relevance score
`
}

func (v *VectorFSPlugin) GetConfigParams() []plugin.ConfigParameter {
	return []plugin.ConfigParameter{
		// S3 parameters
		{Name: "s3_access_key", Type: "string", Required: false, Default: "", Description: "S3 access key"},
		{Name: "s3_secret_key", Type: "string", Required: false, Default: "", Description: "S3 secret key"},
		{Name: "s3_bucket", Type: "string", Required: true, Default: "", Description: "S3 bucket name"},
		{Name: "s3_key_prefix", Type: "string", Required: false, Default: "vectorfs", Description: "S3 key prefix"},
		{Name: "s3_region", Type: "string", Required: false, Default: "us-east-1", Description: "S3 region"},
		{Name: "s3_endpoint", Type: "string", Required: false, Default: "", Description: "Custom S3 endpoint"},
		// TiDB parameters
		{Name: "tidb_dsn", Type: "string", Required: true, Default: "", Description: "TiDB connection string (DSN)"},
		// Embedding parameters
		{Name: "embedding_provider", Type: "string", Required: false, Default: "openai", Description: "Embedding provider (openai)"},
		{Name: "openai_api_key", Type: "string", Required: true, Default: "", Description: "OpenAI API key"},
		{Name: "embedding_model", Type: "string", Required: false, Default: "text-embedding-3-small", Description: "OpenAI embedding model"},
		{Name: "embedding_dim", Type: "int", Required: false, Default: "1536", Description: "Embedding dimension"},
		// Chunking parameters
		{Name: "chunk_size", Type: "int", Required: false, Default: "512", Description: "Chunk size in tokens"},
		{Name: "chunk_overlap", Type: "int", Required: false, Default: "50", Description: "Chunk overlap in tokens"},
		// Worker pool parameters
		{Name: "index_workers", Type: "int", Required: false, Default: "4", Description: "Number of concurrent indexing workers"},
	}
}

func (v *VectorFSPlugin) Shutdown() error {
	v.mu.Lock()
	defer v.mu.Unlock()

	// Shutdown worker pool
	if v.shutdown != nil {
		close(v.shutdown)
		close(v.indexQueue)
		v.workerWg.Wait() // Wait for all workers to finish
		log.Info("[vectorfs] All index workers shut down")
	}

	if v.tidbClient != nil {
		v.tidbClient.Close()
	}

	return nil
}

// CustomGrep implements the CustomGrepper interface using vector search
func (vfs *vectorFS) CustomGrep(path, query string) ([]mountablefs.CustomGrepResult, error) {
	// Parse path to get namespace
	namespace, relativePath, err := parsePath(path)
	if err != nil {
		return nil, err
	}

	// Only support search in docs/ directory
	if !strings.HasPrefix(relativePath, "docs") && relativePath != "docs" {
		return nil, fmt.Errorf("vector search only supported in docs/ directory")
	}

	// Use VectorSearch method (dependency injection point)
	return vfs.VectorSearch(namespace, query)
}

// VectorSearch performs vector similarity search using embeddings
// This method can be injected/replaced for testing or alternative implementations
func (vfs *vectorFS) VectorSearch(namespace, query string) ([]mountablefs.CustomGrepResult, error) {
	// Generate embedding for query
	queryEmbedding, err := vfs.plugin.embeddingClient.GenerateEmbedding(query)
	if err != nil {
		return nil, fmt.Errorf("failed to generate query embedding: %w", err)
	}

	// Perform vector search in TiDB
	results, err := vfs.plugin.tidbClient.VectorSearch(namespace, queryEmbedding, 10)
	if err != nil {
		return nil, fmt.Errorf("failed to perform vector search: %w", err)
	}

	// Convert to CustomGrepResult format
	var matches []mountablefs.CustomGrepResult
	for _, result := range results {
		matches = append(matches, mountablefs.CustomGrepResult{
			File:    namespace + "/docs/" + result.FileName,
			Line:    result.ChunkIndex + 1, // 1-indexed line numbers
			Content: result.ChunkText,
			Metadata: map[string]interface{}{
				"distance": result.Distance,
				"score":    1.0 - result.Distance, // Convert distance to similarity score
			},
		})
	}

	return matches, nil
}

// vectorFS implements the FileSystem interface for vector operations
type vectorFS struct {
	plugin *VectorFSPlugin
}

// parsePath parses a path like "/namespace/docs/file.txt" into (namespace, "docs/file.txt")
func parsePath(path string) (namespace string, relativePath string, err error) {
	path = filepath.Clean(path)
	path = strings.TrimPrefix(path, "/")

	if path == "" || path == "." {
		return "", "", nil
	}

	parts := strings.SplitN(path, "/", 2)
	if len(parts) == 0 {
		return "", "", fmt.Errorf("invalid path")
	}

	namespace = parts[0]
	if len(parts) == 2 {
		relativePath = parts[1]
	}

	return namespace, relativePath, nil
}

func (vfs *vectorFS) Create(path string) error {
	return nil // Files are created on Write
}

func (vfs *vectorFS) Mkdir(path string, perm uint32) error {
	namespace, relativePath, err := parsePath(path)
	if err != nil {
		return err
	}

	// If creating subdirectory under docs/, allow it (virtual, no-op)
	if relativePath != "" {
		if strings.HasPrefix(relativePath, "docs/") {
			// Virtual subdirectory - no action needed, just return success
			return nil
		}
		return fmt.Errorf("can only create namespace directories or docs/ subdirectories")
	}

	if namespace == "" {
		return fmt.Errorf("invalid namespace name")
	}

	// Create tables for this namespace
	return vfs.plugin.tidbClient.CreateNamespace(namespace, vfs.plugin.embeddingClient.GetDimension())
}

func (vfs *vectorFS) Remove(path string) error {
	return fmt.Errorf("remove not supported in vectorfs (use rm -r to delete entire namespace)")
}

func (vfs *vectorFS) RemoveAll(path string) error {
	namespace, relativePath, err := parsePath(path)
	if err != nil {
		return err
	}

	// Only allow removing entire namespace (not subdirectories)
	if relativePath != "" {
		return fmt.Errorf("can only remove entire namespace, not subdirectories (path: %s)", path)
	}

	if namespace == "" {
		return fmt.Errorf("cannot remove root directory")
	}

	// Delete the namespace (drops all tables)
	return vfs.plugin.tidbClient.DeleteNamespace(namespace)
}

func (vfs *vectorFS) Read(path string, offset int64, size int64) ([]byte, error) {
	// Special case: README at root
	if path == "/README" {
		data := []byte(vfs.plugin.GetReadme())
		return plugin.ApplyRangeRead(data, offset, size)
	}

	namespace, relativePath, err := parsePath(path)
	if err != nil {
		return nil, err
	}

	// Handle virtual .indexing file
	if relativePath == ".indexing" {
		status := "idle" // TODO: get actual indexing status
		return []byte(status), nil
	}

	// Only allow reading from docs/ directory
	if !strings.HasPrefix(relativePath, "docs/") {
		return nil, fmt.Errorf("can only read files from docs/ directory")
	}

	// Extract filename from path (support subdirectories)
	// relativePath format: "docs/subdir/file.txt" or "docs/file.txt"
	fileName := strings.TrimPrefix(relativePath, "docs/")
	if fileName == "" || fileName == "/" {
		return nil, fmt.Errorf("cannot read directory, specify a file")
	}

	// Get file metadata from TiDB (includes S3 key and digest)
	meta, err := vfs.plugin.tidbClient.GetFileMetadataByName(namespace, fileName)
	if err != nil {
		return nil, fmt.Errorf("failed to get file metadata: %w", err)
	}

	// Download document from S3 using digest
	ctx := context.Background()
	data, err := vfs.plugin.s3Client.DownloadDocument(ctx, namespace, meta.FileDigest)
	if err != nil {
		return nil, fmt.Errorf("failed to download document from S3: %w", err)
	}

	log.Debugf("[vectorfs] Read file: %s (namespace: %s, digest: %s, size: %d bytes)",
		fileName, namespace, meta.FileDigest, len(data))

	// Apply range read if requested
	return plugin.ApplyRangeRead(data, offset, size)
}

func (vfs *vectorFS) Write(path string, data []byte, offset int64, flags filesystem.WriteFlag) (int64, error) {
	namespace, relativePath, err := parsePath(path)
	if err != nil {
		return 0, err
	}

	// Only allow writing to docs/ directory
	if !strings.HasPrefix(relativePath, "docs/") {
		return 0, fmt.Errorf("can only write files to docs/ directory")
	}

	// Calculate file digest
	hash := sha256.Sum256(data)
	digest := hex.EncodeToString(hash[:])

	// Extract relative path from docs/ (includes subdirectories)
	// relativePath format: "docs/subdir/file.txt" -> fileName: "subdir/file.txt"
	fileName := strings.TrimPrefix(relativePath, "docs/")

	// Submit indexing task to worker pool
	task := indexTask{
		namespace: namespace,
		digest:    digest,
		fileName:  fileName,
		data:      string(data),
	}

	// Non-blocking send to queue
	select {
	case vfs.plugin.indexQueue <- task:
		// Task queued successfully
	default:
		// Queue is full, log warning but don't block
		log.Warnf("[vectorfs] Index queue full, document %s will be indexed when queue has space", fileName)
		go func() {
			vfs.plugin.indexQueue <- task
		}()
	}

	return int64(len(data)), nil
}

func (vfs *vectorFS) ReadDir(path string) ([]filesystem.FileInfo, error) {
	namespace, relativePath, err := parsePath(path)
	if err != nil {
		return nil, err
	}

	now := time.Now()

	// Root directory
	if path == "/" || namespace == "" {
		readme := vfs.plugin.GetReadme()
		files := []filesystem.FileInfo{
			{
				Name:    "README",
				Size:    int64(len(readme)),
				Mode:    0444,
				ModTime: now,
				IsDir:   false,
				Meta:    filesystem.MetaData{Name: PluginName, Type: "doc"},
			},
		}

		// List all namespaces (get from TiDB)
		namespaces, err := vfs.plugin.tidbClient.ListNamespaces()
		if err != nil {
			return nil, err
		}

		for _, ns := range namespaces {
			files = append(files, filesystem.FileInfo{
				Name:    ns,
				Size:    0,
				Mode:    0755,
				ModTime: now,
				IsDir:   true,
				Meta:    filesystem.MetaData{Name: PluginName, Type: "namespace"},
			})
		}

		return files, nil
	}

	// Namespace directory
	if relativePath == "" {
		return []filesystem.FileInfo{
			{
				Name:    "docs",
				Size:    0,
				Mode:    0755,
				ModTime: now,
				IsDir:   true,
				Meta:    filesystem.MetaData{Name: PluginName, Type: "docs"},
			},
			{
				Name:    ".indexing",
				Size:    4,
				Mode:    0444,
				ModTime: now,
				IsDir:   false,
				Meta:    filesystem.MetaData{Name: PluginName, Type: "status"},
			},
		}, nil
	}

	// docs/ directory
	if relativePath == "docs" {
		// List files in this namespace
		files, err := vfs.plugin.tidbClient.ListFiles(namespace)
		if err != nil {
			return nil, err
		}

		var fileInfos []filesystem.FileInfo
		for _, f := range files {
			fileInfos = append(fileInfos, filesystem.FileInfo{
				Name:    f.FileName,
				Size:    f.FileSize,
				Mode:    0644,
				ModTime: f.UpdatedAt,
				IsDir:   false,
				Meta:    filesystem.MetaData{Name: PluginName, Type: "document"},
			})
		}

		return fileInfos, nil
	}

	return nil, fmt.Errorf("not a directory")
}

func (vfs *vectorFS) Stat(path string) (*filesystem.FileInfo, error) {
	if path == "/" {
		return &filesystem.FileInfo{
			Name:    "/",
			Size:    0,
			Mode:    0755,
			ModTime: time.Now(),
			IsDir:   true,
			Meta:    filesystem.MetaData{Name: PluginName, Type: "root"},
		}, nil
	}

	if path == "/README" {
		readme := vfs.plugin.GetReadme()
		return &filesystem.FileInfo{
			Name:    "README",
			Size:    int64(len(readme)),
			Mode:    0444,
			ModTime: time.Now(),
			IsDir:   false,
			Meta:    filesystem.MetaData{Name: PluginName, Type: "doc"},
		}, nil
	}

	namespace, relativePath, err := parsePath(path)
	if err != nil {
		return nil, err
	}

	// Namespace directory
	if relativePath == "" {
		exists, err := vfs.plugin.tidbClient.NamespaceExists(namespace)
		if err != nil || !exists {
			return nil, filesystem.ErrNotFound
		}

		return &filesystem.FileInfo{
			Name:    namespace,
			Size:    0,
			Mode:    0755,
			ModTime: time.Now(),
			IsDir:   true,
			Meta:    filesystem.MetaData{Name: PluginName, Type: "namespace"},
		}, nil
	}

	// docs directory
	if relativePath == "docs" {
		return &filesystem.FileInfo{
			Name:    "docs",
			Size:    0,
			Mode:    0755,
			ModTime: time.Now(),
			IsDir:   true,
			Meta:    filesystem.MetaData{Name: PluginName, Type: "docs"},
		}, nil
	}

	// .indexing status file
	if relativePath == ".indexing" {
		return &filesystem.FileInfo{
			Name:    ".indexing",
			Size:    4,
			Mode:    0444,
			ModTime: time.Now(),
			IsDir:   false,
			Meta:    filesystem.MetaData{Name: PluginName, Type: "status"},
		}, nil
	}

	return nil, filesystem.ErrNotFound
}

func (vfs *vectorFS) Rename(oldPath, newPath string) error {
	return fmt.Errorf("rename not supported in vectorfs")
}

func (vfs *vectorFS) Chmod(path string, mode uint32) error {
	return fmt.Errorf("chmod not supported in vectorfs")
}

func (vfs *vectorFS) Open(path string) (io.ReadCloser, error) {
	data, err := vfs.Read(path, 0, -1)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(strings.NewReader(string(data))), nil
}

func (vfs *vectorFS) OpenWrite(path string) (io.WriteCloser, error) {
	return &vectorWriter{vfs: vfs, path: path}, nil
}

type vectorWriter struct {
	vfs  *vectorFS
	path string
	buf  strings.Builder
}

func (vw *vectorWriter) Write(p []byte) (n int, err error) {
	return vw.buf.Write(p)
}

func (vw *vectorWriter) Close() error {
	data := []byte(vw.buf.String())
	_, err := vw.vfs.Write(vw.path, data, -1, filesystem.WriteFlagCreate)
	return err
}

// Ensure VectorFSPlugin implements ServicePlugin
var _ plugin.ServicePlugin = (*VectorFSPlugin)(nil)
var _ filesystem.FileSystem = (*vectorFS)(nil)
