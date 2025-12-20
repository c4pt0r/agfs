"""AGFS File System abstraction layer"""

from typing import BinaryIO, Iterator, Optional, Union

from pyagfs import AGFSClient, AGFSClientError


class AGFSFileSystem:
    """Abstraction layer for AGFS file system operations"""

    def __init__(self, server_url: str = "http://localhost:8080", timeout: int = 30):
        """
        Initialize AGFS file system

        Args:
            server_url: AGFS server URL (default: http://localhost:8080)
            timeout: Request timeout in seconds (default: 30)
                    - Increased from 5 to 30 for better support of large file transfers
                    - Each 8KB chunk upload/download should complete within this time
        """
        self.server_url = server_url
        self.client = AGFSClient(server_url, timeout=timeout)
        self._connected = False

    def check_connection(self) -> bool:
        """Check if AGFS server is accessible"""
        if self._connected:
            return True

        try:
            self.client.health()
            self._connected = True
            return True
        except Exception:
            # Catch all exceptions (ConnectionError, AGFSClientError, etc.)
            return False

    def read_file(
        self, path: str, offset: int = 0, size: int = -1, stream: bool = False
    ) -> Union[bytes, Iterator[bytes]]:
        """
        Read file content from AGFS

        Args:
            path: File path in AGFS
            offset: Starting byte offset (default: 0)
            size: Number of bytes to read, -1 for all (default: -1)
            stream: If True, return iterator for streaming; if False, return all content

        Returns:
            If stream=False: File content as bytes
            If stream=True: Iterator yielding chunks of bytes

        Raises:
            AGFSClientError: If file cannot be read
        """
        try:
            if stream:
                # Try streaming mode on server side first
                try:
                    response = self.client.cat(
                        path, offset=offset, size=size, stream=True
                    )
                    return response.iter_content(chunk_size=8192)
                except AGFSClientError as e:
                    # Fallback to regular read and simulate streaming
                    content = self.client.cat(
                        path, offset=offset, size=size, stream=False
                    )

                    # Return iterator that yields chunks
                    def chunk_generator(data, chunk_size=8192):
                        for i in range(0, len(data), chunk_size):
                            yield data[i : i + chunk_size]

                    return chunk_generator(content)
            else:
                # Return all content at once
                return self.client.cat(path, offset=offset, size=size)
        except AGFSClientError as e:
            # SDK error already includes path, don't duplicate it
            raise AGFSClientError(str(e))

    def write_file(
        self,
        path: str,
        data: Union[bytes, Iterator[bytes], BinaryIO],
        append: bool = False,
    ) -> Optional[str]:
        """
        Write data to file in AGFS

        Args:
            path: File path in AGFS
            data: Data to write (bytes, iterator of bytes, or file-like object)
            append: If True, append to file; if False, overwrite

        Returns:
            Response message from server (if any)

        Raises:
            AGFSClientError: If file cannot be written
        """
        try:
            if append:
                # For append mode, we need to read existing content first
                # This means we can't stream directly, need to collect all data
                try:
                    existing = self.client.cat(path)
                except AGFSClientError:
                    # File doesn't exist, just write new data
                    existing = b""

                # Collect data if it's streaming
                if hasattr(data, "__iter__") and not isinstance(
                    data, (bytes, bytearray)
                ):
                    chunks = [existing]
                    for chunk in data:
                        chunks.append(chunk)
                    data = b"".join(chunks)
                elif hasattr(data, "read"):
                    # File-like object
                    data = existing + data.read()
                else:
                    data = existing + data

            # Write to AGFS - SDK now supports streaming data directly
            # Use max_retries=0 for shell operations (fail fast)
            response = self.client.write(path, data, max_retries=0)
            return response
        except AGFSClientError as e:
            # SDK error already includes path, don't duplicate it
            raise AGFSClientError(str(e))

    def file_exists(self, path: str) -> bool:
        """
        Check if file exists in AGFS

        Args:
            path: File path in AGFS

        Returns:
            True if file exists, False otherwise
        """
        try:
            self.client.stat(path)
            return True
        except AGFSClientError:
            return False

    def is_directory(self, path: str) -> bool:
        """
        Check if path is a directory

        Args:
            path: Path in AGFS

        Returns:
            True if path is a directory, False otherwise
        """
        try:
            info = self.client.stat(path)
            # Check if it's a directory based on mode or isDir field
            return info.get("isDir", False)
        except AGFSClientError:
            return False

    def list_directory(self, path: str):
        """
        List directory contents

        Args:
            path: Directory path in AGFS

        Returns:
            List of file info dicts

        Raises:
            AGFSClientError: If directory cannot be listed
        """
        try:
            return self.client.ls(path)
        except AGFSClientError as e:
            # SDK error already includes path, don't duplicate it
            raise AGFSClientError(str(e))

    def get_file_info(self, path: str):
        """
        Get file/directory information

        Args:
            path: File or directory path in AGFS

        Returns:
            Dict containing file information (name, size, mode, modTime, isDir, etc.)

        Raises:
            AGFSClientError: If file/directory does not exist
        """
        try:
            return self.client.stat(path)
        except AGFSClientError as e:
            # SDK error already includes path, don't duplicate it
            raise AGFSClientError(str(e))

    def touch_file(self, path: str) -> None:
        """
        Touch a file (update timestamp by writing empty content)

        Args:
            path: File path in AGFS

        Raises:
            AGFSClientError: If file cannot be touched
        """
        try:
            self.client.touch(path)
        except AGFSClientError as e:
            # SDK error already includes path, don't duplicate it
            raise AGFSClientError(str(e))

    def symlink(self, target: str, link_path: str) -> None:
        """
        Create a symbolic link

        Args:
            target: Target path (what the symlink points to)
            link_path: Path where the symlink will be created

        Raises:
            AGFSClientError: If symlink cannot be created
        """
        try:
            self.client.symlink(target, link_path)
        except AGFSClientError as e:
            raise AGFSClientError(str(e))

    def readlink(self, link_path: str) -> str:
        """
        Read the target of a symbolic link

        Args:
            link_path: Path to the symbolic link

        Returns:
            Target path that the symlink points to

        Raises:
            AGFSClientError: If symlink cannot be read
        """
        try:
            return self.client.readlink(link_path)
        except AGFSClientError as e:
            raise AGFSClientError(str(e))

    def grep(self, path: str, pattern: str, recursive: bool = False,
             case_insensitive: bool = False, stream: bool = False, limit: int = 0):
        """
        Search for pattern in files using server-side grep (supports custom implementations like VectorFS)

        Args:
            path: Path to file or directory to search
            pattern: Pattern to search for (regex for text grep, semantic query for VectorFS)
            recursive: Whether to search recursively in directories
            case_insensitive: Whether to perform case-insensitive matching (for text grep)
            stream: Whether to stream results
            limit: Maximum number of results to return (0 means default, for vector search defaults to 10)

        Returns:
            Dict with 'matches' (list of match objects) and 'count'
            Each match has: {'file': str, 'line': int, 'content': str, 'metadata': dict (optional)}

        Raises:
            AGFSClientError: If grep operation fails
        """
        try:
            return self.client.grep(path, pattern, recursive, case_insensitive, stream, limit)
        except AGFSClientError as e:
            raise AGFSClientError(str(e))

    def get_error_message(self, error: Exception) -> str:
        """
        Get user-friendly error message

        Args:
            error: Exception object

        Returns:
            Formatted error message
        """
        if isinstance(error, AGFSClientError):
            msg = str(error)
            if "Connection refused" in msg:
                return f"AGFS server not running at {self.server_url}"
            return msg
        return str(error)
