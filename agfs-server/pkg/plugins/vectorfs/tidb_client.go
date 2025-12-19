package vectorfs

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	log "github.com/sirupsen/logrus"
)

// TiDBConfig holds TiDB configuration
type TiDBConfig struct {
	DSN string // Connection string
}

// TiDBClient handles TiDB operations for vector search
type TiDBClient struct {
	db *sql.DB
}

// FileMetadata represents file metadata stored in TiDB
type FileMetadata struct {
	FileDigest string
	FileName   string
	S3Key      string
	FileSize   int64
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// VectorMatch represents a vector search result
type VectorMatch struct {
	FileDigest string
	FileName   string
	ChunkText  string
	ChunkIndex int
	Distance   float64
}

// NewTiDBClient creates a new TiDB client
func NewTiDBClient(cfg TiDBConfig) (*TiDBClient, error) {
	db, err := sql.Open("mysql", cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to TiDB: %w", err)
	}

	// Test connection
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping TiDB: %w", err)
	}

	// Set connection pool settings
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	log.Infof("[vectorfs/tidb] Connected to TiDB successfully")

	return &TiDBClient{db: db}, nil
}

// Close closes the TiDB connection
func (c *TiDBClient) Close() error {
	if c.db != nil {
		return c.db.Close()
	}
	return nil
}

// sanitizeTableName sanitizes namespace name for use as table suffix
func sanitizeTableName(namespace string) string {
	// Replace invalid characters with underscore
	replacer := strings.NewReplacer(
		"-", "_",
		".", "_",
		"/", "_",
		" ", "_",
	)
	return replacer.Replace(namespace)
}

// CreateNamespace creates tables for a new namespace (fails if already exists)
func (c *TiDBClient) CreateNamespace(namespace string, embeddingDim int) error {
	tableSuffix := sanitizeTableName(namespace)
	metaTable := fmt.Sprintf("tbl_meta_%s", tableSuffix)
	chunksTable := fmt.Sprintf("tbl_chunks_%s", tableSuffix)

	// Check if namespace already exists
	exists, err := c.NamespaceExists(namespace)
	if err != nil {
		return fmt.Errorf("failed to check namespace existence: %w", err)
	}
	if exists {
		return fmt.Errorf("namespace already exists: %s", namespace)
	}

	// Create metadata table
	createMetaSQL := fmt.Sprintf(`
		CREATE TABLE %s (
			file_digest VARCHAR(64) PRIMARY KEY,
			file_name VARCHAR(1024) NOT NULL,
			s3_key VARCHAR(1024) NOT NULL,
			file_size BIGINT NOT NULL DEFAULT 0,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			INDEX idx_file_name (file_name)
		)
	`, metaTable)

	if _, err := c.db.Exec(createMetaSQL); err != nil {
		return fmt.Errorf("failed to create metadata table: %w", err)
	}

	// Create chunks table with vector index
	createChunksSQL := fmt.Sprintf(`
		CREATE TABLE %s (
			chunk_id BIGINT AUTO_INCREMENT PRIMARY KEY,
			file_digest VARCHAR(64) NOT NULL,
			chunk_index INT NOT NULL,
			chunk_text TEXT NOT NULL,
			embedding VECTOR(%d) NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			INDEX idx_file_digest (file_digest),
			VECTOR INDEX idx_embedding ((VEC_COSINE_DISTANCE(embedding)))
		)
	`, chunksTable, embeddingDim)

	if _, err := c.db.Exec(createChunksSQL); err != nil {
		return fmt.Errorf("failed to create chunks table: %w", err)
	}

	log.Infof("[vectorfs/tidb] Created tables for namespace: %s", namespace)
	return nil
}

