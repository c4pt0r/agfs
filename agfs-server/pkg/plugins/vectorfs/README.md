# VectorFS Plugin

Document Vector Search Plugin for AGFS with S3 storage and TiDB Cloud vector indexing.

## Overview

VectorFS provides semantic search capabilities for documents by combining:
- **S3** for scalable document storage
- **TiDB Cloud** vector index for fast similarity search using HNSW algorithm
- **OpenAI** embeddings (default) for generating vector representations

## Features

- **Automatic Indexing**: Documents are automatically indexed when written (async with worker pool)
- **Deduplication**: Same content (same SHA256 digest) won't be indexed twice
- **Semantic Search**: Use standard `grep` command for vector similarity search
- **Document Retrieval**: Read original documents with `cat` command
- **Subdirectory Support**: Organize documents in nested folders
- **Batch Copy**: Copy entire folders with `cp -r` command
- **Scalable Storage**: S3-backed document storage
- **Fast Vector Search**: TiDB Cloud's HNSW index with >90% recall rate
- **Document Chunking**: Smart chunking by paragraphs and sentences
- **Multiple Namespaces**: Isolate documents by project/namespace
- **Similarity Scores**: Search results include distance and relevance scores

## Directory Structure

```
/vectorfs/
  README                    - Documentation
  <namespace>/              - Project/namespace directory
    docs/                   - Document directory (auto-indexed)
      file1.txt             - Root-level document
      subfolder/            - Subdirectory (virtual)
        file2.txt           - Nested document
        deep/file3.txt      - Deeply nested document
    .indexing               - Indexing status (virtual file, read-only)
```

**Note**:
- Subdirectories under `docs/` are virtual - they don't need to be created explicitly. Just write files with paths like `docs/guides/tutorial.txt` and the directory structure is maintained in metadata.
- The `.indexing` file is a virtual read-only status file. Currently returns "idle" as a placeholder. Future versions will show real-time worker pool status.

## Configuration

### TOML Configuration

```toml
[plugins.vectorfs]
enabled = true
path = "/vectorfs"

  [plugins.vectorfs.config]
  # S3 Storage Configuration
  s3_bucket = "my-document-bucket"
  s3_key_prefix = "vectorfs"           # Optional, default: "vectorfs"
  s3_region = "us-east-1"              # Optional, default: "us-east-1"
  s3_access_key = "AKIAXXXXXXXX"       # Optional, uses IAM role if not provided
  s3_secret_key = "secret"             # Optional
  s3_endpoint = ""                     # Optional, for custom S3-compatible services

  # TiDB Cloud Configuration
  tidb_dsn = "user:password@tcp(gateway01.us-west-2.prod.aws.tidbcloud.com:4000)/dbname?tls=true"

  # Embedding Configuration
  embedding_provider = "openai"                    # Default: "openai"
  openai_api_key = "sk-xxxxxxxxxxxxxxxx"
  embedding_model = "text-embedding-3-small"       # Default: "text-embedding-3-small"
  embedding_dim = 1536                             # Default: 1536

  # Chunking Configuration (Optional)
  chunk_size = 512                                 # Default: 512 tokens
  chunk_overlap = 50                               # Default: 50 tokens

  # Worker Pool Configuration (Optional)
  index_workers = 4                                # Default: 4 concurrent workers
```

### TiDB Cloud Setup

1. Create a TiDB Cloud cluster (Serverless or Dedicated)
2. Enable TiFlash (required for vector search)
3. Get the connection string (DSN) from cluster details
4. Tables will be created automatically when you create a namespace

### S3 Setup

1. Create an S3 bucket (or use S3-compatible service like MinIO)
2. Configure access credentials (IAM role recommended for production)
3. Documents will be stored as: `s3://bucket/vectorfs/<namespace>/<digest>`

## Usage

### 1. Create a Namespace (Project)

```bash
agfs:/> mkdir /vectorfs/my_project
```

This creates TiDB tables:
- `tbl_meta_my_project` - File metadata
- `tbl_chunks_my_project` - Document chunks with vector embeddings

### 2. Write Documents

Documents are automatically indexed when written to the `docs/` directory:

```bash
# Write a single file
agfs:/> echo "How to deploy applications..." > /vectorfs/my_project/docs/deployment.txt

# Write to subdirectory (virtual subdirectories)
agfs:/> echo "Kubernetes guide" > /vectorfs/my_project/docs/guides/kubernetes.txt
agfs:/> echo "Docker tutorial" > /vectorfs/my_project/docs/tutorials/docker.txt
```

