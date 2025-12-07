"""AGFS Server API Client"""

import requests
import time
from typing import List, Dict, Any, Optional, Union, Iterator, BinaryIO
from requests.exceptions import ConnectionError, Timeout, RequestException

from .exceptions import AGFSClientError


class AGFSClient:
    """Client for interacting with AGFS (Plugin-based File System) Server API"""

    def __init__(self, api_base_url="http://localhost:8080", timeout=10):
        """
        Initialize AGFS client.

        Args:
            api_base_url: API base URL. Can be either full URL with "/api/v1" or just the base.
                         If "/api/v1" is not present, it will be automatically appended.
                         e.g., "http://localhost:8080" or "http://localhost:8080/api/v1"
            timeout: Request timeout in seconds (default: 10)
        """
        api_base_url = api_base_url.rstrip("/")
        # Auto-append /api/v1 if not present
        if not api_base_url.endswith("/api/v1"):
            api_base_url = api_base_url + "/api/v1"
        self.api_base = api_base_url
        self.session = requests.Session()
        self.timeout = timeout

    def _handle_request_error(self, e: Exception, operation: str = "request") -> None:
        """Convert request exceptions to user-friendly error messages"""
        if isinstance(e, ConnectionError):
            # Extract host and port from the error message
            url_parts = self.api_base.split("://")
            if len(url_parts) > 1:
                host_port = url_parts[1].split("/")[0]
            else:
                host_port = "server"
            raise AGFSClientError(f"Connection refused - server not running at {host_port}")
        elif isinstance(e, Timeout):
            raise AGFSClientError(f"Request timeout after {self.timeout}s")
        elif isinstance(e, requests.exceptions.HTTPError):
            # Extract useful error information from response
            if hasattr(e, 'response') and e.response is not None:
                status_code = e.response.status_code
                # Try to get error message from JSON response first (priority)
                try:
                    error_data = e.response.json()
                    error_msg = error_data.get("error", "")
                    if error_msg:
                        # Use the server's detailed error message
                        raise AGFSClientError(error_msg)
                except (ValueError, KeyError, TypeError):
                    # If JSON parsing fails, fall through to generic status code messages
                    pass
                except AGFSClientError:
                    # Re-raise our own error
                    raise

                # Fallback to generic messages based on status codes
                if status_code == 404:
                    raise AGFSClientError("No such file or directory")
                elif status_code == 403:
                    raise AGFSClientError("Permission denied")
                elif status_code == 409:
                    raise AGFSClientError("Resource already exists")
                elif status_code == 500:
                    raise AGFSClientError("Internal server error")
                elif status_code == 502:
                    raise AGFSClientError("Bad Gateway - backend service unavailable")
                else:
                    raise AGFSClientError(f"HTTP error {status_code}")
            else:
                raise AGFSClientError("HTTP error")
        else:
            # For other exceptions, re-raise with simplified message
            raise AGFSClientError(str(e))

    def health(self) -> Dict[str, Any]:
        """Check server health"""
        response = self.session.get(f"{self.api_base}/health", timeout=self.timeout)
        response.raise_for_status()
        return response.json()

    def ls(self, path: str = "/") -> List[Dict[str, Any]]:
        """List directory contents"""
        try:
            response = self.session.get(
                f"{self.api_base}/directories",
                params={"path": path},
                timeout=self.timeout
            )
            response.raise_for_status()
            data = response.json()
            files = data.get("files")
            return files if files is not None else []
        except Exception as e:
            self._handle_request_error(e)

    def read(self, path: str, offset: int = 0, size: int = -1, stream: bool = False):
        return self.cat(path, offset, size, stream)

    def cat(self, path: str, offset: int = 0, size: int = -1, stream: bool = False):
        """Read file content with optional offset and size

        Args:
            path: File path
            offset: Starting position (default: 0)
            size: Number of bytes to read (default: -1, read all)
            stream: Enable streaming mode for continuous reads (default: False)

        Returns:
            If stream=False: bytes content
            If stream=True: Response object for iteration
        """
        try:
            params = {"path": path}

            if stream:
                params["stream"] = "true"
                # Streaming mode - return response object for iteration
                response = self.session.get(
                    f"{self.api_base}/files",
                    params=params,
                    stream=True,
                    timeout=None  # No timeout for streaming
                )
                response.raise_for_status()
                return response
            else:
                # Normal mode - return content
                if offset > 0:
                    params["offset"] = str(offset)
                if size >= 0:
                    params["size"] = str(size)

                response = self.session.get(
                    f"{self.api_base}/files",
                    params=params,
                    timeout=self.timeout
                )
                response.raise_for_status()
                return response.content
        except Exception as e:
            self._handle_request_error(e)

    def write(self, path: str, data: Union[bytes, Iterator[bytes], BinaryIO], max_retries: int = 3) -> str:
        """Write data to file and return the response message

        Args:
            path: Path to write the file
            data: File content as bytes, iterator of bytes, or file-like object
            max_retries: Maximum number of retry attempts (default: 3)

        Returns:
            Response message from server
        """
        # Calculate timeout based on file size (if known)
        # For streaming data, use a larger default timeout
        if isinstance(data, bytes):
            data_size_mb = len(data) / (1024 * 1024)
            write_timeout = max(10, min(300, int(data_size_mb * 1 + 10)))
        else:
            # For streaming/unknown size, use no timeout
            write_timeout = None

        last_error = None

        for attempt in range(max_retries + 1):
            try:
                response = self.session.put(
                    f"{self.api_base}/files",
                    params={"path": path},
                    data=data,  # requests supports bytes, iterator, or file-like object
                    timeout=write_timeout
                )
                response.raise_for_status()
                result = response.json()

                # If we succeeded after retrying, let user know
                if attempt > 0:
                    print(f"✓ Upload succeeded after {attempt} retry(ies)")

                return result.get("message", "OK")

            except (ConnectionError, Timeout) as e:
                # Network errors and timeouts are retryable
                last_error = e

                if attempt < max_retries:
                    # Exponential backoff: 1s, 2s, 4s
                    wait_time = 2 ** attempt
                    print(f"⚠ Upload failed (attempt {attempt + 1}/{max_retries + 1}): {type(e).__name__}")
                    print(f"  Retrying in {wait_time} seconds...")
                    time.sleep(wait_time)
                else:
                    # Last attempt failed
                    print(f"✗ Upload failed after {max_retries + 1} attempts")
                    self._handle_request_error(e)

            except requests.exceptions.HTTPError as e:
                # Check if it's a server error (5xx) which might be retryable
                if hasattr(e, 'response') and e.response is not None:
                    status_code = e.response.status_code

                    # Only retry specific server errors that indicate temporary issues
                    # 502 Bad Gateway, 503 Service Unavailable, 504 Gateway Timeout
                    # Do NOT retry 500 Internal Server Error (usually indicates business logic errors)
                    retryable_5xx = [502, 503, 504]

                    if status_code in retryable_5xx:
                        last_error = e

                        if attempt < max_retries:
                            wait_time = 2 ** attempt
                            print(f"⚠ Server error {status_code} (attempt {attempt + 1}/{max_retries + 1})")
                            print(f"  Retrying in {wait_time} seconds...")
                            time.sleep(wait_time)
                        else:
                            print(f"✗ Upload failed after {max_retries + 1} attempts")
                            self._handle_request_error(e)
                    else:
                        # 500 and other errors (including 4xx) are not retryable
                        # They usually indicate business logic errors or client mistakes
                        self._handle_request_error(e)
                else:
                    self._handle_request_error(e)

            except Exception as e:
                # Other exceptions are not retryable
                self._handle_request_error(e)

        # Should not reach here, but just in case
        if last_error:
            self._handle_request_error(last_error)

    def create(self, path: str) -> Dict[str, Any]:
        """Create a new file"""
        try:
            response = self.session.post(
                f"{self.api_base}/files",
                params={"path": path},
                timeout=self.timeout
            )
            response.raise_for_status()
            return response.json()
        except Exception as e:
            self._handle_request_error(e)

    def mkdir(self, path: str, mode: str = "755") -> Dict[str, Any]:
        """Create a directory"""
        try:
            response = self.session.post(
                f"{self.api_base}/directories",
                params={"path": path, "mode": mode},
                timeout=self.timeout
            )
            response.raise_for_status()
            return response.json()
        except Exception as e:
            self._handle_request_error(e)

    def rm(self, path: str, recursive: bool = False) -> Dict[str, Any]:
        """Remove a file or directory"""
        try:
            params = {"path": path}
            if recursive:
                params["recursive"] = "true"
            response = self.session.delete(
                f"{self.api_base}/files",
                params=params,
                timeout=self.timeout
            )
            response.raise_for_status()
            return response.json()
        except Exception as e:
            self._handle_request_error(e)

    def stat(self, path: str) -> Dict[str, Any]:
        """Get file/directory information"""
        try:
            response = self.session.get(
                f"{self.api_base}/stat",
                params={"path": path},
                timeout=self.timeout
            )
            response.raise_for_status()
            return response.json()
        except Exception as e:
            self._handle_request_error(e)

    def mv(self, old_path: str, new_path: str) -> Dict[str, Any]:
        """Rename/move a file or directory"""
        try:
            response = self.session.post(
                f"{self.api_base}/rename",
                params={"path": old_path},
                json={"newPath": new_path},
                timeout=self.timeout
            )
            response.raise_for_status()
            return response.json()
        except Exception as e:
            self._handle_request_error(e)

    def chmod(self, path: str, mode: int) -> Dict[str, Any]:
        """Change file permissions"""
        try:
            response = self.session.post(
                f"{self.api_base}/chmod",
                params={"path": path},
                json={"mode": mode},
                timeout=self.timeout
            )
            response.raise_for_status()
            return response.json()
        except Exception as e:
            self._handle_request_error(e)

    def touch(self, path: str) -> Dict[str, Any]:
        """Touch a file (update timestamp by writing empty content)"""
        try:
            response = self.session.post(
                f"{self.api_base}/touch",
                params={"path": path},
                timeout=self.timeout
            )
            response.raise_for_status()
            return response.json()
        except Exception as e:
            self._handle_request_error(e)

    def mounts(self) -> List[Dict[str, Any]]:
        """List all mounted plugins"""
        try:
            response = self.session.get(f"{self.api_base}/mounts", timeout=self.timeout)
            response.raise_for_status()
            data = response.json()
            return data.get("mounts", [])
        except Exception as e:
            self._handle_request_error(e)

    def mount(self, fstype: str, path: str, config: Dict[str, Any]) -> Dict[str, Any]:
        """Mount a plugin dynamically

        Args:
            fstype: Filesystem type (e.g., 'sqlfs', 's3fs', 'memfs')
            path: Mount path
            config: Plugin configuration as dictionary

        Returns:
            Response with message
        """
        try:
            response = self.session.post(
                f"{self.api_base}/mount",
                json={"fstype": fstype, "path": path, "config": config},
                timeout=self.timeout
            )
            response.raise_for_status()
            return response.json()
        except Exception as e:
            self._handle_request_error(e)

    def unmount(self, path: str) -> Dict[str, Any]:
        """Unmount a plugin"""
        try:
            response = self.session.post(
                f"{self.api_base}/unmount",
                json={"path": path},
                timeout=self.timeout
            )
            response.raise_for_status()
            return response.json()
        except Exception as e:
            self._handle_request_error(e)

    def load_plugin(self, library_path: str) -> Dict[str, Any]:
        """Load an external plugin from a shared library or HTTP(S) URL

        Args:
            library_path: Path to the shared library (.so/.dylib/.dll) or HTTP(S) URL

        Returns:
            Response with message and plugin name
        """
        try:
            response = self.session.post(
                f"{self.api_base}/plugins/load",
                json={"library_path": library_path},
                timeout=self.timeout
            )
            response.raise_for_status()
            return response.json()
        except Exception as e:
            self._handle_request_error(e)

    def unload_plugin(self, library_path: str) -> Dict[str, Any]:
        """Unload an external plugin

        Args:
            library_path: Path to the shared library

        Returns:
            Response with message
        """
        try:
            response = self.session.post(
                f"{self.api_base}/plugins/unload",
                json={"library_path": library_path},
                timeout=self.timeout
            )
            response.raise_for_status()
            return response.json()
        except Exception as e:
            self._handle_request_error(e)

    def list_plugins(self) -> List[str]:
        """List all loaded external plugins

        Returns:
            List of plugin library paths
        """
        try:
            response = self.session.get(
                f"{self.api_base}/plugins",
                timeout=self.timeout
            )
            response.raise_for_status()
            data = response.json()

            # Support both old and new API formats
            if "loaded_plugins" in data:
                # Old format
                return data.get("loaded_plugins", [])
            elif "plugins" in data:
                # New format - extract library paths from external plugins only
                plugins = data.get("plugins", [])
                return [p.get("library_path", "") for p in plugins
                       if p.get("is_external", False) and p.get("library_path")]
            else:
                return []
        except Exception as e:
            self._handle_request_error(e)

    def get_plugins_info(self) -> List[dict]:
        """Get detailed information about all loaded plugins

        Returns:
            List of plugin info dictionaries with keys:
            - name: Plugin name
            - library_path: Path to plugin library (for external plugins)
            - is_external: Whether this is an external plugin
            - mounted_paths: List of mount point information
            - config_params: List of configuration parameters (name, type, required, default, description)
        """
        try:
            response = self.session.get(
                f"{self.api_base}/plugins",
                timeout=self.timeout
            )
            response.raise_for_status()
            data = response.json()
            return data.get("plugins", [])
        except Exception as e:
            self._handle_request_error(e)

    def grep(self, path: str, pattern: str, recursive: bool = False, case_insensitive: bool = False, stream: bool = False):
        """Search for a pattern in files using regular expressions

        Args:
            path: Path to file or directory to search
            pattern: Regular expression pattern to search for
            recursive: Whether to search recursively in directories (default: False)
            case_insensitive: Whether to perform case-insensitive matching (default: False)
            stream: Whether to stream results as NDJSON (default: False)

        Returns:
            If stream=False: Dict with 'matches' (list of match objects) and 'count'
            If stream=True: Iterator yielding match dicts and a final summary dict

        Example (non-stream):
            >>> result = client.grep("/local/test-grep", "error", recursive=True)
            >>> print(result['count'])
            2

        Example (stream):
            >>> for item in client.grep("/local/test-grep", "error", recursive=True, stream=True):
            ...     if item.get('type') == 'summary':
            ...         print(f"Total: {item['count']}")
            ...     else:
            ...         print(f"{item['file']}:{item['line']}: {item['content']}")
        """
        try:
            response = self.session.post(
                f"{self.api_base}/grep",
                json={
                    "path": path,
                    "pattern": pattern,
                    "recursive": recursive,
                    "case_insensitive": case_insensitive,
                    "stream": stream
                },
                timeout=None if stream else self.timeout,
                stream=stream
            )
            response.raise_for_status()

            if stream:
                # Return iterator for streaming results
                return self._parse_ndjson_stream(response)
            else:
                # Return complete result
                return response.json()
        except Exception as e:
            self._handle_request_error(e)

    def _parse_ndjson_stream(self, response):
        """Parse NDJSON streaming response line by line"""
        import json
        for line in response.iter_lines():
            if line:
                try:
                    yield json.loads(line)
                except json.JSONDecodeError as e:
                    # Skip malformed lines
                    continue

    def digest(self, path: str, algorithm: str = "xxh3") -> Dict[str, Any]:
        """Calculate the digest of a file using specified algorithm

        Args:
            path: Path to the file
            algorithm: Hash algorithm to use - "xxh3" or "md5" (default: "xxh3")

        Returns:
            Dict with 'algorithm', 'path', and 'digest' keys

        Example:
            >>> result = client.digest("/local/file.txt", "xxh3")
            >>> print(result['digest'])
            abc123def456...

            >>> result = client.digest("/local/file.txt", "md5")
            >>> print(result['digest'])
            5d41402abc4b2a76b9719d911017c592
        """
        try:
            response = self.session.post(
                f"{self.api_base}/digest",
                json={"algorithm": algorithm, "path": path},
                timeout=self.timeout
            )
            response.raise_for_status()
            return response.json()
        except Exception as e:
            self._handle_request_error(e)
