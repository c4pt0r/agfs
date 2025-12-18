package fusefs

import (
	"context"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// AGFSFileHandle represents an open file handle
type AGFSFileHandle struct {
	node   *AGFSNode
	handle uint64
}

var _ = (fs.FileReader)((*AGFSFileHandle)(nil))
var _ = (fs.FileWriter)((*AGFSFileHandle)(nil))
var _ = (fs.FileFsyncer)((*AGFSFileHandle)(nil))
var _ = (fs.FileReleaser)((*AGFSFileHandle)(nil))
var _ = (fs.FileGetattrer)((*AGFSFileHandle)(nil))

// Read reads data from the file
func (fh *AGFSFileHandle) Read(ctx context.Context, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	data, err := fh.node.root.handles.Read(fh.handle, off, len(dest))
	if err != nil {
		return nil, syscall.EIO
	}

	return fuse.ReadResultData(data), 0
}

// Write writes data to the file
func (fh *AGFSFileHandle) Write(ctx context.Context, data []byte, off int64) (written uint32, errno syscall.Errno) {
	n, err := fh.node.root.handles.Write(fh.handle, data, off)
	if err != nil {
		return 0, syscall.EIO
	}

	// Invalidate metadata cache since file size may have changed
	path := fh.node.getPath()
	fh.node.root.metaCache.Invalidate(path)

	return uint32(n), 0
}

// Fsync syncs file data to storage
func (fh *AGFSFileHandle) Fsync(ctx context.Context, flags uint32) syscall.Errno {
	err := fh.node.root.handles.Sync(fh.handle)
	if err != nil {
		return syscall.EIO
	}

	return 0
}

// Release releases the file handle
func (fh *AGFSFileHandle) Release(ctx context.Context) syscall.Errno {
	err := fh.node.root.handles.Close(fh.handle)
	if err != nil {
		return syscall.EIO
	}

	return 0
}

// Getattr returns file attributes
func (fh *AGFSFileHandle) Getattr(ctx context.Context, out *fuse.AttrOut) syscall.Errno {
	return fh.node.Getattr(ctx, fh, out)
}
