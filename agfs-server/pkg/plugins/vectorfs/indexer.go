package vectorfs

import (
	"context"
	"fmt"
	"time"

	log "github.com/sirupsen/logrus"
)

// Indexer handles document indexing
type Indexer struct {
	s3Client        *S3Client
	tidbClient      *TiDBClient
	embeddingClient *EmbeddingClient
	chunkerConfig   ChunkerConfig
}

// NewIndexer creates a new indexer
func NewIndexer(
	s3Client *S3Client,
	tidbClient *TiDBClient,
	embeddingClient *EmbeddingClient,
	chunkerConfig ChunkerConfig,
) *Indexer {
	return &Indexer{
		s3Client:        s3Client,
		tidbClient:      tidbClient,
		embeddingClient: embeddingClient,
		chunkerConfig:   chunkerConfig,
	}
}

// IndexDocument indexes a document (upload to S3, chunk, generate embeddings, store in TiDB)
func (idx *Indexer) IndexDocument(namespace, digest, fileName, content string) error {
	ctx := context.Background()

	log.Infof("[vectorfs/indexer] Indexing document: %s (namespace: %s, digest: %s)",
		fileName, namespace, digest)

	// Check if already indexed
	exists, err := idx.tidbClient.FileExists(namespace, digest)
	if err != nil {
		return fmt.Errorf("failed to check if file exists: %w", err)
	}

	if exists {
		log.Infof("[vectorfs/indexer] Document already indexed, skipping: %s", digest)
		return nil
	}

	// Upload to S3
	s3Key := idx.s3Client.buildKey(namespace, digest)
	err = idx.s3Client.UploadDocument(ctx, namespace, digest, []byte(content))
	if err != nil {
		return fmt.Errorf("failed to upload to S3: %w", err)
	}

	// Insert file metadata
	now := time.Now()
	metadata := FileMetadata{
		FileDigest: digest,
		FileName:   fileName,
		S3Key:      s3Key,
		FileSize:   int64(len(content)),
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	err = idx.tidbClient.InsertFileMetadata(namespace, metadata)
	if err != nil {
		return fmt.Errorf("failed to insert file metadata: %w", err)
	}

	// Chunk the document
	chunks := ChunkDocument(content, idx.chunkerConfig)
	log.Infof("[vectorfs/indexer] Split into %d chunks", len(chunks))

	// Generate embeddings for all chunks (batch)
	var chunkTexts []string
	for _, chunk := range chunks {
		chunkTexts = append(chunkTexts, chunk.Text)
	}

	embeddings, err := idx.embeddingClient.GenerateBatchEmbeddings(chunkTexts)
	if err != nil {
		return fmt.Errorf("failed to generate embeddings: %w", err)
	}

	// Insert chunks with embeddings
	for i, chunk := range chunks {
		err = idx.tidbClient.InsertChunk(namespace, digest, chunk.Index, chunk.Text, embeddings[i])
		if err != nil {
			return fmt.Errorf("failed to insert chunk %d: %w", i, err)
		}
	}

	log.Infof("[vectorfs/indexer] Successfully indexed document: %s (%d chunks)",
		fileName, len(chunks))
	return nil
}

// DeleteDocument removes a document from the index
func (idx *Indexer) DeleteDocument(namespace, digest string) error {
	ctx := context.Background()

	// Delete chunks from TiDB
	if err := idx.tidbClient.DeleteFileChunks(namespace, digest); err != nil {
		return fmt.Errorf("failed to delete chunks: %w", err)
	}

	// Delete metadata from TiDB
	if err := idx.tidbClient.DeleteFileMetadata(namespace, digest); err != nil {
		return fmt.Errorf("failed to delete metadata: %w", err)
	}

	// Delete from S3
	if err := idx.s3Client.DeleteDocument(ctx, namespace, digest); err != nil {
		return fmt.Errorf("failed to delete from S3: %w", err)
	}

	log.Infof("[vectorfs/indexer] Deleted document: %s", digest)
	return nil
}
