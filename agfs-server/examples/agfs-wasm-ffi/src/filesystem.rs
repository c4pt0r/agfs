//! High-level agfs filesystem trait for WASM plugins

use crate::types::{Config, ConfigParameter, FileInfo, OpenFlag, Result, WriteFlag};

/// Filesystem trait that plugin developers should implement
///
/// All methods have default implementations that return appropriate errors,
/// so you only need to implement the operations your filesystem supports.
pub trait FileSystem {
    /// Returns the name of this filesystem plugin
    fn name(&self) -> &str;

    /// Returns the README/documentation for this plugin
    fn readme(&self) -> &str {
        "No documentation available"
    }

    /// Returns the list of configuration parameters this plugin supports
    fn config_params(&self) -> Vec<ConfigParameter> {
        Vec::new()
    }

    /// Validate the configuration before initialization
    ///
    /// This is called before `initialize` and should check that all
    /// required configuration values are present and valid.
    fn validate(&self, _config: &Config) -> Result<()> {
        Ok(())
    }

    /// Initialize the filesystem with the given configuration
    ///
    /// This is called after successful validation and before any
    /// filesystem operations.
    fn initialize(&mut self, _config: &Config) -> Result<()> {
        Ok(())
    }

    /// Shutdown the filesystem
    ///
    /// This is called when the filesystem is being unmounted.
    /// Use this to cleanup resources.
    fn shutdown(&mut self) -> Result<()> {
        Ok(())
    }

    /// Read data from a file
    ///
    /// # Arguments
    /// * `path` - The file path
    /// * `offset` - Starting position (0 for beginning)
    /// * `size` - Number of bytes to read (-1 for all)
    fn read(&self, _path: &str, _offset: i64, _size: i64) -> Result<Vec<u8>> {
        Err(crate::types::Error::ReadOnly)
    }

    /// Write data to a file
    ///
    /// # Arguments
    /// * `path` - The file path
    /// * `data` - Data to write
    /// * `offset` - Position to write at (-1 for append mode behavior)
    /// * `flags` - Write flags (CREATE, TRUNCATE, APPEND, etc.)
    ///
    /// # Returns
    /// Number of bytes written
    fn write(&mut self, _path: &str, _data: &[u8], _offset: i64, _flags: WriteFlag) -> Result<i64> {
        Err(crate::types::Error::ReadOnly)
    }

    /// Create a new empty file
    fn create(&mut self, _path: &str) -> Result<()> {
        Err(crate::types::Error::ReadOnly)
    }

    /// Create a new directory
    fn mkdir(&mut self, _path: &str, _perm: u32) -> Result<()> {
        Err(crate::types::Error::ReadOnly)
    }

    /// Remove a file or empty directory
    fn remove(&mut self, _path: &str) -> Result<()> {
        Err(crate::types::Error::ReadOnly)
    }

    /// Remove a file or directory and all its contents
    fn remove_all(&mut self, _path: &str) -> Result<()> {
        Err(crate::types::Error::ReadOnly)
    }

    /// Get file information
    fn stat(&self, path: &str) -> Result<FileInfo>;

    /// List directory contents
    fn readdir(&self, path: &str) -> Result<Vec<FileInfo>>;

    /// Rename/move a file or directory
    fn rename(&mut self, _old_path: &str, _new_path: &str) -> Result<()> {
        Err(crate::types::Error::ReadOnly)
    }

    /// Change file permissions
    fn chmod(&mut self, _path: &str, _mode: u32) -> Result<()> {
        Err(crate::types::Error::ReadOnly)
    }
}

/// Read-only filesystem helper
///
/// This trait provides common functionality for read-only filesystems.
/// Implement this instead of `FileSystem` if your filesystem is read-only.
pub trait ReadOnlyFileSystem {
    /// Returns the name of this filesystem plugin
    fn name(&self) -> &str;

    /// Returns the README/documentation for this plugin
    fn readme(&self) -> &str {
        "No documentation available"
    }

    /// Read data from a file
    fn read(&self, path: &str, offset: i64, size: i64) -> Result<Vec<u8>>;

    /// Get file information
    fn stat(&self, path: &str) -> Result<FileInfo>;

