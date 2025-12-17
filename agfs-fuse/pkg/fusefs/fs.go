package fusefs

import (
	"context"
	"net/http"
	"sync"
	"syscall"
	"time"

	agfs "github.com/c4pt0r/agfs/agfs-sdk/go"
	"github.com/dongxuny/agfs-fuse/pkg/cache"
	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// AGFSFS is the root of the FUSE file system
type AGFSFS struct {
	fs.Inode

	client    *agfs.Client
	handles   *HandleManager
	metaCache *cache.MetadataCache
	dirCache  *cache.DirectoryCache
	cacheTTL  time.Duration
	mu        sync.RWMutex
}

// Config contains filesystem configuration
type Config struct {
	ServerURL string
	CacheTTL  time.Duration
	Debug     bool
}

// NewAGFSFS creates a new AGFS FUSE filesystem
func NewAGFSFS(config Config) *AGFSFS {
	// Use longer timeout for FUSE operations (streams may block)
	httpClient := &http.Client{
		Timeout: 60 * time.Second,
	}
	client := agfs.NewClientWithHTTPClient(config.ServerURL, httpClient)

	return &AGFSFS{
		client:    client,
		handles:   NewHandleManager(client),
		metaCache: cache.NewMetadataCache(config.CacheTTL),
		dirCache:  cache.NewDirectoryCache(config.CacheTTL),
		cacheTTL:  config.CacheTTL,
	}
}

// Close closes the filesystem and releases resources
func (root *AGFSFS) Close() error {
	// Close all open handles
	if err := root.handles.CloseAll(); err != nil {
		return err
	}

	// Clear caches
	root.metaCache.Clear()
	root.dirCache.Clear()

	return nil
}

// Statfs returns filesystem statistics
func (root *AGFSFS) Statfs(ctx context.Context, out *fuse.StatfsOut) syscall.Errno {
	// Return some reasonable defaults
	out.Blocks = 1024 * 1024 * 1024 // 1TB
	out.Bfree = 512 * 1024 * 1024   // 512GB free
	out.Bavail = 512 * 1024 * 1024  // 512GB available
	out.Files = 1000000             // 1M files
	out.Ffree = 500000              // 500K free inodes
	out.Bsize = 4096                // 4KB block size
	out.NameLen = 255               // Max filename length
	out.Frsize = 4096               // Fragment size

	return 0
}

// invalidateCache invalidates cache for a path and its parent directory
func (root *AGFSFS) invalidateCache(path string) {
	root.metaCache.Invalidate(path)

	// Invalidate parent directory listing
	parent := getParentPath(path)
	if parent != "" {
		root.dirCache.Invalidate(parent)
	}
}

// getParentPath returns the parent directory path
func getParentPath(path string) string {
	if path == "" || path == "/" {
		return ""
	}

	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			if i == 0 {
				return "/"
			}
			return path[:i]
		}
	}

	return "/"
}

// modeToFileMode converts AGFS mode to os.FileMode
func modeToFileMode(mode uint32) uint32 {
	return mode
}

// fileModeToMode converts os.FileMode to AGFS mode
func fileModeToMode(mode uint32) uint32 {
	return mode
}

// getStableMode returns mode with file type bits for StableAttr
func getStableMode(info *agfs.FileInfo) uint32 {
	mode := modeToFileMode(info.Mode)
	if info.IsSymlink {
		mode |= syscall.S_IFLNK
	} else if info.IsDir {
		mode |= syscall.S_IFDIR
	} else {
		mode |= syscall.S_IFREG
	}
	return mode
}

// Interface assertions for root node
var _ = (fs.NodeGetattrer)((*AGFSFS)(nil))
var _ = (fs.NodeLookuper)((*AGFSFS)(nil))
var _ = (fs.NodeReaddirer)((*AGFSFS)(nil))

// Getattr returns attributes for the root directory
func (root *AGFSFS) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	// Root is always a directory
	out.Mode = 0755 | syscall.S_IFDIR
	out.Size = 4096
	return 0
}

// Lookup looks up a child node in the root directory
func (root *AGFSFS) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	childPath := "/" + name

	// Try cache first
	var info *agfs.FileInfo
	if cached, ok := root.metaCache.Get(childPath); ok {
		info = cached
	} else {
		// Fetch from server
		var err error
		info, err = root.client.Stat(childPath)
		if err != nil {
			return nil, syscall.ENOENT
		}
		// Cache the result
		root.metaCache.Set(childPath, info)
	}

	fillAttr(&out.Attr, info)

	// Create child node
	stable := fs.StableAttr{
		Mode: getStableMode(info),
	}

	child := &AGFSNode{
		root: root,
		path: childPath,
	}

	return root.NewInode(ctx, child, stable), 0
}

// Readdir reads root directory contents
func (root *AGFSFS) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	rootPath := "/"

	// Try cache first
	var files []agfs.FileInfo
	if cached, ok := root.dirCache.Get(rootPath); ok {
		files = cached
	} else {
		// Fetch from server
		var err error
		files, err = root.client.ReadDir(rootPath)
		if err != nil {
			return nil, syscall.EIO
		}
		// Cache the result
		root.dirCache.Set(rootPath, files)
	}

	// Convert to FUSE entries
	entries := make([]fuse.DirEntry, 0, len(files))
	for _, f := range files {
		entry := fuse.DirEntry{
			Name: f.Name,
			Mode: getStableMode(&f),
		}
		entries = append(entries, entry)
	}

	return fs.NewListDirStream(entries), 0
}
