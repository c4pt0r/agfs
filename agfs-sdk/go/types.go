package agfs

import "time"

// MetaData represents structured metadata for files and directories
type MetaData struct {
	Name    string            // Plugin name or identifier
	Type    string            // Type classification of the file/directory
	Content map[string]string // Additional extensible metadata
}

// FileInfo represents file metadata similar to os.FileInfo
type FileInfo struct {
	Name      string
	Size      int64
	Mode      uint32
	ModTime   time.Time
	IsDir     bool
	IsSymlink bool     // True if this is a symbolic link
	Meta      MetaData // Structured metadata for additional information
}

// OpenFlag represents file open flags
type OpenFlag int

const (
	OpenFlagReadOnly  OpenFlag = 0
	OpenFlagWriteOnly OpenFlag = 1
	OpenFlagReadWrite OpenFlag = 2
	OpenFlagAppend    OpenFlag = 1024
	OpenFlagCreate    OpenFlag = 64
	OpenFlagExclusive OpenFlag = 128
	OpenFlagTruncate  OpenFlag = 512
	OpenFlagSync      OpenFlag = 1052672
)

// HandleInfo represents an open file handle
type HandleInfo struct {
	ID    int64    `json:"id"`
	Path  string   `json:"path"`
	Flags OpenFlag `json:"flags"`
}

// HandleResponse is the response for handle operations
type HandleResponse struct {
	HandleID int64 `json:"handle_id"`
}
