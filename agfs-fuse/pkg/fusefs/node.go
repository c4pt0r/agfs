package fusefs

import (
	"context"
	"path/filepath"
	"syscall"

	agfs "github.com/c4pt0r/agfs/agfs-sdk/go"
	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// AGFSNode represents a file or directory node
type AGFSNode struct {
	fs.Inode

	root *AGFSFS
	path string
}

var _ = (fs.NodeGetattrer)((*AGFSNode)(nil))
var _ = (fs.NodeLookuper)((*AGFSNode)(nil))
var _ = (fs.NodeReaddirer)((*AGFSNode)(nil))
var _ = (fs.NodeMkdirer)((*AGFSNode)(nil))
var _ = (fs.NodeRmdirer)((*AGFSNode)(nil))
var _ = (fs.NodeUnlinker)((*AGFSNode)(nil))
var _ = (fs.NodeRenamer)((*AGFSNode)(nil))
var _ = (fs.NodeCreater)((*AGFSNode)(nil))
var _ = (fs.NodeOpener)((*AGFSNode)(nil))
var _ = (fs.NodeSetattrer)((*AGFSNode)(nil))

// Getattr returns file attributes
func (n *AGFSNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	// Try cache first
	if cached, ok := n.root.metaCache.Get(n.path); ok {
		fillAttr(&out.Attr, cached)
		out.SetTimeout(n.root.cacheTTL)
		return 0
	}

	// Fetch from server
	info, err := n.root.client.Stat(n.path)
	if err != nil {
		return syscall.ENOENT
	}

	// Cache the result
	n.root.metaCache.Set(n.path, info)

	fillAttr(&out.Attr, info)

	return 0
}

// Lookup looks up a child node
func (n *AGFSNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	childPath := filepath.Join(n.path, name)

	// Try cache first
	var info *agfs.FileInfo
	if cached, ok := n.root.metaCache.Get(childPath); ok {
		info = cached
	} else {
		// Fetch from server
		var err error
		info, err = n.root.client.Stat(childPath)
		if err != nil {
			return nil, syscall.ENOENT
		}
		// Cache the result
		n.root.metaCache.Set(childPath, info)
	}

	fillAttr(&out.Attr, info)

	// Create child node
	stable := fs.StableAttr{
		Mode: getStableMode(info),
	}

	child := &AGFSNode{
		root: n.root,
		path: childPath,
	}

	return n.NewInode(ctx, child, stable), 0
}

// Readdir reads directory contents
func (n *AGFSNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	// Try cache first
	var files []agfs.FileInfo
	if cached, ok := n.root.dirCache.Get(n.path); ok {
		files = cached
	} else {
		// Fetch from server
		var err error
		files, err = n.root.client.ReadDir(n.path)
		if err != nil {
			return nil, syscall.EIO
		}
		// Cache the result
		n.root.dirCache.Set(n.path, files)
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

// Mkdir creates a directory
func (n *AGFSNode) Mkdir(ctx context.Context, name string, mode uint32, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	childPath := filepath.Join(n.path, name)

	err := n.root.client.Mkdir(childPath, mode)
	if err != nil {
		return nil, syscall.EIO
	}

	// Invalidate caches
	n.root.invalidateCache(childPath)

	// Fetch new file info
	info, err := n.root.client.Stat(childPath)
	if err != nil {
		return nil, syscall.EIO
	}

	fillAttr(&out.Attr, info)

	stable := fs.StableAttr{
		Mode: getStableMode(info),
	}

	child := &AGFSNode{
		root: n.root,
		path: childPath,
	}

	return n.NewInode(ctx, child, stable), 0
}

// Rmdir removes a directory
func (n *AGFSNode) Rmdir(ctx context.Context, name string) syscall.Errno {
	childPath := filepath.Join(n.path, name)

	err := n.root.client.Remove(childPath)
	if err != nil {
		return syscall.EIO
	}

	// Invalidate caches
	n.root.invalidateCache(childPath)

	return 0
}

// Unlink removes a file
func (n *AGFSNode) Unlink(ctx context.Context, name string) syscall.Errno {
	childPath := filepath.Join(n.path, name)

	err := n.root.client.Remove(childPath)
	if err != nil {
		return syscall.EIO
	}

	// Invalidate caches
	n.root.invalidateCache(childPath)

	return 0
}

// Rename renames a file or directory
func (n *AGFSNode) Rename(ctx context.Context, name string, newParent fs.InodeEmbedder, newName string, flags uint32) syscall.Errno {
	oldPath := filepath.Join(n.path, name)

	// Get new parent path
	newParentNode, ok := newParent.(*AGFSNode)
	if !ok {
		return syscall.EINVAL
	}
	newPath := filepath.Join(newParentNode.path, newName)

	err := n.root.client.Rename(oldPath, newPath)
	if err != nil {
		return syscall.EIO
	}

	// Invalidate caches
	n.root.invalidateCache(oldPath)
	n.root.invalidateCache(newPath)

	return 0
}

// Create creates a new file
func (n *AGFSNode) Create(ctx context.Context, name string, flags uint32, mode uint32, out *fuse.EntryOut) (node *fs.Inode, fh fs.FileHandle, fuseFlags uint32, errno syscall.Errno) {
	childPath := filepath.Join(n.path, name)

	// Create the file
	err := n.root.client.Create(childPath)
	if err != nil {
		return nil, nil, 0, syscall.EIO
	}

	// Invalidate caches
	n.root.invalidateCache(childPath)

	// Open the file with the requested flags
	openFlags := convertOpenFlags(flags)
	fuseHandle, err := n.root.handles.Open(childPath, openFlags, mode)
	if err != nil {
		return nil, nil, 0, syscall.EIO
	}

	// Fetch file info
	info, err := n.root.client.Stat(childPath)
	if err != nil {
		n.root.handles.Close(fuseHandle)
		return nil, nil, 0, syscall.EIO
	}

	fillAttr(&out.Attr, info)

	stable := fs.StableAttr{
		Mode: getStableMode(info),
	}

	child := &AGFSNode{
		root: n.root,
		path: childPath,
	}

	childInode := n.NewInode(ctx, child, stable)

	fileHandle := &AGFSFileHandle{
		node:   child,
		handle: fuseHandle,
	}

	return childInode, fileHandle, fuse.FOPEN_DIRECT_IO, 0
}

// Open opens a file
func (n *AGFSNode) Open(ctx context.Context, flags uint32) (fh fs.FileHandle, fuseFlags uint32, errno syscall.Errno) {
	openFlags := convertOpenFlags(flags)
	fuseHandle, err := n.root.handles.Open(n.path, openFlags, 0644)
	if err != nil {
		return nil, 0, syscall.EIO
	}

	fileHandle := &AGFSFileHandle{
		node:   n,
		handle: fuseHandle,
	}

	// Use DIRECT_IO for files with unknown/dynamic size (like queuefs control files)
	// This tells FUSE to ignore cached size and always read from the filesystem
	return fileHandle, fuse.FOPEN_DIRECT_IO, 0
}

// Setattr sets file attributes
func (n *AGFSNode) Setattr(ctx context.Context, f fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	// Handle chmod
	if mode, ok := in.GetMode(); ok {
		err := n.root.client.Chmod(n.path, mode)
		if err != nil {
			return syscall.EIO
		}

		// Invalidate cache
		n.root.metaCache.Invalidate(n.path)
	}

	// Handle truncate (size change)
	if size, ok := in.GetSize(); ok {
		err := n.root.client.Truncate(n.path, int64(size))
		if err != nil {
			return syscall.EIO
		}

		// Invalidate cache
		n.root.metaCache.Invalidate(n.path)
	}

	// Return updated attributes
	return n.Getattr(ctx, f, out)
}

// fillAttr fills FUSE attributes from AGFS FileInfo
func fillAttr(out *fuse.Attr, info *agfs.FileInfo) {
	out.Mode = modeToFileMode(info.Mode)
	out.Size = uint64(info.Size)
	out.Mtime = uint64(info.ModTime.Unix())
	out.Mtimensec = uint32(info.ModTime.Nanosecond())
	out.Atime = out.Mtime
	out.Atimensec = out.Mtimensec
	out.Ctime = out.Mtime
	out.Ctimensec = out.Mtimensec

	// Set owner to current user so they have proper read/write permissions
	out.Uid = uint32(syscall.Getuid())
	out.Gid = uint32(syscall.Getgid())

	if info.IsDir {
		out.Mode |= syscall.S_IFDIR
	} else {
		out.Mode |= syscall.S_IFREG
	}
}

// convertOpenFlags converts FUSE open flags to AGFS OpenFlag
func convertOpenFlags(flags uint32) agfs.OpenFlag {
	accessMode := flags & syscall.O_ACCMODE

	var openFlag agfs.OpenFlag

	switch accessMode {
	case syscall.O_RDONLY:
		openFlag = agfs.OpenFlagReadOnly
	case syscall.O_WRONLY:
		openFlag = agfs.OpenFlagWriteOnly
	case syscall.O_RDWR:
		openFlag = agfs.OpenFlagReadWrite
	}

	if flags&syscall.O_APPEND != 0 {
		openFlag |= agfs.OpenFlagAppend
	}
	if flags&syscall.O_CREAT != 0 {
		openFlag |= agfs.OpenFlagCreate
	}
	if flags&syscall.O_EXCL != 0 {
		openFlag |= agfs.OpenFlagExclusive
	}
	if flags&syscall.O_TRUNC != 0 {
		openFlag |= agfs.OpenFlagTruncate
	}
	if flags&syscall.O_SYNC != 0 {
		openFlag |= agfs.OpenFlagSync
	}

	return openFlag
}
