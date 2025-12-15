package gptfs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/c4pt0r/agfs/agfs-server/pkg/filesystem"
	"github.com/c4pt0r/agfs/agfs-server/pkg/plugin"
	"github.com/c4pt0r/agfs/agfs-server/pkg/plugin/config"
	"github.com/c4pt0r/agfs/agfs-server/pkg/plugins/localfs"
	log "github.com/sirupsen/logrus"
)

const (
	PluginName = "gptfs"
)

type Gptfs struct {
	gptDriver *gptDriver
	apiHost   string
	apiKey    string
}

type Job struct {
	ID           string        `json:"id"`
	RequestPath  string        `json:"request_path"`
	ResponsePath string        `json:"response_path"`
	Data         []byte        `json:"data"`
	Timestamp    time.Time     `json:"timestamp"`
	Status       JobStatus     `json:"status"`
	Error        string        `json:"error,omitempty"`
	Duration     time.Duration `json:"duration,omitempty"`
}

type JobStatus string

const (
	JobStatusPending    JobStatus = "pending"
	JobStatusProcessing JobStatus = "processing"
	JobStatusCompleted  JobStatus = "completed"
	JobStatusFailed     JobStatus = "failed"
)

type JobRequest struct {
	JobID     string `json:"job_id"`
	Status    string `json:"status"`
	Timestamp int64  `json:"timestamp"`
	Message   string `json:"message,omitempty"`
}

type gptDriver struct {
	client    *http.Client
	apiKey    string
	apiHost   string
	mountPath string
	baseFS    *localfs.LocalFS // 使用 LocalFS 持久化存储

	// 异步处理
	jobQueue chan *Job
	workers  int
	wg       sync.WaitGroup
	ctx      context.Context
	cancel   context.CancelFunc

	// 状态管理
	jobs sync.Map // map[string]*Job
	mu   sync.RWMutex
}

func NewGptfs() *Gptfs {
	return &Gptfs{}
}

func (d *gptDriver) Write(path string, data []byte, offset int64, flags filesystem.WriteFlag) (int64, error) {
	n, err := d.baseFS.Write(path, data, offset, flags)
	if err != nil {
		return 0, err
	}

	log.Infof("[gptfs] Detected file write in inbox, creating async job: %s", path)

	fileName := filepath.Base(path)
	baseName := fileName[:len(fileName)-len(filepath.Ext(fileName))]
	responseFile := filepath.Join("outbox", baseName+"_response.txt")
	jobStatusFile := filepath.Join("outbox", baseName+"_status.json")

	jobID := d.generateJobID()

	job := &Job{
		ID:           jobID,
		RequestPath:  path,
		ResponsePath: responseFile,
		Data:         data,
		Timestamp:    time.Now(),
		Status:       JobStatusPending,
	}

	d.jobs.Store(job.ID, job)

	d.writeJobStatus(jobStatusFile, JobRequest{
		JobID:     job.ID,
		Status:    string(JobStatusPending),
		Timestamp: time.Now().Unix(),
		Message:   "Job queued for processing",
	})

	select {
	case d.jobQueue <- job:
		log.Infof("[gptfs] Job %s queued successfully", job.ID)
	default:
		errorMsg := "job queue is full, please try again later"
		job.Status = JobStatusFailed
		job.Error = errorMsg
		log.Warnf("[gptfs] Job %s rejected: %s", job.ID, errorMsg)

		d.writeJobStatus(jobStatusFile, JobRequest{
			JobID:     job.ID,
			Status:    string(JobStatusFailed),
			Timestamp: time.Now().Unix(),
			Message:   errorMsg,
		})
	}

	return n, nil
}

func (d *gptDriver) generateJobID() string {
	return fmt.Sprintf("job_%d", time.Now().UnixNano())
}

