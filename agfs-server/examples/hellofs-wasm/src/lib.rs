//! HelloFS WASM - Filesystem plugin with host fs access demo
//!
//! Returns a single file with "Hello World" content
//! Also demonstrates accessing the host filesystem
//! Now with HandleFS support for FUSE-like stateful operations

use agfs_wasm_ffi::prelude::*;
use std::collections::HashMap;

/// Internal file handle state
struct HandleState {
    path: String,
    flags: OpenFlag,
    pos: i64,
    /// File content (for /hello.txt) or None for host files
    content: Option<Vec<u8>>,
    /// For host files, store the host path
    host_path: Option<String>,
}

/// Counter for generating unique handle IDs
static mut HANDLE_COUNTER: i64 = 0;

fn generate_handle_id() -> i64 {
    unsafe {
        HANDLE_COUNTER += 1;
        HANDLE_COUNTER
    }
}

#[derive(Default)]
pub struct HelloFS {
    host_prefix: String,
    handles: HashMap<i64, HandleState>,
}

impl FileSystem for HelloFS {
    fn name(&self) -> &str {
        "hellofs-wasm"
    }

    fn readme(&self) -> &str {
        "HelloFS WASM - Demonstrates host filesystem access\n\
         - /hello.txt - Returns 'Hello World'\n\
         - /host/* - Proxies to host filesystem (if configured)"
    }

    fn initialize(&mut self, config: &Config) -> Result<()> {
        // Get optional host_prefix from config
        if let Some(prefix) = config.get_str("host_prefix") {
            self.host_prefix = prefix.to_string();
        }
        Ok(())
    }

    fn read(&self, path: &str, offset: i64, size: i64) -> Result<Vec<u8>> {
        match path {
            "/hello.txt" => Ok(b"Hello World\n".to_vec()),
            p if p.starts_with("/host/") && !self.host_prefix.is_empty() => {
                // Proxy to host filesystem
                let host_path = p.strip_prefix("/host").unwrap();
                let full_path = format!("{}{}", self.host_prefix, host_path);
                HostFS::read(&full_path, offset, size)
                    .map_err(|e| Error::Other(format!("host fs: {}", e)))
            }
            _ => Err(Error::NotFound),
        }
    }

    fn stat(&self, path: &str) -> Result<FileInfo> {
        match path {
            "/" => Ok(FileInfo::dir("", 0o755)),
            "/hello.txt" => Ok(FileInfo::file("hello.txt", 12, 0o644)),
            "/host" if !self.host_prefix.is_empty() => {
                Ok(FileInfo::dir("host", 0o755))
            }
            p if p.starts_with("/host/") && !self.host_prefix.is_empty() => {
                // Proxy to host filesystem
                let host_path = p.strip_prefix("/host").unwrap();
                let full_path = format!("{}{}", self.host_prefix, host_path);
                let host_info = HostFS::stat(&full_path)
                    .map_err(|e| Error::Other(format!("host fs: {}", e)))?;

                // Convert and return
                Ok(FileInfo {
                    name: host_info.name,
                    size: host_info.size,
                    mode: host_info.mode,
                    mod_time: host_info.mod_time,
                    is_dir: host_info.is_dir,
                    meta: host_info.meta,
                })
            }
            _ => Err(Error::NotFound),
        }
    }

    fn readdir(&self, path: &str) -> Result<Vec<FileInfo>> {
        match path {
            "/" => {
                let mut entries = vec![FileInfo::file("hello.txt", 12, 0o644)];
                if !self.host_prefix.is_empty() {
                    entries.push(FileInfo::dir("host", 0o755));
                }
                Ok(entries)
            }
            "/host" if !self.host_prefix.is_empty() => {
                // Read from host filesystem root
                let host_infos = HostFS::readdir(&self.host_prefix)
                    .map_err(|e| Error::Other(format!("host fs: {}", e)))?;

                Ok(host_infos
                    .into_iter()
                    .map(|info| FileInfo {
                        name: info.name,
                        size: info.size,
                        mode: info.mode,
                        mod_time: info.mod_time,
                        is_dir: info.is_dir,
                        meta: info.meta,
                    })
                    .collect())
            }
            p if p.starts_with("/host/") && !self.host_prefix.is_empty() => {
                // Proxy to host filesystem
                let host_path = p.strip_prefix("/host").unwrap();
                let full_path = format!("{}{}", self.host_prefix, host_path);
                let host_infos = HostFS::readdir(&full_path)
                    .map_err(|e| Error::Other(format!("host fs: {}", e)))?;

                Ok(host_infos
                    .into_iter()
                    .map(|info| FileInfo {
                        name: info.name,
                        size: info.size,
                        mode: info.mode,
                        mod_time: info.mod_time,
                        is_dir: info.is_dir,
                        meta: info.meta,
                    })
                    .collect())
            }
            _ => Err(Error::NotFound),
        }
    }

