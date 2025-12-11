package agfs

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_Create(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/files" {
			t.Errorf("expected /api/v1/files, got %s", r.URL.Path)
		}
		if r.URL.Query().Get("path") != "/test/file.txt" {
			t.Errorf("expected path=/test/file.txt, got %s", r.URL.Query().Get("path"))
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(SuccessResponse{Message: "file created"})
	}))
	defer server.Close()

	client := NewClient(server.URL)
	err := client.Create("/test/file.txt")
	if err != nil {
		t.Errorf("Create failed: %v", err)
	}
}

func TestClient_Read(t *testing.T) {
	expectedData := []byte("hello world")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/files" {
			t.Errorf("expected /api/v1/files, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		w.Write(expectedData)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	data, err := client.Read("/test/file.txt", 0, -1)
	if err != nil {
		t.Errorf("Read failed: %v", err)
	}
	if string(data) != string(expectedData) {
		t.Errorf("expected %s, got %s", expectedData, data)
	}
}

func TestClient_Write(t *testing.T) {
	testData := []byte("test content")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/files" {
			t.Errorf("expected /api/v1/files, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(SuccessResponse{Message: "OK"})
	}))
	defer server.Close()

	client := NewClient(server.URL)
	response, err := client.Write("/test/file.txt", testData)
	if err != nil {
		t.Errorf("Write failed: %v", err)
	}
	if string(response) != "OK" {
		t.Errorf("expected OK, got %s", response)
	}
}

func TestClient_Mkdir(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/directories" {
			t.Errorf("expected /api/v1/directories, got %s", r.URL.Path)
		}
		if r.URL.Query().Get("mode") != "755" {
			t.Errorf("expected mode=755, got %s", r.URL.Query().Get("mode"))
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(SuccessResponse{Message: "directory created"})
	}))
	defer server.Close()

	client := NewClient(server.URL)
	err := client.Mkdir("/test/dir", 0755)
	if err != nil {
		t.Errorf("Mkdir failed: %v", err)
	}
}

func TestClient_ErrorHandling(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "file not found"})
	}))
	defer server.Close()

	client := NewClient(server.URL)
	_, err := client.Read("/nonexistent", 0, -1)
	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestNormalizeBaseURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "full URL with /api/v1",
			input:    "http://localhost:8080/api/v1",
			expected: "http://localhost:8080/api/v1",
		},
		{
			name:     "URL without /api/v1",
			input:    "http://localhost:8080",
			expected: "http://localhost:8080/api/v1",
		},
		{
			name:     "URL with trailing slash",
			input:    "http://localhost:8080/",
			expected: "http://localhost:8080/api/v1",
		},
		{
			name:     "URL with /api/v1 and trailing slash",
			input:    "http://localhost:8080/api/v1/",
			expected: "http://localhost:8080/api/v1",
		},
		{
			name:     "malformed URL - just protocol",
			input:    "http:",
			expected: "http:", // Don't try to fix it, return as-is
		},
		{
			name:     "hostname with port",
			input:    "http://workstation:8080/api/v1",
			expected: "http://workstation:8080/api/v1",
		},
		{
			name:     "hostname with port no api path",
			input:    "http://workstation:8080",
			expected: "http://workstation:8080/api/v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeBaseURL(tt.input)
			if result != tt.expected {
				t.Errorf("normalizeBaseURL(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}