func (d *gptDriver) writeJobStatus(statusFile string, req JobRequest) {
	data, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		log.Errorf("[gptfs] Failed to marshal job status: %v", err)
		return
	}

	_, err = d.baseFS.Write(statusFile, data, -1,
		filesystem.WriteFlagCreate|filesystem.WriteFlagTruncate)
	if err != nil {
		log.Errorf("[gptfs] Failed to write job status: %v", err)
	}
}

func (d *gptDriver) startWorkers() {
	for i := 0; i < d.workers; i++ {
		d.wg.Add(1)
		go d.worker(i)
	}
	log.Infof("[gptfs] Started %d workers", d.workers)
}

func (d *gptDriver) worker(workerID int) {
	defer d.wg.Done()

	log.Infof("[gptfs] Worker %d started", workerID)

	for {
		select {
		case job := <-d.jobQueue:
			log.Infof("[gptfs] Worker %d processing job %s", workerID, job.ID)
			d.processJob(job)
		case <-d.ctx.Done():
			log.Infof("[gptfs] Worker %d shutting down", workerID)
			return
		}
	}
}

func (d *gptDriver) processJob(job *Job) {
	startTime := time.Now()
	job.Status = JobStatusProcessing

	// Use the same base name as the response file for status, e.g., outbox/<base>_status.json
	dir := filepath.Dir(job.ResponsePath)
	base := strings.TrimSuffix(filepath.Base(job.ResponsePath), "_response.txt")
	jobStatusFile := filepath.Join(dir, base+"_status.json")

	d.writeJobStatus(jobStatusFile, JobRequest{
		JobID:     job.ID,
		Status:    string(JobStatusProcessing),
		Timestamp: time.Now().Unix(),
		Message:   "Processing request...",
	})

	response, err := d.callOpenAI(job.Data)
	if err != nil {
		job.Duration = time.Since(startTime)
		job.Status = JobStatusFailed
		job.Error = err.Error()

		log.Errorf("[gptfs] Job %s failed: %v", job.ID, err)

		d.writeJobStatus(jobStatusFile, JobRequest{
			JobID:     job.ID,
			Status:    string(JobStatusFailed),
			Timestamp: time.Now().Unix(),
			Message:   fmt.Sprintf("API call failed: %s", err.Error()),
		})
		return
	}

	_, err = d.baseFS.Write(job.ResponsePath, response, -1,
		filesystem.WriteFlagCreate|filesystem.WriteFlagTruncate)
	if err != nil {
		job.Duration = time.Since(startTime)
		job.Status = JobStatusFailed
		job.Error = err.Error()

		log.Errorf("[gptfs] Job %s failed to write response: %v", job.ID, err)

		d.writeJobStatus(jobStatusFile, JobRequest{
			JobID:     job.ID,
			Status:    string(JobStatusFailed),
			Timestamp: time.Now().Unix(),
			Message:   fmt.Sprintf("Failed to write response: %s", err.Error()),
		})
		return
	}

	job.Duration = time.Since(startTime)
	job.Status = JobStatusCompleted

	log.Infof("[gptfs] Job %s completed in %v", job.ID, job.Duration)

	d.writeJobStatus(jobStatusFile, JobRequest{
		JobID:     job.ID,
		Status:    string(JobStatusCompleted),
		Timestamp: time.Now().Unix(),
		Message:   fmt.Sprintf("Completed in %v", job.Duration),
	})
}

func (d *gptDriver) callOpenAI(reqBody []byte) ([]byte, error) {
	const maxRetries = 3
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(attempt) * time.Second
			log.Warnf("[gptfs] API call attempt %d failed, retrying in %v: %v",
				attempt+1, backoff, lastErr)
			time.Sleep(backoff)
		}

		response, err := d.doAPICall(reqBody)
		if err == nil {
			return response, nil
		}
		lastErr = err

		if !isRetryableError(err) {
			break
		}
	}

	return nil, fmt.Errorf("failed after %d retries: %w", maxRetries, lastErr)
}

