import unittest
from agfs_shell.parser import CommandParser

class TestCommandParser(unittest.TestCase):
    def test_parse_pipeline_simple(self):
        cmd = "ls -l"
        expected = [("ls", ["-l"])]
        self.assertEqual(CommandParser.parse_pipeline(cmd), expected)

    def test_parse_pipeline_multiple(self):
        cmd = "cat file.txt | grep pattern | wc -l"
        expected = [
            ("cat", ["file.txt"]),
            ("grep", ["pattern"]),
            ("wc", ["-l"])
        ]
        self.assertEqual(CommandParser.parse_pipeline(cmd), expected)

    def test_parse_pipeline_quoted(self):
        cmd = 'echo "hello world" | grep "world"'
        expected = [
            ("echo", ["hello world"]),
            ("grep", ["world"])
        ]
        self.assertEqual(CommandParser.parse_pipeline(cmd), expected)

    def test_parse_pipeline_empty(self):
        self.assertEqual(CommandParser.parse_pipeline(""), [])
        self.assertEqual(CommandParser.parse_pipeline("   "), [])

    def test_parse_redirection_stdin(self):
        cmd = "cat < input.txt"
        cleaned, redirs = CommandParser.parse_redirection(cmd)
        self.assertEqual(cleaned, "cat")
        self.assertEqual(redirs["stdin"], "input.txt")

    def test_parse_redirection_stdout(self):
        cmd = "ls > output.txt"
        cleaned, redirs = CommandParser.parse_redirection(cmd)
        self.assertEqual(cleaned, "ls")
        self.assertEqual(redirs["stdout"], "output.txt")
        self.assertEqual(redirs["stdout_mode"], "write")

    def test_parse_redirection_append(self):
        cmd = "echo hello >> log.txt"
        cleaned, redirs = CommandParser.parse_redirection(cmd)
        self.assertEqual(cleaned, "echo hello")
        self.assertEqual(redirs["stdout"], "log.txt")
        self.assertEqual(redirs["stdout_mode"], "append")

    def test_parse_redirection_stderr(self):
        cmd = "cmd 2> error.log"
        cleaned, redirs = CommandParser.parse_redirection(cmd)
        self.assertEqual(cleaned, "cmd")
        self.assertEqual(redirs["stderr"], "error.log")
        self.assertEqual(redirs["stderr_mode"], "write")

    def test_quote_arg(self):
        self.assertEqual(CommandParser.quote_arg("simple"), "simple")
        self.assertEqual(CommandParser.quote_arg("hello world"), "'hello world'")
        self.assertEqual(CommandParser.quote_arg("foo|bar"), "'foo|bar'")

    def test_unquote_arg(self):
        self.assertEqual(CommandParser.unquote_arg("'hello'"), "hello")
        self.assertEqual(CommandParser.unquote_arg('"world"'), "world")
        self.assertEqual(CommandParser.unquote_arg("simple"), "simple")

    def test_parse_filenames_with_spaces(self):
        """Test parsing filenames with spaces using quotes"""
        # Double quotes
        cmd = 'rm "Ed Huang - 2024 US filing authorization forms.PDF"'
        commands, _ = CommandParser.parse_command_line(cmd)
        self.assertEqual(commands, [('rm', ['Ed Huang - 2024 US filing authorization forms.PDF'])])

        # Single quotes
        cmd = "rm 'Ed Huang - 2024 US filing authorization forms.PDF'"
        commands, _ = CommandParser.parse_command_line(cmd)
        self.assertEqual(commands, [('rm', ['Ed Huang - 2024 US filing authorization forms.PDF'])])

        # Multiple files with spaces
        cmd = 'rm "file 1.txt" "file 2.txt" normal.txt'
        commands, _ = CommandParser.parse_command_line(cmd)
        self.assertEqual(commands, [('rm', ['file 1.txt', 'file 2.txt', 'normal.txt'])])

        # ls with filename containing spaces
        cmd = 'ls -l "2. 【清洁版】INSTRUMENT OF TRANSFER.doc"'
        commands, _ = CommandParser.parse_command_line(cmd)
        self.assertEqual(commands, [('ls', ['-l', '2. 【清洁版】INSTRUMENT OF TRANSFER.doc'])])

if __name__ == '__main__':
    unittest.main()