    fn write(&mut self, path: &str, data: &[u8], _offset: i64, _flags: WriteFlag) -> Result<i64> {
        if path.starts_with("/host/") && !self.host_prefix.is_empty() {
            // Proxy to host filesystem
            // Note: HostFS doesn't support offset/flags yet, ignoring them
            let host_path = path.strip_prefix("/host").unwrap();
            let full_path = format!("{}{}", self.host_prefix, host_path);
            HostFS::write(&full_path, data)
                .map_err(|e| Error::Other(format!("host fs: {}", e)))?;
            Ok(data.len() as i64)
        } else {
            Err(Error::PermissionDenied)
        }
    }

    fn create(&mut self, path: &str) -> Result<()> {
        if path.starts_with("/host/") && !self.host_prefix.is_empty() {
            // Proxy to host filesystem
            let host_path = path.strip_prefix("/host").unwrap();
            let full_path = format!("{}{}", self.host_prefix, host_path);
            HostFS::create(&full_path)
                .map_err(|e| Error::Other(format!("host fs: {}", e)))
        } else {
            Err(Error::PermissionDenied)
        }
    }

    fn mkdir(&mut self, path: &str, perm: u32) -> Result<()> {
        if path.starts_with("/host/") && !self.host_prefix.is_empty() {
            // Proxy to host filesystem
            let host_path = path.strip_prefix("/host").unwrap();
            let full_path = format!("{}{}", self.host_prefix, host_path);
            HostFS::mkdir(&full_path, perm)
                .map_err(|e| Error::Other(format!("host fs: {}", e)))
        } else {
            Err(Error::PermissionDenied)
        }
    }

    fn remove(&mut self, path: &str) -> Result<()> {
        if path.starts_with("/host/") && !self.host_prefix.is_empty() {
            // Proxy to host filesystem
            let host_path = path.strip_prefix("/host").unwrap();
            let full_path = format!("{}{}", self.host_prefix, host_path);
            HostFS::remove(&full_path)
                .map_err(|e| Error::Other(format!("host fs: {}", e)))
        } else {
            Err(Error::PermissionDenied)
        }
    }

    fn remove_all(&mut self, path: &str) -> Result<()> {
        if path.starts_with("/host/") && !self.host_prefix.is_empty() {
            // Proxy to host filesystem
            let host_path = path.strip_prefix("/host").unwrap();
            let full_path = format!("{}{}", self.host_prefix, host_path);
            HostFS::remove_all(&full_path)
                .map_err(|e| Error::Other(format!("host fs: {}", e)))
        } else {
            Err(Error::PermissionDenied)
        }
    }

    fn rename(&mut self, old_path: &str, new_path: &str) -> Result<()> {
        if old_path.starts_with("/host/") && new_path.starts_with("/host/") && !self.host_prefix.is_empty() {
            // Proxy to host filesystem (both paths must be in host)
            let host_old_path = old_path.strip_prefix("/host").unwrap();
            let host_new_path = new_path.strip_prefix("/host").unwrap();
            let full_old_path = format!("{}{}", self.host_prefix, host_old_path);
            let full_new_path = format!("{}{}", self.host_prefix, host_new_path);
            HostFS::rename(&full_old_path, &full_new_path)
                .map_err(|e| Error::Other(format!("host fs: {}", e)))
        } else {
            Err(Error::PermissionDenied)
        }
    }

    fn chmod(&mut self, _path: &str, _mode: u32) -> Result<()> {
        Ok(())
    }
}

impl HandleFS for HelloFS {
    fn open_handle(&mut self, path: &str, flags: OpenFlag, _mode: u32) -> Result<i64> {
        // Check if file exists (unless O_CREATE is set)
        let exists = self.stat(path).is_ok();

        if !exists && !flags.contains(OpenFlag::O_CREATE) {
            return Err(Error::NotFound);
        }

        // Handle O_EXCL - should fail if file exists
        if exists && flags.contains(OpenFlag::O_EXCL) && flags.contains(OpenFlag::O_CREATE) {
            return Err(Error::AlreadyExists);
        }

        // Determine content and host_path
        let (content, host_path) = match path {
            "/hello.txt" => {
                // Built-in file - load content
                (Some(b"Hello World\n".to_vec()), None)
            }
            p if p.starts_with("/host/") && !self.host_prefix.is_empty() => {
                // Host file
                let hp = p.strip_prefix("/host").unwrap();
                let full_path = format!("{}{}", self.host_prefix, hp);
                (None, Some(full_path))
            }
            _ => return Err(Error::NotFound),
        };

        let id = generate_handle_id();
        let state = HandleState {
            path: path.to_string(),
            flags,
            pos: 0,
            content,
            host_path,
        };

        self.handles.insert(id, state);
        Ok(id)
    }

    fn handle_read(&mut self, id: i64, buf: &mut [u8]) -> Result<usize> {
        let state = self.handles.get_mut(&id).ok_or(Error::NotFound)?;

        if !state.flags.is_readable() {
            return Err(Error::PermissionDenied);
        }

        let pos = state.pos;
        let n = self.handle_read_at_internal(id, buf, pos)?;

        // Update position
        if let Some(state) = self.handles.get_mut(&id) {
            state.pos += n as i64;
        }

        Ok(n)
    }