// DeleteNamespace drops all tables for a namespace
func (c *TiDBClient) DeleteNamespace(namespace string) error {
	tableSuffix := sanitizeTableName(namespace)
	metaTable := fmt.Sprintf("tbl_meta_%s", tableSuffix)
	chunksTable := fmt.Sprintf("tbl_chunks_%s", tableSuffix)

	// Drop chunks table first (has foreign key reference)
	if _, err := c.db.Exec(fmt.Sprintf("DROP TABLE IF EXISTS %s", chunksTable)); err != nil {
		return fmt.Errorf("failed to drop chunks table: %w", err)
	}

	// Drop metadata table
	if _, err := c.db.Exec(fmt.Sprintf("DROP TABLE IF EXISTS %s", metaTable)); err != nil {
		return fmt.Errorf("failed to drop metadata table: %w", err)
	}

	log.Infof("[vectorfs/tidb] Deleted tables for namespace: %s", namespace)
	return nil
}

// NamespaceExists checks if a namespace exists
func (c *TiDBClient) NamespaceExists(namespace string) (bool, error) {
	tableSuffix := sanitizeTableName(namespace)
	metaTable := fmt.Sprintf("tbl_meta_%s", tableSuffix)

	query := `
		SELECT COUNT(*)
		FROM information_schema.tables
		WHERE table_schema = DATABASE()
		AND table_name = ?
	`

	var count int
	err := c.db.QueryRow(query, metaTable).Scan(&count)
	if err != nil {
		return false, err
	}

	return count > 0, nil
}

// ListNamespaces lists all namespaces (by finding all tbl_meta_* tables)
func (c *TiDBClient) ListNamespaces() ([]string, error) {
	query := `
		SELECT table_name
		FROM information_schema.tables
		WHERE table_schema = DATABASE()
		AND table_name LIKE 'tbl_meta_%'
	`

	rows, err := c.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var namespaces []string
	for rows.Next() {
		var tableName string
		if err := rows.Scan(&tableName); err != nil {
			return nil, err
		}

		// Extract namespace from table name (remove tbl_meta_ prefix)
		namespace := strings.TrimPrefix(tableName, "tbl_meta_")
		namespaces = append(namespaces, namespace)
	}

	return namespaces, nil
}

// FileExists checks if a file (by digest) is already indexed
func (c *TiDBClient) FileExists(namespace, digest string) (bool, error) {
	tableSuffix := sanitizeTableName(namespace)
	metaTable := fmt.Sprintf("tbl_meta_%s", tableSuffix)

	query := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE file_digest = ?", metaTable)

	var count int
	err := c.db.QueryRow(query, digest).Scan(&count)
	if err != nil {
		return false, err
	}

	return count > 0, nil
}

// InsertFileMetadata inserts file metadata
func (c *TiDBClient) InsertFileMetadata(namespace string, meta FileMetadata) error {
	tableSuffix := sanitizeTableName(namespace)
	metaTable := fmt.Sprintf("tbl_meta_%s", tableSuffix)

	query := fmt.Sprintf(`
		INSERT INTO %s (file_digest, file_name, s3_key, file_size, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			file_name = VALUES(file_name),
			s3_key = VALUES(s3_key),
			file_size = VALUES(file_size),
			updated_at = VALUES(updated_at)
	`, metaTable)

	_, err := c.db.Exec(query, meta.FileDigest, meta.FileName, meta.S3Key, meta.FileSize,
		meta.CreatedAt, meta.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to insert file metadata: %w", err)
	}

	return nil
}

// InsertChunk inserts a document chunk with embedding
func (c *TiDBClient) InsertChunk(namespace, fileDigest string, chunkIndex int, chunkText string, embedding []float32) error {
	tableSuffix := sanitizeTableName(namespace)
	chunksTable := fmt.Sprintf("tbl_chunks_%s", tableSuffix)

	// Convert embedding to vector string format: "[1.0, 2.0, 3.0]"
	embeddingStr := formatVector(embedding)

	// Use parameterized query - TiDB accepts vector as string parameter
	query := fmt.Sprintf(`
		INSERT INTO %s (file_digest, chunk_index, chunk_text, embedding)
		VALUES (?, ?, ?, ?)
	`, chunksTable)

	_, err := c.db.Exec(query, fileDigest, chunkIndex, chunkText, embeddingStr)
	if err != nil {
		return fmt.Errorf("failed to insert chunk: %w", err)
	}

	return nil
}

// formatVector converts float32 array to vector string format
func formatVector(vec []float32) string {
	strVals := make([]string, len(vec))
	for i, v := range vec {
		strVals[i] = fmt.Sprintf("%f", v)
	}
	return fmt.Sprintf("[%s]", strings.Join(strVals, ","))
}