func (d *gptDriver) doAPICall(reqBody []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", d.apiHost, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+d.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var openaiResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(body, &openaiResp); err == nil && len(openaiResp.Choices) > 0 {
		content := openaiResp.Choices[0].Message.Content
		log.Infof("[gptfs] Successfully extracted content (%d bytes)", len(content))
		return []byte(content), nil
	}

	log.Warnf("[gptfs] Could not extract OpenAI content, returning raw response")
	return body, nil
}

func isRetryableError(err error) bool {
	errStr := err.Error()
	retryableErrors := []string{
		"timeout",
		"connection refused",
		"temporary failure",
		"network is unreachable",
		"no such host",
		"connection reset",
		"502", // Bad Gateway
		"503", // Service Unavailable
		"504", // Gateway Timeout
		"429", // Too Many Requests
	}

	for _, retryable := range retryableErrors {
		if strings.Contains(strings.ToLower(errStr), retryable) {
			return true
		}
	}
	return false
}

func (d *gptDriver) Create(path string) error {
	return d.baseFS.Create(path)
}

func (d *gptDriver) Mkdir(path string, perm uint32) error {
	return d.baseFS.Mkdir(path, perm)
}

func (d *gptDriver) RemoveAll(path string) error {
	return d.baseFS.RemoveAll(path)
}

func (d *gptDriver) ReadDir(path string) ([]filesystem.FileInfo, error) {
	return d.baseFS.ReadDir(path)
}

func (d *gptDriver) Rename(oldPath, newPath string) error {
	return d.baseFS.Rename(oldPath, newPath)
}

func (d *gptDriver) Chmod(path string, mode uint32) error {
	return d.baseFS.Chmod(path, mode)
}

func (d *gptDriver) Open(path string) (io.ReadCloser, error) {
	return d.baseFS.Open(path)
}

func (d *gptDriver) OpenWrite(path string) (io.WriteCloser, error) {
	return d.baseFS.OpenWrite(path)
}

func (d *gptDriver) Read(path string, offset int64, size int64) ([]byte, error) {
	return d.baseFS.Read(path, offset, size)
}

func (d *gptDriver) Remove(path string) error {
	return d.baseFS.Remove(path)
}

func (d *gptDriver) Stat(path string) (*filesystem.FileInfo, error) {
	return d.baseFS.Stat(path)
}

func (g *Gptfs) Name() string {
	return PluginName
}

func (g *Gptfs) Validate(cfg map[string]interface{}) error {
	allowedKeys := []string{"api_host", "api_key", "mount_path", "workers"}
	if err := config.ValidateOnlyKnownKeys(cfg, allowedKeys); err != nil {
		return err
	}

	if _, err := config.RequireString(cfg, "api_key"); err != nil {
		return err
	}

	if _, err := config.RequireString(cfg, "api_host"); err != nil {
		return err
	}

	if _, err := config.RequireString(cfg, "mount_path"); err != nil {
		return err
	}

	return nil
}

func (g *Gptfs) Initialize(config map[string]interface{}) error {
	apiKey := config["api_key"].(string)
	apiHost := config["api_host"].(string)
	mountPath := config["mount_path"].(string)

	if err := os.MkdirAll(mountPath, 0755); err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "already exists") {
			return fmt.Errorf("failed to create inbox directory: %w", err)
		}
	}

	baseFS, err := localfs.NewLocalFS(mountPath)
	if err != nil {
		return fmt.Errorf("failed to initialize localfs: %w", err)
	}

	if err := baseFS.Mkdir("inbox", 0755); err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "already exists") {
			return fmt.Errorf("failed to create inbox directory: %w", err)
		}
	}
	if err := baseFS.Mkdir("outbox", 0755); err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "already exists") {
			return fmt.Errorf("failed to create outbox directory: %w", err)
		}
	}

	workers := 3
	if w, ok := config["workers"].(int); ok && w > 0 {
		workers = w
	}

	ctx, cancel := context.WithCancel(context.Background())

	driver := &gptDriver{
		client:    &http.Client{Transport: &http.Transport{}},
		apiKey:    apiKey,
		apiHost:   apiHost,
		mountPath: mountPath,
		baseFS:    baseFS,
		jobQueue:  make(chan *Job, 100), // 缓冲队列
		workers:   workers,
		ctx:       ctx,
		cancel:    cancel,
	}

	driver.startWorkers()

	g.gptDriver = driver
	g.apiKey = apiKey
	g.apiHost = apiHost

	log.Infof("[gptfs] Initialized with mounth=%s, workers=%d", mountPath, workers)
	return nil
}