    /// List directory contents
    fn readdir(&self, path: &str) -> Result<Vec<FileInfo>>;
}

// Automatically implement FileSystem for any ReadOnlyFileSystem
impl<T: ReadOnlyFileSystem> FileSystem for T {
    fn name(&self) -> &str {
        ReadOnlyFileSystem::name(self)
    }

    fn readme(&self) -> &str {
        ReadOnlyFileSystem::readme(self)
    }

    fn read(&self, path: &str, offset: i64, size: i64) -> Result<Vec<u8>> {
        ReadOnlyFileSystem::read(self, path, offset, size)
    }

    fn stat(&self, path: &str) -> Result<FileInfo> {
        ReadOnlyFileSystem::stat(self, path)
    }

    fn readdir(&self, path: &str) -> Result<Vec<FileInfo>> {
        ReadOnlyFileSystem::readdir(self, path)
    }
}

/// FileHandle represents an open file handle with stateful operations
/// This trait is used for FUSE-like operations that require maintaining
/// file position and state across multiple read/write operations
pub trait FileHandle {
    /// Returns the unique identifier of this handle
    fn id(&self) -> i64;

    /// Returns the file path this handle is associated with
    fn path(&self) -> &str;

    /// Returns the open flags used when opening this handle
    fn flags(&self) -> OpenFlag;

    /// Read reads up to buf.len() bytes from the current position
    fn read(&mut self, buf: &mut [u8]) -> Result<usize>;

    /// ReadAt reads bytes from the specified offset (pread)
    fn read_at(&self, buf: &mut [u8], offset: i64) -> Result<usize>;

    /// Write writes data at the current position
    fn write(&mut self, data: &[u8]) -> Result<usize>;

    /// WriteAt writes data at the specified offset (pwrite)
    fn write_at(&self, data: &[u8], offset: i64) -> Result<usize>;

    /// Seek moves the read/write position
    /// whence: 0 = SEEK_SET (from start), 1 = SEEK_CUR (from current), 2 = SEEK_END (from end)
    fn seek(&mut self, offset: i64, whence: i32) -> Result<i64>;

    /// Sync synchronizes the file data to storage
    fn sync(&self) -> Result<()>;

    /// Close closes the handle and releases resources
    fn close(&mut self) -> Result<()>;

    /// Stat returns file information
    fn stat(&self) -> Result<FileInfo>;
}

/// HandleFS is implemented by file systems that support stateful file handles
/// This is optional - file systems that don't support handles can still work
/// with the basic FileSystem interface
pub trait HandleFS: FileSystem {
    /// Opens a file and returns the handle ID for stateful operations
    /// flags: OpenFlag bits (O_RDONLY, O_WRONLY, O_RDWR, O_APPEND, O_CREATE, O_EXCL, O_TRUNC)
    /// mode: file permission mode (used when creating new files)
    /// Returns the handle ID as i64
    fn open_handle(&mut self, path: &str, flags: OpenFlag, mode: u32) -> Result<i64>;

    /// Read from handle at current position, returns bytes read
    fn handle_read(&mut self, id: i64, buf: &mut [u8]) -> Result<usize>;

    /// Read from handle at specified offset (pread)
    fn handle_read_at(&self, id: i64, buf: &mut [u8], offset: i64) -> Result<usize>;

    /// Write to handle at current position, returns bytes written
    fn handle_write(&mut self, id: i64, data: &[u8]) -> Result<usize>;

    /// Write to handle at specified offset (pwrite)
    fn handle_write_at(&self, id: i64, data: &[u8], offset: i64) -> Result<usize>;

    /// Seek handle position
    fn handle_seek(&mut self, id: i64, offset: i64, whence: i32) -> Result<i64>;

    /// Sync handle data
    fn handle_sync(&self, id: i64) -> Result<()>;

    /// Stat via handle
    fn handle_stat(&self, id: i64) -> Result<FileInfo>;

    /// Get handle info (path, flags)
    fn handle_info(&self, id: i64) -> Result<(String, OpenFlag)>;

    /// Closes a handle by its ID
    fn close_handle(&mut self, id: i64) -> Result<()>;
}