**What happens:**
1. Write operation returns immediately (~8ms)
2. Indexing happens asynchronously in background worker pool:
   - SHA256 digest calculated
   - Document uploaded to S3
   - Text split into chunks (~512 tokens)
   - Embeddings generated via OpenAI API
   - Chunks and embeddings stored in TiDB

**Copy entire folders:**
```bash
# Copy multiple files and folders
agfs:/> cp -r /s3fs/mybucket/docs /vectorfs/my_project/docs/imported
```

### 3. Search Documents

Use the standard `grep` command for semantic search:

```bash
agfs:/> grep "deployment strategies" /vectorfs/my_project/docs

# Or use agfs-shell's fsgrep command
$ fsgrep -r "deployment strategies" /vectorfs/my_project/docs
```

**Returns:**
```json
{
  "matches": [
    {
      "file": "/vectorfs/my_project/docs/deployment.txt",
      "line": 1,
      "content": "How to deploy applications using blue-green strategy...",
      "metadata": {
        "distance": 0.234,
        "score": 0.766
      }
    },
    {
      "file": "/vectorfs/my_project/docs/kubernetes.txt",
      "line": 3,
      "content": "Kubernetes deployment strategies include rolling updates...",
      "metadata": {
        "distance": 0.412,
        "score": 0.588
      }
    }
  ],
  "count": 2
}
```

**Similarity scores:**
- `distance`: Cosine distance (0.0 = identical, 1.0 = completely different)
- `score`: Relevance score (1.0 - distance, higher is better)

The search uses **cosine distance** in TiDB's vector index to find semantically similar chunks.

### 4. Read Documents

Read original document content from S3:

```bash
# Read a file
agfs:/> cat /vectorfs/my_project/docs/deployment.txt

# Read from subdirectory
agfs:/> cat /vectorfs/my_project/docs/guides/kubernetes.txt
```

Documents are retrieved from S3 using the file's digest and returned with their original content.

### 5. List Documents

```bash
agfs:/> ls /vectorfs/my_project/docs
deployment.txt
kubernetes.txt
architecture.md
guides/
tutorials/

# List subdirectory
agfs:/> ls /vectorfs/my_project/docs/guides
kubernetes.txt
getting-started.md
```

### 6. Check Indexing Status

Each namespace has a virtual `.indexing` file that shows background indexing status:

```bash
agfs:/> cat /vectorfs/my_project/.indexing
idle
```

**Current Status**: This file currently returns `idle` as a placeholder. Since indexing happens asynchronously in a worker pool, documents may still be processing in the background even when showing "idle".

**Future Enhancement**: Will show real-time worker pool statistics:
- Queue depth (pending documents)
- Active workers processing
- Indexing rate and completion status

**Note**: With async indexing, there may be a short delay (typically 1-15 seconds depending on file size) between writing a file and it being searchable. Large files (>20KB) with many chunks take longer to index.

## Architecture

### Data Flow

```
User writes file
      ↓
  Calculate SHA256 digest
      ↓
  Submit to index queue → Return immediately (~8ms)
      ↓
Worker pool (4 workers by default) processes async:
      ↓
  Upload to S3 (s3://bucket/vectorfs/<namespace>/<digest>)
      ↓
  Chunk document (paragraphs → sentences)
      ↓
  Generate embeddings (OpenAI API, batch)
      ↓
  Store in TiDB:
    - tbl_meta_<namespace> (file metadata)
    - tbl_chunks_<namespace> (chunks + vector embeddings)
```

### Vector Search Flow

```
User runs grep
      ↓
  Generate query embedding (OpenAI API)
      ↓
  TiDB vector search:
    SELECT ... ORDER BY VEC_COSINE_DISTANCE(embedding, <query>) LIMIT 10
      ↓
  Return matching chunks as GrepMatch format
```

## Database Schema

### File Metadata Table

```sql
CREATE TABLE tbl_meta_<namespace> (
    file_digest VARCHAR(64) PRIMARY KEY,
    file_name VARCHAR(1024) NOT NULL,
    s3_key VARCHAR(1024) NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_file_name (file_name)
);
```

### Chunks Table with Vector Index

```sql
CREATE TABLE tbl_chunks_<namespace> (
    chunk_id BIGINT AUTO_INCREMENT PRIMARY KEY,
    file_digest VARCHAR(64) NOT NULL,
    chunk_index INT NOT NULL,
    chunk_text TEXT NOT NULL,
    embedding VECTOR(1536) NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_file_digest (file_digest),
    VECTOR INDEX idx_embedding ((VEC_COSINE_DISTANCE(embedding)))
);
```

## Performance Considerations

