package vectorfs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	log "github.com/sirupsen/logrus"
)

// EmbeddingConfig holds embedding configuration
type EmbeddingConfig struct {
	Provider  string // Provider name (openai)
	APIKey    string // API key
	Model     string // Model name
	Dimension int    // Embedding dimension
}

// EmbeddingClient handles embedding generation
type EmbeddingClient struct {
	provider  string
	apiKey    string
	model     string
	dimension int
	client    *http.Client
}

// NewEmbeddingClient creates a new embedding client
func NewEmbeddingClient(cfg EmbeddingConfig) (*EmbeddingClient, error) {
	if cfg.Provider != "openai" {
		return nil, fmt.Errorf("unsupported embedding provider: %s", cfg.Provider)
	}

	if cfg.APIKey == "" {
		return nil, fmt.Errorf("API key is required")
	}

	log.Infof("[vectorfs/embedding] Initialized %s embedding client (model: %s, dim: %d)",
		cfg.Provider, cfg.Model, cfg.Dimension)

	return &EmbeddingClient{
		provider:  cfg.Provider,
		apiKey:    cfg.APIKey,
		model:     cfg.Model,
		dimension: cfg.Dimension,
		client: &http.Client{
			Timeout: 60 * time.Second, // Prevent indefinite blocking on API calls
		},
	}, nil
}

// GetDimension returns the embedding dimension
func (e *EmbeddingClient) GetDimension() int {
	return e.dimension
}

// GenerateEmbedding generates an embedding for the given text
func (e *EmbeddingClient) GenerateEmbedding(text string) ([]float32, error) {
	switch e.provider {
	case "openai":
		return e.generateOpenAIEmbedding(text)
	default:
		return nil, fmt.Errorf("unsupported provider: %s", e.provider)
	}
}

// GenerateBatchEmbeddings generates embeddings for multiple texts
func (e *EmbeddingClient) GenerateBatchEmbeddings(texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	switch e.provider {
	case "openai":
		return e.generateOpenAIBatchEmbeddingsImpl(texts)
	default:
		return nil, fmt.Errorf("unsupported provider: %s", e.provider)
	}
}

// OpenAI API structures
type openAIEmbeddingRequest struct {
	Input string `json:"input"`
	Model string `json:"model"`
}

type openAIBatchEmbeddingRequest struct {
	Input []string `json:"input"`
	Model string   `json:"model"`
}

type openAIEmbeddingResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Model string `json:"model"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

// generateOpenAIEmbedding generates embedding using OpenAI API
func (e *EmbeddingClient) generateOpenAIEmbedding(text string) ([]float32, error) {
	requestBody := openAIEmbeddingRequest{
		Input: text,
		Model: e.model,
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", "https://api.openai.com/v1/embeddings", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("OpenAI API error (status %d): %s", resp.StatusCode, string(body))
	}

	var response openAIEmbeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if len(response.Data) == 0 {
		return nil, fmt.Errorf("no embedding returned from API")
	}

	log.Debugf("[vectorfs/embedding] Generated embedding (tokens: %d)", response.Usage.TotalTokens)
	return response.Data[0].Embedding, nil
}

// generateOpenAIBatchEmbeddingsImpl generates embeddings for multiple texts using OpenAI
func (e *EmbeddingClient) generateOpenAIBatchEmbeddingsImpl(texts []string) ([][]float32, error) {
	// OpenAI supports batch requests
	requestBody := openAIBatchEmbeddingRequest{
		Input: texts,
		Model: e.model,
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", "https://api.openai.com/v1/embeddings", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("OpenAI API error (status %d): %s", resp.StatusCode, string(body))
	}

	var response openAIEmbeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if len(response.Data) != len(texts) {
		return nil, fmt.Errorf("expected %d embeddings, got %d", len(texts), len(response.Data))
	}

	// Sort by index to ensure order matches input
	embeddings := make([][]float32, len(texts))
	for _, data := range response.Data {
		embeddings[data.Index] = data.Embedding
	}

	log.Debugf("[vectorfs/embedding] Generated %d embeddings (tokens: %d)",
		len(embeddings), response.Usage.TotalTokens)
	return embeddings, nil
}
