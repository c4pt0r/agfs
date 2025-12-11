//! Macros for exporting WASM plugin functions

/// Export a FileSystem implementation as a WASM plugin
#[macro_export]
macro_rules! export_plugin {
    ($plugin_type:ty) => {
        static mut PLUGIN: Option<$plugin_type> = None;

        // Force type checking
        const _: fn() = || {
            fn assert_impl<T: $crate::FileSystem + Default>() {}
            assert_impl::<$plugin_type>();
        };

        #[no_mangle]
        pub extern "C" fn plugin_new() -> usize {
            unsafe {
                PLUGIN = Some(<$plugin_type>::default());
            }
            1
        }

        #[no_mangle]
        pub extern "C" fn plugin_name() -> *mut u8 {
            use $crate::memory::CString;
            use $crate::FileSystem;
            unsafe {
                let p = PLUGIN.as_ref().expect("Not initialized");
                CString::new(<$plugin_type as $crate::FileSystem>::name(p)).into_raw()
            }
        }

        #[no_mangle]
        pub extern "C" fn plugin_get_readme() -> *mut u8 {
            use $crate::memory::CString;
            use $crate::FileSystem;
            unsafe {
                let p = PLUGIN.as_ref().expect("Not initialized");
                CString::new(<$plugin_type as $crate::FileSystem>::readme(p)).into_raw()
            }
        }

        #[no_mangle]
        pub extern "C" fn plugin_get_config_params() -> *mut u8 {
            use $crate::memory::CString;
            use $crate::FileSystem;
            unsafe {
                let p = PLUGIN.as_ref().expect("Not initialized");
                let params = <$plugin_type as $crate::FileSystem>::config_params(p);
                // Serialize to JSON using crate's re-exported serde_json
                match $crate::serde_json::to_string(&params) {
                    Ok(json) => CString::new(&json).into_raw(),
                    Err(_) => CString::new("[]").into_raw(),
                }
            }
        }

        #[no_mangle]
        pub extern "C" fn plugin_validate(config_ptr: *const u8) -> *mut u8 {
            use $crate::ffi::{read_config, result_to_error_ptr};
            use $crate::FileSystem;
            let config = match read_config(config_ptr) {
                Ok(c) => c,
                Err(e) => return result_to_error_ptr::<()>(Err(e)),
            };
            unsafe {
                let p = PLUGIN.as_ref().expect("Not initialized");
                result_to_error_ptr::<()>(<$plugin_type as $crate::FileSystem>::validate(p, &config))
            }
        }

        #[no_mangle]
        pub extern "C" fn plugin_initialize(config_ptr: *const u8) -> *mut u8 {
            use $crate::ffi::{read_config, result_to_error_ptr};
            use $crate::FileSystem;
            let config = match read_config(config_ptr) {
                Ok(c) => c,
                Err(e) => return result_to_error_ptr::<()>(Err(e)),
            };
            unsafe {
                let p = PLUGIN.as_mut().expect("Not initialized");
                result_to_error_ptr::<()>(<$plugin_type as $crate::FileSystem>::initialize(p, &config))
            }
        }

        #[no_mangle]
        pub extern "C" fn plugin_shutdown() -> *mut u8 {
            use $crate::ffi::result_to_error_ptr;
            use $crate::FileSystem;
            unsafe {
                let p = PLUGIN.as_mut().expect("Not initialized");
                result_to_error_ptr::<()>(<$plugin_type as $crate::FileSystem>::shutdown(p))
            }
        }

        #[no_mangle]
        pub extern "C" fn fs_read(path_ptr: *const u8, offset: i64, size: i64) -> u64 {
            use $crate::memory::{CString, Buffer, pack_u64};
            use $crate::FileSystem;

            let path = unsafe { CString::from_ptr(path_ptr) };

            unsafe {
                let p = PLUGIN.as_ref().expect("Not initialized");
                match <$plugin_type as $crate::FileSystem>::read(p, &path, offset, size) {
                    Ok(data) => {
                        let len = data.len() as u32;
                        let buffer = Buffer::from_bytes(&data);
                        let ptr = buffer.into_raw() as u32;
                        pack_u64(ptr, len)
                    }
                    Err(_) => 0,
                }
            }
        }

        #[no_mangle]
        pub extern "C" fn fs_stat(path_ptr: *const u8) -> u64 {
            use $crate::memory::{CString, pack_u64};
            use $crate::ffi::fileinfo_to_json_ptr;
            use $crate::FileSystem;

            let path = unsafe { CString::from_ptr(path_ptr) };

            unsafe {
                let p = PLUGIN.as_ref().expect("Not initialized");
                match <$plugin_type as $crate::FileSystem>::stat(p, &path) {
                    Ok(info) => match fileinfo_to_json_ptr(&info) {
                        Ok(json_ptr) => pack_u64(json_ptr as u32, 0),
                        Err(e) => {
                            let err_ptr = CString::new(&e.to_string()).into_raw();
                            pack_u64(0, err_ptr as u32)
                        }
                    },
                    Err(e) => {
                        let err_ptr = CString::new(&e.to_string()).into_raw();
                        pack_u64(0, err_ptr as u32)
                    }
                }
            }
        }

        #[no_mangle]
        pub extern "C" fn fs_readdir(path_ptr: *const u8) -> u64 {
            use $crate::memory::{CString, pack_u64};
            use $crate::ffi::fileinfo_vec_to_json_ptr;
            use $crate::FileSystem;

            let path = unsafe { CString::from_ptr(path_ptr) };

            unsafe {
                let p = PLUGIN.as_ref().expect("Not initialized");
                match <$plugin_type as $crate::FileSystem>::readdir(p, &path) {
                    Ok(infos) => match fileinfo_vec_to_json_ptr(&infos) {
                        Ok(json_ptr) => pack_u64(json_ptr as u32, 0),
                        Err(e) => {
                            let err_ptr = CString::new(&e.to_string()).into_raw();
                            pack_u64(0, err_ptr as u32)
                        }
                    },
                    Err(e) => {
                        let err_ptr = CString::new(&e.to_string()).into_raw();
                        pack_u64(0, err_ptr as u32)
                    }
                }
            }
        }

        /// Write to file with offset and flags
        /// Returns packed u64: high 32 bits = bytes written, low 32 bits = error ptr (0 = success)
        #[no_mangle]
        pub extern "C" fn fs_write(path_ptr: *const u8, data_ptr: *const u8, size: usize, offset: i64, flags: u32) -> u64 {
            use $crate::memory::{CString, pack_u64};
            use $crate::FileSystem;
            use $crate::WriteFlag;

            let path = unsafe { CString::from_ptr(path_ptr) };
            let data = unsafe { std::slice::from_raw_parts(data_ptr, size) };

            unsafe {
                let p = PLUGIN.as_mut().expect("Not initialized");
                match <$plugin_type as $crate::FileSystem>::write(p, &path, data, offset, WriteFlag::from(flags)) {
                    Ok(bytes_written) => {
                        // Pack bytes_written in high 32 bits, 0 (success) in low 32 bits
                        pack_u64(bytes_written as u32, 0)
                    }
                    Err(e) => {
                        let err_ptr = CString::new(&e.to_string()).into_raw();
                        pack_u64(0, err_ptr as u32)
                    }
                }
            }
        }

        #[no_mangle]
        pub extern "C" fn fs_create(path_ptr: *const u8) -> *mut u8 {
            use $crate::memory::CString;
            use $crate::ffi::result_to_error_ptr;
            use $crate::FileSystem;

            let path = unsafe { CString::from_ptr(path_ptr) };

            unsafe {
                let p = PLUGIN.as_mut().expect("Not initialized");
                result_to_error_ptr::<()>(<$plugin_type as $crate::FileSystem>::create(p, &path))
            }
        }

        #[no_mangle]
        pub extern "C" fn fs_mkdir(path_ptr: *const u8, perm: u32) -> *mut u8 {
            use $crate::memory::CString;
            use $crate::ffi::result_to_error_ptr;
            use $crate::FileSystem;

            let path = unsafe { CString::from_ptr(path_ptr) };

            unsafe {
                let p = PLUGIN.as_mut().expect("Not initialized");
                result_to_error_ptr::<()>(<$plugin_type as $crate::FileSystem>::mkdir(p, &path, perm))
            }
        }

        #[no_mangle]
        pub extern "C" fn fs_remove(path_ptr: *const u8) -> *mut u8 {
            use $crate::memory::CString;
            use $crate::ffi::result_to_error_ptr;
            use $crate::FileSystem;

            let path = unsafe { CString::from_ptr(path_ptr) };

            unsafe {
                let p = PLUGIN.as_mut().expect("Not initialized");
                result_to_error_ptr::<()>(<$plugin_type as $crate::FileSystem>::remove(p, &path))
            }
        }

        #[no_mangle]
        pub extern "C" fn fs_remove_all(path_ptr: *const u8) -> *mut u8 {
            use $crate::memory::CString;
            use $crate::ffi::result_to_error_ptr;
            use $crate::FileSystem;

            let path = unsafe { CString::from_ptr(path_ptr) };

            unsafe {
                let p = PLUGIN.as_mut().expect("Not initialized");
                result_to_error_ptr::<()>(<$plugin_type as $crate::FileSystem>::remove_all(p, &path))
            }
        }

        #[no_mangle]
        pub extern "C" fn fs_rename(old_path_ptr: *const u8, new_path_ptr: *const u8) -> *mut u8 {
            use $crate::memory::CString;
            use $crate::ffi::result_to_error_ptr;
            use $crate::FileSystem;

            let old_path = unsafe { CString::from_ptr(old_path_ptr) };
            let new_path = unsafe { CString::from_ptr(new_path_ptr) };

            unsafe {
                let p = PLUGIN.as_mut().expect("Not initialized");
                result_to_error_ptr::<()>(<$plugin_type as $crate::FileSystem>::rename(p, &old_path, &new_path))
            }
        }

        #[no_mangle]
        pub extern "C" fn fs_chmod(path_ptr: *const u8, mode: u32) -> *mut u8 {
            use $crate::memory::CString;
            use $crate::ffi::result_to_error_ptr;
            use $crate::FileSystem;

            let path = unsafe { CString::from_ptr(path_ptr) };

            unsafe {
                let p = PLUGIN.as_mut().expect("Not initialized");
                result_to_error_ptr::<()>(<$plugin_type as $crate::FileSystem>::chmod(p, &path, mode))
            }
        }

        // Shared memory buffers for zero-copy optimization
        // Each buffer is 64KB by default
        const SHARED_BUFFER_SIZE: usize = 65536;
        static mut INPUT_BUFFER: [u8; SHARED_BUFFER_SIZE] = [0; SHARED_BUFFER_SIZE];
        static mut OUTPUT_BUFFER: [u8; SHARED_BUFFER_SIZE] = [0; SHARED_BUFFER_SIZE];

        /// Get pointer to input buffer (Go -> WASM)
        #[no_mangle]
        pub extern "C" fn get_input_buffer_ptr() -> *mut u8 {
            unsafe { INPUT_BUFFER.as_mut_ptr() }
        }

        /// Get pointer to output buffer (WASM -> Go)
        #[no_mangle]
        pub extern "C" fn get_output_buffer_ptr() -> *mut u8 {
            unsafe { OUTPUT_BUFFER.as_mut_ptr() }
        }

        /// Get shared buffer size
        #[no_mangle]
        pub extern "C" fn get_shared_buffer_size() -> u32 {
            SHARED_BUFFER_SIZE as u32
        }

        // Export malloc and free for Go compatibility (fallback for large data)
        #[no_mangle]
        pub extern "C" fn malloc(size: usize) -> *mut u8 {
            use std::alloc::{alloc, Layout};

            if size == 0 {
                return std::ptr::null_mut();
            }

            unsafe {
                let layout = Layout::from_size_align(size, 1).unwrap();
                alloc(layout)
            }
        }

        #[no_mangle]
        pub extern "C" fn free(ptr: *mut u8, size: usize) {
            use std::alloc::{dealloc, Layout};

            if ptr.is_null() || size == 0 {
                return;
            }

            unsafe {
                let layout = Layout::from_size_align(size, 1).unwrap();
                dealloc(ptr, layout);
            }
        }
    };
}