    fn handle_read_at(&self, id: i64, buf: &mut [u8], offset: i64) -> Result<usize> {
        let state = self.handles.get(&id).ok_or(Error::NotFound)?;

        if !state.flags.is_readable() {
            return Err(Error::PermissionDenied);
        }

        // For local content (hello.txt)
        if let Some(ref content) = state.content {
            if offset < 0 || offset as usize >= content.len() {
                return Ok(0);
            }
            let start = offset as usize;
            let end = (start + buf.len()).min(content.len());
            let n = end - start;
            buf[..n].copy_from_slice(&content[start..end]);
            return Ok(n);
        }

        // For host files
        if let Some(ref host_path) = state.host_path {
            let data = HostFS::read(host_path, offset, buf.len() as i64)
                .map_err(|e| Error::Other(format!("host fs: {}", e)))?;
            let n = data.len().min(buf.len());
            buf[..n].copy_from_slice(&data[..n]);
            return Ok(n);
        }

        Ok(0)
    }

    fn handle_write(&mut self, id: i64, data: &[u8]) -> Result<usize> {
        let state = self.handles.get_mut(&id).ok_or(Error::NotFound)?;

        if !state.flags.is_writable() {
            return Err(Error::PermissionDenied);
        }

        // Handle append mode
        let pos = if state.flags.contains(OpenFlag::O_APPEND) {
            if let Some(ref content) = state.content {
                content.len() as i64
            } else if let Some(ref host_path) = state.host_path {
                let info = HostFS::stat(host_path)
                    .map_err(|e| Error::Other(format!("host fs: {}", e)))?;
                info.size
            } else {
                state.pos
            }
        } else {
            state.pos
        };

        let n = self.handle_write_at_internal(id, data, pos)?;

        // Update position
        if let Some(state) = self.handles.get_mut(&id) {
            state.pos = pos + n as i64;
        }

        Ok(n)
    }

    fn handle_write_at(&self, id: i64, data: &[u8], _offset: i64) -> Result<usize> {
        let state = self.handles.get(&id).ok_or(Error::NotFound)?;

        if !state.flags.is_writable() {
            return Err(Error::PermissionDenied);
        }

        // /hello.txt is read-only
        if state.content.is_some() {
            return Err(Error::PermissionDenied);
        }

        // For host files
        if let Some(ref host_path) = state.host_path {
            // Note: Host FS write doesn't support offset well
            HostFS::write(host_path, data)
                .map_err(|e| Error::Other(format!("host fs: {}", e)))?;
            return Ok(data.len());
        }

        Err(Error::PermissionDenied)
    }

    fn handle_seek(&mut self, id: i64, offset: i64, whence: i32) -> Result<i64> {
        let state = self.handles.get_mut(&id).ok_or(Error::NotFound)?;

        let size = if let Some(ref content) = state.content {
            content.len() as i64
        } else if let Some(ref host_path) = state.host_path {
            let info = HostFS::stat(host_path)
                .map_err(|e| Error::Other(format!("host fs: {}", e)))?;
            info.size
        } else {
            0
        };

        let new_pos = match whence {
            0 => offset,                    // SEEK_SET
            1 => state.pos + offset,        // SEEK_CUR
            2 => size + offset,             // SEEK_END
            _ => return Err(Error::InvalidInput("invalid whence".to_string())),
        };

        if new_pos < 0 {
            return Err(Error::InvalidInput("negative position".to_string()));
        }

        state.pos = new_pos;
        Ok(state.pos)
    }

    fn handle_sync(&self, id: i64) -> Result<()> {
        let _ = self.handles.get(&id).ok_or(Error::NotFound)?;
        Ok(())
    }

    fn handle_stat(&self, id: i64) -> Result<FileInfo> {
        let state = self.handles.get(&id).ok_or(Error::NotFound)?;

        if let Some(ref content) = state.content {
            return Ok(FileInfo::file("hello.txt", content.len() as i64, 0o644));
        }

        if let Some(ref host_path) = state.host_path {
            let info = HostFS::stat(host_path)
                .map_err(|e| Error::Other(format!("host fs: {}", e)))?;
            return Ok(info);
        }

        Err(Error::NotFound)
    }

    fn handle_info(&self, id: i64) -> Result<(String, OpenFlag)> {
        let state = self.handles.get(&id).ok_or(Error::NotFound)?;
        Ok((state.path.clone(), state.flags))
    }

    fn close_handle(&mut self, id: i64) -> Result<()> {
        self.handles.remove(&id).ok_or(Error::NotFound)?;
        Ok(())
    }
}

// Helper methods for internal use
impl HelloFS {
    fn handle_read_at_internal(&self, id: i64, buf: &mut [u8], offset: i64) -> Result<usize> {
        self.handle_read_at(id, buf, offset)
    }

    fn handle_write_at_internal(&self, id: i64, data: &[u8], offset: i64) -> Result<usize> {
        self.handle_write_at(id, data, offset)
    }
}

// Export with HandleFS support
export_handle_plugin!(HelloFS);