### Write Performance
- **Write Response**: ~8ms (immediate return, async indexing)
- **Worker Pool**: 4 concurrent workers (configurable)
- **Queue Capacity**: 100 pending tasks

### Indexing Performance (Background)
- **Embedding API**: ~100-200ms per batch (OpenAI)
- **TiDB Insert**: ~10-50ms per chunk
- **S3 Upload**: ~50-200ms per document
- **Large Files**: 26KB file (~169 chunks) completes in ~15 seconds

**Benefits of async indexing:**
- No timeout issues with `cp -r` for large folders
- User operations never blocked
- Controlled concurrency prevents API rate limits

### Search Performance
- **Query Embedding**: ~100ms (OpenAI)
- **Vector Search**: ~10-50ms (TiDB HNSW index)
- **Total**: ~150ms for typical search

**TiDB Cloud vector search maintains >90% recall rate** with HNSW indexing.

## Cost Estimation

### OpenAI Embeddings
- Model: `text-embedding-3-small`
- Cost: ~$0.02 per 1M tokens
- Example: 100 documents × 1000 words ≈ 130K tokens ≈ $0.003

### TiDB Cloud
- Serverless: Pay per use (RU consumption)
- Dedicated: Fixed monthly cost based on cluster size

### S3 Storage
- Standard storage: ~$0.023 per GB/month
- Example: 1000 documents × 10KB ≈ 10MB ≈ $0.0002/month

## Limitations

1. **No Updates**: Updating documents creates a new version (different digest). Old versions remain in S3 and TiDB.

2. **Deletion**: Not yet implemented. Use direct TiDB/S3 operations to clean up.

3. **Single Embedding Provider**: Only OpenAI is supported currently.

4. **TiFlash Required**: TiDB Cloud cluster must have TiFlash enabled for vector search.

5. **Indexing Visibility**: The `.indexing` status file is currently a placeholder (always shows "idle"). No API yet to check:
   - Whether a specific file has been indexed
   - Real-time queue depth or worker status
   - Indexing progress or completion percentage

## Troubleshooting

### "failed to connect to TiDB"
- Verify DSN connection string
- Ensure TLS is enabled for TiDB Cloud: `?tls=true`
- Check network connectivity and firewall rules

### "failed to initialize S3 client"
- Verify AWS credentials or IAM role
- Check bucket name and region
- For custom endpoints, ensure `s3_endpoint` is correct

### "failed to generate embeddings"
- Verify OpenAI API key is valid
- Check API rate limits and quotas
- Ensure network access to api.openai.com

### "vector search returns no results"
- Verify documents have been indexed (`ls /vectorfs/<namespace>/docs`)
- Check TiFlash is enabled on TiDB Cloud cluster
- Try broader search queries

### "file not appearing in search results immediately"
- Indexing happens asynchronously in background worker pool
- Small files (< 5KB): typically indexed within 1-3 seconds
- Large files (> 20KB): may take 10-15+ seconds to complete indexing
- Check server logs for indexing completion: `grep "Successfully indexed" /var/log/agfs.log`
- The `.indexing` status file currently doesn't show real-time status (placeholder)
- Workaround: Wait a few seconds after writing, then search again

## Example: Complete Workflow

```bash
# 1. Create namespace
mkdir /vectorfs/tech_docs

# 2. Add documents
echo "Kubernetes is a container orchestration platform..." > /vectorfs/tech_docs/docs/k8s.txt
echo "Docker provides containerization for applications..." > /vectorfs/tech_docs/docs/docker.txt
echo "Terraform enables infrastructure as code..." > /vectorfs/tech_docs/docs/terraform.txt

# 3. Search
grep "container management" /vectorfs/tech_docs/docs

# Returns semantically similar results:
# - k8s.txt (mentions container orchestration)
# - docker.txt (mentions containerization)
```

## Future Enhancements

- [ ] Real-time indexing status in `.indexing` file (queue depth, active workers, completion %)
- [ ] Per-file indexing status API (check if specific file has been indexed)
- [ ] Document update/delete operations
- [ ] Multiple embedding providers (Cohere, Hugging Face, etc.)
- [ ] Hybrid search (vector + keyword)
- [ ] Metadata filtering in search
- [ ] Configurable top-K results
- [ ] Re-indexing support
- [ ] Priority queue for indexing tasks

## See Also

- [TiDB Cloud Vector Search Documentation](https://docs.pingcap.com/tidbcloud/vector-search-index/)
- [OpenAI Embeddings API](https://platform.openai.com/docs/guides/embeddings)
- [AWS S3 Documentation](https://docs.aws.amazon.com/s3/)

## License

Apache 2.0