/// Export a HandleFS implementation as a WASM plugin with handle support
/// This macro exports all FileSystem functions plus HandleFS handle operations
#[macro_export]
macro_rules! export_handle_plugin {
    ($plugin_type:ty) => {
        // First export all the basic FileSystem functions
        $crate::export_plugin!($plugin_type);

        // Then add HandleFS-specific exports

        /// Open a file handle
        /// Returns: On success, handle_id as i64 (cast to u64). On error, high 32 bits = error ptr, low 32 bits = 0
        #[no_mangle]
        pub extern "C" fn handle_open(path_ptr: *const u8, flags: u32, mode: u32) -> u64 {
            use $crate::memory::{CString, pack_u64};
            use $crate::HandleFS;

            let path = unsafe { CString::from_ptr(path_ptr) };

            unsafe {
                let p = PLUGIN.as_mut().expect("Not initialized");
                match <$plugin_type as $crate::HandleFS>::open_handle(p, &path, $crate::OpenFlag::from(flags), mode) {
                    Ok(id) => {
                        // Return handle ID as i64 (cast to u64)
                        id as u64
                    }
                    Err(e) => {
                        // Error: high 32 bits = error ptr, low 32 bits = 0
                        let err_ptr = CString::new(&e.to_string()).into_raw();
                        pack_u64(0, err_ptr as u32)
                    }
                }
            }
        }

        /// Read from handle
        /// Returns packed u64: high 32 bits = bytes read, low 32 bits = error ptr (0 = success)
        #[no_mangle]
        pub extern "C" fn handle_read(id: i64, buf_ptr: *mut u8, buf_size: usize) -> u64 {
            use $crate::memory::{CString, pack_u64};
            use $crate::HandleFS;

            let buf = unsafe { std::slice::from_raw_parts_mut(buf_ptr, buf_size) };

            unsafe {
                let p = PLUGIN.as_mut().expect("Not initialized");
                match <$plugin_type as $crate::HandleFS>::handle_read(p, id, buf) {
                    Ok(n) => pack_u64(n as u32, 0),
                    Err(e) => {
                        let err_ptr = CString::new(&e.to_string()).into_raw();
                        pack_u64(0, err_ptr as u32)
                    }
                }
            }
        }

        /// Read from handle at offset (pread)
        /// Returns packed u64: high 32 bits = bytes read, low 32 bits = error ptr (0 = success)
        #[no_mangle]
        pub extern "C" fn handle_read_at(id: i64, buf_ptr: *mut u8, buf_size: usize, offset: i64) -> u64 {
            use $crate::memory::{CString, pack_u64};
            use $crate::HandleFS;

            let buf = unsafe { std::slice::from_raw_parts_mut(buf_ptr, buf_size) };

            unsafe {
                let p = PLUGIN.as_ref().expect("Not initialized");
                match <$plugin_type as $crate::HandleFS>::handle_read_at(p, id, buf, offset) {
                    Ok(n) => pack_u64(n as u32, 0),
                    Err(e) => {
                        let err_ptr = CString::new(&e.to_string()).into_raw();
                        pack_u64(0, err_ptr as u32)
                    }
                }
            }
        }

        /// Write to handle
        /// Returns packed u64: high 32 bits = bytes written, low 32 bits = error ptr (0 = success)
        #[no_mangle]
        pub extern "C" fn handle_write(id: i64, data_ptr: *const u8, data_size: usize) -> u64 {
            use $crate::memory::{CString, pack_u64};
            use $crate::HandleFS;
            let data = unsafe { std::slice::from_raw_parts(data_ptr, data_size) };

            unsafe {
                let p = PLUGIN.as_mut().expect("Not initialized");
                match <$plugin_type as $crate::HandleFS>::handle_write(p, id, data) {
                    Ok(n) => pack_u64(n as u32, 0),
                    Err(e) => {
                        let err_ptr = CString::new(&e.to_string()).into_raw();
                        pack_u64(0, err_ptr as u32)
                    }
                }
            }
        }

        /// Write to handle at offset (pwrite)
        /// Returns packed u64: high 32 bits = bytes written, low 32 bits = error ptr (0 = success)
        #[no_mangle]
        pub extern "C" fn handle_write_at(id: i64, data_ptr: *const u8, data_size: usize, offset: i64) -> u64 {
            use $crate::memory::{CString, pack_u64};
            use $crate::HandleFS;
            let data = unsafe { std::slice::from_raw_parts(data_ptr, data_size) };

            unsafe {
                let p = PLUGIN.as_ref().expect("Not initialized");
                match <$plugin_type as $crate::HandleFS>::handle_write_at(p, id, data, offset) {
                    Ok(n) => pack_u64(n as u32, 0),
                    Err(e) => {
                        let err_ptr = CString::new(&e.to_string()).into_raw();
                        pack_u64(0, err_ptr as u32)
                    }
                }
            }
        }

        /// Seek handle position
        /// Returns packed u64: high 32 bits = new position (truncated), low 32 bits = error ptr (0 = success)
        /// For full 64-bit position, use handle_seek64
        #[no_mangle]
        pub extern "C" fn handle_seek(id: i64, offset: i64, whence: i32) -> u64 {
            use $crate::memory::{CString, pack_u64};
            use $crate::HandleFS;

            unsafe {
                let p = PLUGIN.as_mut().expect("Not initialized");
                match <$plugin_type as $crate::HandleFS>::handle_seek(p, id, offset, whence) {
                    Ok(pos) => pack_u64(pos as u32, 0),
                    Err(e) => {
                        let err_ptr = CString::new(&e.to_string()).into_raw();
                        pack_u64(0, err_ptr as u32)
                    }
                }
            }
        }

        /// Sync handle data
        /// Returns error pointer (0 = success)
        #[no_mangle]
        pub extern "C" fn handle_sync(id: i64) -> *mut u8 {
            use $crate::memory::CString;
            use $crate::ffi::result_to_error_ptr;
            use $crate::HandleFS;

            unsafe {
                let p = PLUGIN.as_ref().expect("Not initialized");
                result_to_error_ptr::<()>(<$plugin_type as $crate::HandleFS>::handle_sync(p, id))
            }
        }

        /// Stat via handle
        /// Returns packed u64: high 32 bits = json pointer, low 32 bits = error ptr
        #[no_mangle]
        pub extern "C" fn handle_stat(id: i64) -> u64 {
            use $crate::memory::{CString, pack_u64};
            use $crate::ffi::fileinfo_to_json_ptr;
            use $crate::HandleFS;

            unsafe {
                let p = PLUGIN.as_ref().expect("Not initialized");
                match <$plugin_type as $crate::HandleFS>::handle_stat(p, id) {
                    Ok(info) => match fileinfo_to_json_ptr(&info) {
                        Ok(json_ptr) => pack_u64(json_ptr as u32, 0),
                        Err(e) => {
                            let err_ptr = CString::new(&e.to_string()).into_raw();
                            pack_u64(0, err_ptr as u32)
                        }
                    },
                    Err(e) => {
                        let err_ptr = CString::new(&e.to_string()).into_raw();
                        pack_u64(0, err_ptr as u32)
                    }
                }
            }
        }

        /// Get handle info (path, flags)
        /// Returns packed u64: high 32 bits = json pointer, low 32 bits = error ptr
        #[no_mangle]
        pub extern "C" fn handle_info(id: i64) -> u64 {
            use $crate::memory::{CString, pack_u64};
            use $crate::HandleFS;

            unsafe {
                let p = PLUGIN.as_ref().expect("Not initialized");
                match <$plugin_type as $crate::HandleFS>::handle_info(p, id) {
                    Ok((path, flags)) => {
                        // Return JSON with path and flags
                        let json = $crate::serde_json::json!({
                            "path": path,
                            "flags": flags.0
                        });
                        let json_str = json.to_string();
                        let json_ptr = CString::new(&json_str).into_raw();
                        pack_u64(json_ptr as u32, 0)
                    }
                    Err(e) => {
                        let err_ptr = CString::new(&e.to_string()).into_raw();
                        pack_u64(0, err_ptr as u32)
                    }
                }
            }
        }

        /// Close handle
        /// Returns error pointer (0 = success)
        #[no_mangle]
        pub extern "C" fn handle_close(id: i64) -> *mut u8 {
            use $crate::memory::CString;
            use $crate::ffi::result_to_error_ptr;
            use $crate::HandleFS;

            unsafe {
                let p = PLUGIN.as_mut().expect("Not initialized");
                result_to_error_ptr::<()>(<$plugin_type as $crate::HandleFS>::close_handle(p, id))
            }
        }
    };
}
