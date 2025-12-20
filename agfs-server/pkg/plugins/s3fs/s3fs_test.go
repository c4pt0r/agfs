package s3fs

import (
	"os"
	"testing"

	"github.com/c4pt0r/agfs/agfs-server/pkg/filesystem"
)

// getTestConfig returns S3 config from environment variables
// Required: S3_TEST_BUCKET
// Optional: S3_TEST_REGION (default: us-east-1), S3_TEST_ENDPOINT, S3_TEST_ACCESS_KEY, S3_TEST_SECRET_KEY
func getTestConfig() (S3Config, bool) {
	bucket := os.Getenv("S3_TEST_BUCKET")
	if bucket == "" {
		return S3Config{}, false
	}

	region := os.Getenv("S3_TEST_REGION")
	if region == "" {
		region = "us-east-1"
	}

	return S3Config{
		Bucket:          bucket,
		Region:          region,
		Endpoint:        os.Getenv("S3_TEST_ENDPOINT"),
		AccessKeyID:     os.Getenv("S3_TEST_ACCESS_KEY"),
		SecretAccessKey: os.Getenv("S3_TEST_SECRET_KEY"),
		DisableSSL:      os.Getenv("S3_TEST_DISABLE_SSL") == "true",
		Prefix:          "agfs-test",
	}, true
}

func newTestFS(t *testing.T) *S3FS {
	t.Helper()

	cfg, ok := getTestConfig()
	if !ok {
		t.Skip("S3 test environment not configured (set S3_TEST_BUCKET)")
	}

	fs, err := NewS3FS(cfg)
	if err != nil {
		t.Fatalf("NewS3FS failed: %v", err)
	}
	return fs
}

// readIgnoreEOF reads file content, handling the case where EOF is returned with data
func readIgnoreEOF(fs *S3FS, path string) ([]byte, error) {
	return fs.Read(path, 0, -1)
}

// TestS3FSTruncate tests the Truncate method
func TestS3FSTruncate(t *testing.T) {
	fs := newTestFS(t)
	path := "/truncate_test.txt"

	// Clean up before and after test
	defer fs.Remove(path)
	fs.Remove(path)

	// Create file with initial content
	_, err := fs.Write(path, []byte("Hello, World!"), -1, filesystem.WriteFlagCreate)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Test 1: Truncate to zero
	t.Run("TruncateToZero", func(t *testing.T) {
		// Restore content first
		_, err := fs.Write(path, []byte("Hello, World!"), -1, filesystem.WriteFlagTruncate)
		if err != nil {
			t.Fatalf("Write failed: %v", err)
		}

		err = fs.Truncate(path, 0)
		if err != nil {
			t.Fatalf("Truncate to zero failed: %v", err)
		}

		content, err := readIgnoreEOF(fs, path)
		if err != nil {
			t.Fatalf("Read failed: %v", err)
		}
		if len(content) != 0 {
			t.Errorf("Expected empty file, got %d bytes: %q", len(content), content)
		}

		info, _ := fs.Stat(path)
		if info.Size != 0 {
			t.Errorf("Expected size 0, got %d", info.Size)
		}
	})

	// Test 2: Truncate to shrink file
	t.Run("TruncateShrink", func(t *testing.T) {
		// Write new content
		_, err := fs.Write(path, []byte("Hello, World!"), -1, filesystem.WriteFlagTruncate)
		if err != nil {
			t.Fatalf("Write failed: %v", err)
		}

		// Truncate to 5 bytes ("Hello")
		err = fs.Truncate(path, 5)
		if err != nil {
			t.Fatalf("Truncate shrink failed: %v", err)
		}

		content, err := readIgnoreEOF(fs, path)
		if err != nil {
			t.Fatalf("Read failed: %v", err)
		}
		if string(content) != "Hello" {
			t.Errorf("Content mismatch: got %q, want %q", string(content), "Hello")
		}
	})

	// Test 3: Truncate to extend file (pad with zeros)
	t.Run("TruncateExtend", func(t *testing.T) {
		// Write small content
		_, err := fs.Write(path, []byte("Hi"), -1, filesystem.WriteFlagTruncate)
		if err != nil {
			t.Fatalf("Write failed: %v", err)
		}

		// Extend to 10 bytes
		err = fs.Truncate(path, 10)
		if err != nil {
			t.Fatalf("Truncate extend failed: %v", err)
		}

		content, err := readIgnoreEOF(fs, path)
		if err != nil {
			t.Fatalf("Read failed: %v", err)
		}
		if len(content) != 10 {
			t.Errorf("Expected 10 bytes, got %d", len(content))
		}
		// First 2 bytes should be "Hi", rest should be zero
		if string(content[:2]) != "Hi" {
			t.Errorf("First 2 bytes should be 'Hi', got %q", string(content[:2]))
		}
		for i := 2; i < 10; i++ {
			if content[i] != 0 {
				t.Errorf("Byte %d should be 0, got %d", i, content[i])
			}
		}
	})

	// Test 4: Truncate same size (no-op)
	t.Run("TruncateSameSize", func(t *testing.T) {
		_, err := fs.Write(path, []byte("Test"), -1, filesystem.WriteFlagTruncate)
		if err != nil {
			t.Fatalf("Write failed: %v", err)
		}

		err = fs.Truncate(path, 4)
		if err != nil {
			t.Fatalf("Truncate same size failed: %v", err)
		}

		content, err := readIgnoreEOF(fs, path)
		if err != nil {
			t.Fatalf("Read failed: %v", err)
		}
		if string(content) != "Test" {
			t.Errorf("Content mismatch: got %q, want %q", string(content), "Test")
		}
	})

	// Test 5: Truncate non-existent file should fail
	t.Run("TruncateNonExistent", func(t *testing.T) {
		err := fs.Truncate("/nonexistent_truncate_test.txt", 0)
		if err == nil {
			t.Error("Expected error for truncating non-existent file")
		}
	})

	// Test 6: Truncate directory should fail
	t.Run("TruncateDirectory", func(t *testing.T) {
		dirPath := "/truncate_testdir/"
		defer fs.Remove(dirPath)

		err := fs.Mkdir(dirPath, 0755)
		if err != nil {
			t.Fatalf("Mkdir failed: %v", err)
		}

		err = fs.Truncate(dirPath, 0)
		if err == nil {
			t.Error("Expected error for truncating directory")
		}
	})
}

// TestS3FSTruncateInterface verifies S3FS implements Truncater interface
func TestS3FSTruncateInterface(t *testing.T) {
	fs := newTestFS(t)

	// Verify interface implementation
	var _ filesystem.Truncater = fs

	// Also test via interface
	truncater, ok := interface{}(fs).(filesystem.Truncater)
	if !ok {
		t.Fatal("S3FS does not implement filesystem.Truncater")
	}

	// Create a file and truncate via interface
	path := "/interface_truncate_test.txt"
	defer fs.Remove(path)

	_, err := fs.Write(path, []byte("Hello, World!"), -1, filesystem.WriteFlagCreate)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	err = truncater.Truncate(path, 5)
	if err != nil {
		t.Fatalf("Truncate via interface failed: %v", err)
	}

	content, err := readIgnoreEOF(fs, path)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if string(content) != "Hello" {
		t.Errorf("Content mismatch: got %q, want %q", string(content), "Hello")
	}
}
