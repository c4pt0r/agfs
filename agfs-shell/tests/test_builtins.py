import unittest
import tempfile
import os
from unittest.mock import Mock, MagicMock
from agfs_shell.builtins import BUILTINS
from agfs_shell.process import Process
from agfs_shell.streams import InputStream, OutputStream, ErrorStream

class TestBuiltins(unittest.TestCase):
    def create_process(self, command, args, input_data=""):
        stdin = InputStream.from_string(input_data)
        stdout = OutputStream.to_buffer()
        stderr = ErrorStream.to_buffer()
        return Process(command, args, stdin, stdout, stderr)

    def test_echo(self):
        cmd = BUILTINS['echo']
        
        # Test basic echo
        proc = self.create_process("echo", ["hello", "world"])
        self.assertEqual(cmd(proc), 0)
        self.assertEqual(proc.get_stdout(), b"hello world\n")

        # Test empty echo
        proc = self.create_process("echo", [])
        self.assertEqual(cmd(proc), 0)
        self.assertEqual(proc.get_stdout(), b"\n")

    def test_cat_stdin(self):
        cmd = BUILTINS['cat']
        input_data = "line1\nline2\n"
        proc = self.create_process("cat", [], input_data)
        self.assertEqual(cmd(proc), 0)
        self.assertEqual(proc.get_stdout(), input_data.encode('utf-8'))

    def test_cat_file(self):
        cmd = BUILTINS['cat']
        with tempfile.TemporaryDirectory() as tmpdir:
            filename = os.path.join(tmpdir, "test.txt")
            with open(filename, "w") as f:
                f.write("file content")
            
            proc = self.create_process("cat", [filename])
            self.assertEqual(cmd(proc), 0)
            self.assertEqual(proc.get_stdout(), b"file content")

    def test_grep(self):
        cmd = BUILTINS['grep']
        input_data = "apple\nbanana\ncherry\n"
        
        # Match found
        proc = self.create_process("grep", ["pp"], input_data)
        self.assertEqual(cmd(proc), 0)
        self.assertEqual(proc.get_stdout(), b"apple\n")

        # No match
        proc = self.create_process("grep", ["xyz"], input_data)
        self.assertEqual(cmd(proc), 1)
        self.assertEqual(proc.get_stdout(), b"")

        # Missing pattern
        proc = self.create_process("grep", [], input_data)
        self.assertEqual(cmd(proc), 2)
        self.assertIn(b"missing pattern", proc.get_stderr())

    def test_wc(self):
        cmd = BUILTINS['wc']
        input_data = "one two\nthree\n"
        # 2 lines, 3 words, 14 bytes
        
        # Default (all)
        proc = self.create_process("wc", [], input_data)
        self.assertEqual(cmd(proc), 0)
        self.assertEqual(proc.get_stdout(), b"2 3 14\n")

        # Lines only
        proc = self.create_process("wc", ["-l"], input_data)
        self.assertEqual(cmd(proc), 0)
        self.assertEqual(proc.get_stdout(), b"2\n")

    def test_head(self):
        cmd = BUILTINS['head']
        input_data = "\n".join([f"line{i}" for i in range(20)]) + "\n"
        
        # Default 10 lines
        proc = self.create_process("head", [], input_data)
        self.assertEqual(cmd(proc), 0)
        output = proc.get_stdout().decode('utf-8').splitlines()
        self.assertEqual(len(output), 10)
        self.assertEqual(output[0], "line0")
        self.assertEqual(output[-1], "line9")

        # Custom lines
        proc = self.create_process("head", ["-n", "5"], input_data)
        self.assertEqual(cmd(proc), 0)
        output = proc.get_stdout().decode('utf-8').splitlines()
        self.assertEqual(len(output), 5)

    def test_tail(self):
        cmd = BUILTINS['tail']
        input_data = "\n".join([f"line{i}" for i in range(20)]) + "\n"
        
        # Default 10 lines
        proc = self.create_process("tail", [], input_data)
        self.assertEqual(cmd(proc), 0)
        output = proc.get_stdout().decode('utf-8').splitlines()
        self.assertEqual(len(output), 10)
        self.assertEqual(output[0], "line10")
        self.assertEqual(output[-1], "line19")

    def test_sort(self):
        cmd = BUILTINS['sort']
        input_data = "c\na\nb\n"
        
        # Normal sort
        proc = self.create_process("sort", [], input_data)
        self.assertEqual(cmd(proc), 0)
        self.assertEqual(proc.get_stdout(), b"a\nb\nc\n")

        # Reverse sort
        proc = self.create_process("sort", ["-r"], input_data)
        self.assertEqual(cmd(proc), 0)
        self.assertEqual(proc.get_stdout(), b"c\nb\na\n")

    def test_uniq(self):
        cmd = BUILTINS['uniq']
        input_data = "a\na\nb\nb\nc\n"
        
        proc = self.create_process("uniq", [], input_data)
        self.assertEqual(cmd(proc), 0)
        self.assertEqual(proc.get_stdout(), b"a\nb\nc\n")

    def test_tr(self):
        cmd = BUILTINS['tr']
        input_data = "hello"
        
        # Translate
        proc = self.create_process("tr", ["el", "ip"], input_data)
        self.assertEqual(cmd(proc), 0)
        self.assertEqual(proc.get_stdout(), b"hippo")

        # Error cases
        proc = self.create_process("tr", ["a"], input_data)
        self.assertEqual(cmd(proc), 1)
        self.assertIn(b"missing operand", proc.get_stderr())

    def test_ls_multiple_files(self):
        """Test ls command with multiple file arguments (like from glob expansion)"""
        cmd = BUILTINS['ls']

        # Create a mock filesystem
        mock_fs = Mock()

        # Mock get_file_info to return file info for each path
        def mock_get_file_info(path):
            # Simulate file metadata
            if path.endswith('.txt'):
                return {
                    'name': os.path.basename(path),
                    'isDir': False,
                    'size': 100,
                    'modTime': '2025-11-23T12:00:00Z',
                    'mode': 'rw-r--r--'
                }
            else:
                raise Exception(f"No such file: {path}")

        mock_fs.get_file_info = mock_get_file_info

        # Test with multiple file paths (simulating glob expansion like 'ls *.txt')
        proc = self.create_process("ls", [
            "/test/file1.txt",
            "/test/file2.txt",
            "/test/file3.txt"
        ])
        proc.filesystem = mock_fs

        exit_code = cmd(proc)
        self.assertEqual(exit_code, 0)

        # Check output contains all files
        output = proc.get_stdout().decode('utf-8')
        self.assertIn('file1.txt', output)
        self.assertIn('file2.txt', output)
        self.assertIn('file3.txt', output)

        # Verify each file listed once
        self.assertEqual(output.count('file1.txt'), 1)
        self.assertEqual(output.count('file2.txt'), 1)
        self.assertEqual(output.count('file3.txt'), 1)

    def test_ls_mixed_files_and_dirs(self):
        """Test ls command with mix of files and directories"""
        cmd = BUILTINS['ls']

        # Create a mock filesystem
        mock_fs = Mock()

        # Mock get_file_info to return file/dir info
        def mock_get_file_info(path):
            if path == "/test/dir1":
                return {
                    'name': 'dir1',
                    'isDir': True,
                    'size': 0,
                    'modTime': '2025-11-23T12:00:00Z'
                }
            elif path.endswith('.txt'):
                return {
                    'name': os.path.basename(path),
                    'isDir': False,
                    'size': 100,
                    'modTime': '2025-11-23T12:00:00Z'
                }
            else:
                raise Exception(f"No such file: {path}")

        # Mock list_directory for the directory
        def mock_list_directory(path):
            if path == "/test/dir1":
                return [
                    {'name': 'subfile1.txt', 'isDir': False, 'size': 50},
                    {'name': 'subfile2.txt', 'isDir': False, 'size': 60}
                ]
            else:
                raise Exception(f"Not a directory: {path}")

        mock_fs.get_file_info = mock_get_file_info
        mock_fs.list_directory = mock_list_directory

        # Test with mix of file and directory
        proc = self.create_process("ls", [
            "/test/file1.txt",
            "/test/dir1"
        ])
        proc.filesystem = mock_fs

        exit_code = cmd(proc)
        self.assertEqual(exit_code, 0)

        # Check output
        output = proc.get_stdout().decode('utf-8')
        # File should be listed
        self.assertIn('file1.txt', output)
        # Directory contents should be listed
        self.assertIn('subfile1.txt', output)
        self.assertIn('subfile2.txt', output)

if __name__ == '__main__':
    unittest.main()