// VectorSearch performs vector similarity search
func (c *TiDBClient) VectorSearch(namespace string, queryEmbedding []float32, limit int) ([]VectorMatch, error) {
	tableSuffix := sanitizeTableName(namespace)
	metaTable := fmt.Sprintf("tbl_meta_%s", tableSuffix)
	chunksTable := fmt.Sprintf("tbl_chunks_%s", tableSuffix)

	embeddingStr := formatVector(queryEmbedding)

	// Use parameterized query for vector parameter
	query := fmt.Sprintf(`
		SELECT
			c.file_digest,
			m.file_name,
			c.chunk_text,
			c.chunk_index,
			VEC_COSINE_DISTANCE(c.embedding, ?) AS distance
		FROM %s c
		JOIN %s m ON c.file_digest = m.file_digest
		ORDER BY distance
		LIMIT ?
	`, chunksTable, metaTable)

	rows, err := c.db.Query(query, embeddingStr, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to execute vector search: %w", err)
	}
	defer rows.Close()

	var results []VectorMatch
	for rows.Next() {
		var match VectorMatch
		if err := rows.Scan(&match.FileDigest, &match.FileName, &match.ChunkText,
			&match.ChunkIndex, &match.Distance); err != nil {
			return nil, err
		}
		results = append(results, match)
	}

	log.Debugf("[vectorfs/tidb] Vector search returned %d results", len(results))
	return results, nil
}

// ListFiles lists all files in a namespace
func (c *TiDBClient) ListFiles(namespace string) ([]FileMetadata, error) {
	tableSuffix := sanitizeTableName(namespace)
	metaTable := fmt.Sprintf("tbl_meta_%s", tableSuffix)

	query := fmt.Sprintf(`
		SELECT file_digest, file_name, s3_key, file_size, created_at, updated_at
		FROM %s
		ORDER BY updated_at DESC
	`, metaTable)

	rows, err := c.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []FileMetadata
	for rows.Next() {
		var file FileMetadata
		if err := rows.Scan(&file.FileDigest, &file.FileName, &file.S3Key, &file.FileSize,
			&file.CreatedAt, &file.UpdatedAt); err != nil {
			return nil, err
		}
		files = append(files, file)
	}

	return files, nil
}

// DeleteFileChunks deletes all chunks for a file
func (c *TiDBClient) DeleteFileChunks(namespace, fileDigest string) error {
	tableSuffix := sanitizeTableName(namespace)
	chunksTable := fmt.Sprintf("tbl_chunks_%s", tableSuffix)

	query := fmt.Sprintf("DELETE FROM %s WHERE file_digest = ?", chunksTable)

	_, err := c.db.Exec(query, fileDigest)
	return err
}

// DeleteFileMetadata deletes file metadata
func (c *TiDBClient) DeleteFileMetadata(namespace, fileDigest string) error {
	tableSuffix := sanitizeTableName(namespace)
	metaTable := fmt.Sprintf("tbl_meta_%s", tableSuffix)

	query := fmt.Sprintf("DELETE FROM %s WHERE file_digest = ?", metaTable)

	_, err := c.db.Exec(query, fileDigest)
	return err
}

// GetFileMetadataByName retrieves file metadata by file name
func (c *TiDBClient) GetFileMetadataByName(namespace, fileName string) (*FileMetadata, error) {
	tableSuffix := sanitizeTableName(namespace)
	metaTable := fmt.Sprintf("tbl_meta_%s", tableSuffix)

	query := fmt.Sprintf(`
		SELECT file_digest, file_name, s3_key, file_size, created_at, updated_at
		FROM %s
		WHERE file_name = ?
		LIMIT 1
	`, metaTable)

	var meta FileMetadata
	err := c.db.QueryRow(query, fileName).Scan(
		&meta.FileDigest,
		&meta.FileName,
		&meta.S3Key,
		&meta.FileSize,
		&meta.CreatedAt,
		&meta.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("file not found: %s", fileName)
		}
		return nil, err
	}

	return &meta, nil
}