func (g *Gptfs) GetFileSystem() filesystem.FileSystem {
	return g.gptDriver
}

func (g *Gptfs) GetReadme() string {
	return `GPTFS Plugin - Async GPT Processing over Persistent Storage

This plugin provides an asynchronous interface to OpenAI-compatible APIs
with persistent file storage using LocalFS.

PATH LAYOUT:
  /agents/gptfs/
    inbox/                    # Write any file here to trigger API calls
      request.json           # Example: OpenAI request -> request_response.txt
      prompt.txt             # Example: Text prompt -> prompt_response.txt
      query.md               # Example: Markdown query -> query_response.txt
    outbox/
      request_response.txt   # Response for request.json
      request_status.json    # Status for request.json
      prompt_response.txt    # Response for prompt.txt
      prompt_status.json     # Status for prompt.txt
      query_response.txt     # Response for query.md
      query_status.json      # Status for query.md

WORKFLOW:
  1) Write any file to the gptfs mount path (e.g., inbox/request.json)
  2) File write returns immediately (async processing)
  3) Monitor outbox/{filename}_status.json for progress
  4) Read response from outbox/{filename}_response.txt when complete

EXAMPLE:
  # Write an OpenAI request
  echo '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"Say hello"}]}' > inbox/request.json
  # -> Creates outbox/request_response.txt and outbox/request_status.json

  # Write a text prompt
  echo "Tell me a joke" > inbox/prompt.txt
  # -> Creates outbox/prompt_response.txt and outbox/prompt_status.json

  # Write multiple requests concurrently
  echo "What is AI?" > inbox/qa1.txt
  echo "What is ML?" > inbox/qa2.txt
  # -> Creates separate response and status files for each

CONFIGURATION:
  api_host     - OpenAI-compatible endpoint
  api_key      - API authorization key
  data_dir     - Persistent storage directory
  workers      - Concurrent API workers (default: 3)
  mount_path   - Virtual mount path

FEATURES:
  - Asynchronous processing (non-blocking writes)
  - Persistent storage using LocalFS
  - Real-time job status tracking
  - Automatic retry with exponential backoff
  - Multiple concurrent workers
  - Detailed error handling and logging
`
}

func (g *Gptfs) GetConfigParams() []plugin.ConfigParameter {
	return []plugin.ConfigParameter{
		{
			Name:        "api_key",
			Type:        "string",
			Required:    true,
			Description: "API key for OpenAI-compatible service",
		},
		{
			Name:        "api_host",
			Type:        "string",
			Required:    true,
			Description: "OpenAI-compatible endpoint URL",
		},
		{
			Name:        "data_dir",
			Type:        "string",
			Required:    true,
			Description: "Directory for persistent storage",
		},
		{
			Name:        "workers",
			Type:        "int",
			Required:    false,
			Default:     "3",
			Description: "Number of concurrent API workers",
		},
	}
}

func (g *Gptfs) Shutdown() error {
	if g.gptDriver != nil {
		log.Infof("[gptfs] Shutting down, stopping workers...")
		g.gptDriver.cancel()
		g.gptDriver.wg.Wait()
		close(g.gptDriver.jobQueue)
	}
	return nil
}
