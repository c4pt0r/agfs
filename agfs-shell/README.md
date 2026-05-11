# agfs-shell

An experimental shell implementation with Unix-style pipeline support and **AGFS integration**, written in pure Python.
agfs-shell provides a lightweight interpreter with bash-like syntax, enabling script development.

## Table of Contents

- [Overview](#overview)
- [Features](#features)
- [Prerequisites](#prerequisites)
- [Installation](#installation)
- [Quick Start](#quick-start)
- [Shell Syntax Reference](#shell-syntax-reference)
  - [Comments](#comments)
  - [Pipelines](#pipelines)
  - [Redirection](#redirection)
  - [Variables](#variables)
  - [Arithmetic Expansion](#arithmetic-expansion)
  - [Command Substitution](#command-substitution)
  - [Glob Patterns](#glob-patterns)
  - [Control Flow](#control-flow)
  - [Background Jobs](#background-jobs)
  - [Functions](#functions)
  - [Heredoc](#heredoc)
- [Built-in Commands](#built-in-commands)
  - [File System Commands](#file-system-commands)
  - [Text Processing](#text-processing)
  - [Environment Variables](#environment-variables)
  - [Conditional Testing](#conditional-testing)
  - [Control Flow Commands](#control-flow-commands)
  - [Job Control Commands](#job-control-commands)
  - [AGFS Management](#agfs-management)
    - [chroot](#chroot-path----exit)
    - [plugins](#plugins)
    - [mount](#mount-plugin-path-options)
  - [Utility Commands](#utility-commands)
  - [Network Commands](#network-commands)
  - [AI Integration](#ai-integration)
- [Script Files](#script-files)
- [Interactive Features](#interactive-features)
- [Startup Scripts (initrc)](#startup-scripts-initrc)
- [Complex Examples](#complex-examples)
- [Architecture](#architecture)
- [Testing](#testing)

## Overview

agfs-shell is a lightweight, educational shell that demonstrates Unix pipeline concepts while integrating with the AGFS (Aggregated File System) server. All file operations go through AGFS, allowing you to work with multiple backend filesystems (local, S3, SQL, etc.) through a unified interface.

**Key Features:**
- Unix-style pipelines and redirection
- **Background job control** with `command &` syntax
- Full scripting support with control flow
- User-defined functions with local variables and recursion support
- AGFS integration for distributed file operations
- Tab completion and command history
- AI-powered command (llm integration)
- HTTP client with persistent state management
- Command aliasing for custom shortcuts
- VectorFS semantic search (fsgrep)
- Chroot support for directory isolation
- Pure Python implementation (no subprocess for builtins)

**Note:** This is an educational shell implementation with full scripting capabilities including recursive functions and nested command substitution.

## Features

### Core Shell Features
- **Pipelines**: Chain commands with `|` operator
- **Background Jobs**: Run commands in background with `&` operator
- **I/O Redirection**: `<`, `>`, `>>`, `2>`, `2>>`
- **Heredoc**: Multi-line input with `<<` (supports variable expansion)
- **Variables**: Assignment, expansion, special variables (`$?`, `$1`, `$@`, etc.)
- **Arithmetic**: `$((expression))` for calculations
- **Command Substitution**: `$(command)` or backticks
- **Glob Expansion**: `*.txt`, `file?.dat`, `[abc]`
- **Control Flow**: `if/then/elif/else/fi` and `for/in/do/done`
- **Functions**: User-defined functions with parameters, local variables, return values, and recursion
- **Comments**: `#` and `//` style comments

### Built-in Commands (50+)
- **File Operations**: cd, pwd, ls, tree, cat, mkdir, touch, rm, truncate, mv, stat, cp, ln, upload, download
- **Text Processing**: echo, grep, fsgrep, jq, wc, head, tail, tee, sort, uniq, tr, rev, cut
- **Path Utilities**: basename, dirname
- **Variables**: export, env, unset, local
- **Testing**: test, [ ]
- **Control Flow**: break, continue, exit, return, true, false, source, .
- **Job Control**: jobs, wait
- **Utilities**: sleep, date, plugins, mount, chroot, alias, unalias, help
- **Network**: http (HTTP client with persistent state)
- **AI**: llm (LLM integration)
- **Operators**: `&&` (AND), `||` (OR) for conditional command execution, `&` for background jobs

### Interactive Features
- **Tab Completion**: Commands and file paths (AGFS-aware)
- **Command History**: Persistent across sessions (`~/.agfs_shell_history`)
- **Multiline Editing**: Backslash continuation, quote matching
- **Rich Output**: Colorized formatting with Rich library
- **Dynamic Prompt**: Shows current directory

### AGFS Integration
- **Unified Interface**: Work with multiple filesystems through AGFS
- **File Transfer**: Upload/download between local and AGFS
- **Streaming I/O**: Memory-efficient processing (8KB chunks)
- **Cross-filesystem Operations**: Copy between different backends

## Prerequisites

**AGFS Server must be running!**

```bash
# Option 1: Use Docker (Recommended)
# Pull the image
docker pull c4pt0r/agfs:latest

# Run the server with port mapping
docker run -d -p 8080:8080 --name agfs-server c4pt0r/agfs:latest

# With data persistence (mount /data directory)
docker run -d -p 8080:8080 -v $(pwd)/data:/data --name agfs-server c4pt0r/agfs:latest

# With custom configuration
docker run -d -p 8080:8080 -v $(pwd)/config.yaml:/config.yaml --name agfs-server c4pt0r/agfs:latest

# Verify server is running
curl http://localhost:8080/api/v1/health

# Option 2: Run from source
cd agfs-server
make dev
```

For the full installer/source/Docker/webapp/FUSE/MCP startup matrix, see [../docs/first-run.md](../docs/first-run.md).

## Installation

```bash
cd agfs-shell
uv sync
```

## Quick Start

### Interactive Mode

```bash
uv run agfs-shell

agfs:/> echo "Hello, World!" > /local/tmp/hello.txt
agfs:/> cat /local/tmp/hello.txt
Hello, World!

agfs:/> ls /local/tmp | grep txt
hello.txt

agfs:/> for i in 1 2 3; do
>   echo "Count: $i"
> done
Count: 1
Count: 2
Count: 3

agfs:/> sleep 3 &
[1] 140234567890123
agfs:/> jobs
[1] Running      sleep 3
agfs:/> # After 3 seconds...
[1] Done         sleep 3
```

### Execute Command String

```bash
# Using -c flag
uv run agfs-shell -c "echo 'test' > /local/tmp/test.txt"

# With pipeline
uv run agfs-shell -c "cat /local/tmp/data.txt | sort | uniq > /local/tmp/sorted.txt"
```

### Execute Script File

Create a script file with `.as` extension:

```bash
cat > example.as << 'EOF'
#!/usr/bin/env uv run agfs-shell

# Count files in directory
count=0
for file in /local/tmp/*; do
    count=$((count + 1))
    echo "File $count: $file"
done

echo "Total files: $count"
EOF

chmod +x example.as
./example.as
```

## Shell Syntax Reference

### Comments

```bash
# This is a comment (recommended)
// This is also a comment (C-style, also supported)

echo "Hello"  # Inline comment
echo "World"  // Inline comment works too
```

### Pipelines

```bash
# Basic pipeline
command1 | command2 | command3

# Examples
cat /local/tmp/data.txt | grep "error" | wc -l
ls /local/tmp | sort | head -n 10
```

### Redirection

```bash
# Input redirection
command < input.txt

# Output redirection
command > output.txt        # Overwrite
command >> output.txt       # Append

# Error redirection
command 2> errors.log       # Redirect stderr
command 2>> errors.log      # Append stderr

# Combined
command < input.txt > output.txt 2> errors.log
```

### Variables

```bash
# Assignment
NAME="Alice"
COUNT=10
PATH=/local/data

# Expansion
echo $NAME              # Simple expansion
echo ${NAME}            # Braced expansion (preferred)
echo "Hello, $NAME!"    # In double quotes (expanded)
echo 'Hello, $NAME!'    # In single quotes (literal, no expansion)

# Special variables
echo $?                 # Exit code of last command
echo $0                 # Script name
echo $1 $2              # Script arguments
echo $#                 # Number of arguments
echo $@                 # All arguments

# Environment variables
export DATABASE_URL="postgres://localhost/mydb"
env | grep DATABASE
unset DATABASE_URL
```

**Quote Behavior:**
| Quote Type | Variable Expansion | Command Substitution | Glob Expansion |
|------------|-------------------|---------------------|----------------|
| `"..."` (double) | Yes | Yes | No |
| `'...'` (single) | No | No | No |
| No quotes | Yes | Yes | Yes |

### Arithmetic Expansion

```bash
# Basic arithmetic
result=$((5 + 3))
echo $result            # 8

# With variables
count=10
count=$((count + 1))
echo $count             # 11

# Complex expressions
x=5
y=3
result=$(( (x + y) * 2 ))
echo $result            # 16

# In loops
for i in 1 2 3 4 5; do
    doubled=$((i * 2))
    echo "$i * 2 = $doubled"
done
```

### Command Substitution

```bash
# Using $() syntax (recommended)
current_dir=$(pwd)
file_count=$(ls /local/tmp | wc -l)
today=$(date "+%Y-%m-%d")

# Using backticks (also works)
files=`ls /local/tmp`

# In double-quoted strings (expansion happens)
echo "Current time: $(date)"
echo "AGFS Shell initialized at $(date)"
echo "There are $(ls /local/tmp | wc -l) files in the directory"

# In single-quoted strings (NO expansion - literal text)
echo 'This is literal: $(date)'    # Output: This is literal: $(date)
echo 'Price: $100'                 # Output: Price: $100

# Nested command substitution
echo "Files: $(ls $(pwd))"
```

**Quote Behavior (Bash-compatible):**
- **Double quotes `"..."`**: Variables and command substitution ARE expanded
- **Single quotes `'...'`**: Everything is literal, NO expansion
- **No quotes**: Variables and command substitution ARE expanded

### Glob Patterns

```bash
# Wildcard matching
*.txt                   # All .txt files
file?.dat               # file followed by any single character
test[123].log           # test1.log, test2.log, or test3.log
file[a-z].txt           # file with single letter a-z

# Examples
cat /local/tmp/*.txt        # Concatenate all text files
rm /local/tmp/temp_*        # Remove all temp_ files
for file in /local/tmp/data_[0-9]*.json; do
    echo "Processing $file"
done
```

### Control Flow

**If Statements:**

```bash
# Basic if
if [ -f /local/tmp/file.txt ]; then
    echo "File exists"
fi

# If-else
if [ -d /local/tmp/mydir ]; then
    echo "Directory exists"
else
    echo "Directory not found"
fi

# If-elif-else
if [ "$STATUS" = "running" ]; then
    echo "Service is running"
elif [ "$STATUS" = "stopped" ]; then
    echo "Service is stopped"
else
    echo "Unknown status"
fi

# Single line
if [ -f file.txt ]; then cat file.txt; fi
```

**For Loops:**

```bash
# Basic loop
for i in 1 2 3 4 5; do
    echo "Number: $i"
done

# Loop over files
for file in /local/tmp/*.txt; do
    echo "Processing $file"
    cat $file | wc -l
done

# Loop with command substitution
for user in $(cat /local/tmp/users.txt); do
    echo "User: $user"
done

# Nested loops
for dir in /local/tmp/projects/*; do
    echo "Project: $(basename $dir)"
    for file in $dir/*.txt; do
        echo "  File: $(basename $file)"
    done
done
```

**Loop Control:**

```bash
# Break - exit loop early
for i in 1 2 3 4 5; do
    if [ $i -eq 3 ]; then
        break
    fi
    echo $i
done
# Output: 1, 2

# Continue - skip to next iteration
for i in 1 2 3 4 5; do
    if [ $i -eq 3 ]; then
        continue
    fi
    echo $i
done
# Output: 1, 2, 4, 5
```

**Conditional Execution:**

```bash
# && operator - execute second command only if first succeeds
test -f /local/tmp/file.txt && echo "File exists"

# || operator - execute second command only if first fails
test -f /local/tmp/missing.txt || echo "File not found"

# Combining && and ||
mkdir /local/tmp/data && echo "Created" || echo "Failed"

# Short-circuit evaluation
false && echo "Not executed"
true || echo "Not executed"

# Using true/false commands
if true; then
    echo "Always runs"
fi

if false; then
    echo "Never runs"
fi

# Practical example: fallback chain
command1 || command2 || command3 || echo "All failed"
```

### Background Jobs

Run commands in the background to continue working while they execute:

```bash
# Basic background job
sleep 10 &
# Returns immediately with job ID: [1] 140234567890123

# Multiple background jobs
sleep 5 & sleep 3 & sleep 1 &

# Background with output (output appears as job runs)
echo "Processing..." &

# Pipeline in background (entire pipeline runs in background)
cat /local/tmp/large.txt | grep pattern | sort &

# Check running jobs
jobs
# Output:
# [1] Running      sleep 10
# [2] Running      cat /local/tmp/large.txt | grep pattern | sort

# Wait for all background jobs to complete
wait

# Wait for specific job
sleep 30 &
jobs              # Shows: [1] Running sleep 30
wait 1            # Waits for job [1] to complete

# Combining with other operators
command1 && command2 &    # Run command2 in background if command1 succeeds
sleep 5 & echo "Started"  # Start background job, then print message
```

**Job Control Commands:**

| Command | Description |
|---------|-------------|
| `jobs` | List all background jobs with status |
| `jobs -l` | List jobs with thread IDs |
| `wait` | Wait for all background jobs to complete |
| `wait <job_id>` | Wait for specific job to complete |

**Job States:**

- **Running**: Job is currently executing
- **Done**: Job completed successfully
- **Failed**: Job completed with error (shows exit code)

**Examples:**

```bash
# Long-running task in background
cat /local/tmp/huge.log | grep ERROR | sort > /local/tmp/errors.txt &
jobs                    # [1] Running ...
# Continue working...
wait 1                  # Wait for analysis to complete

# Multiple parallel tasks
for i in 1 2 3 4 5; do
    sleep $i &          # Start each sleep in background
done
jobs                    # Shows all 5 jobs running
wait                    # Wait for all to complete

# Background job with notification
(sleep 3; echo "Task complete!") &
# After 3 seconds, you'll see: "Task complete!"
# Then: [1] Done (sleep 3; echo "Task complete!")
```

**Notes:**

- Background jobs output directly to terminal (output may interleave with prompt)
- Jobs terminate when shell exits (waits for completion or Ctrl+C to force quit)
- Background jobs receive empty stdin (cannot read from terminal)
- Job IDs are unique within a shell session
- See `BACKGROUND_JOBS.md` for detailed documentation

### Functions

**Function Definition:**

```bash
# Syntax 1: function_name() { ... }
greet() {
    echo "Hello, $1!"
}

# Syntax 2: function keyword
function greet {
    echo "Hello, $1!"
}

# Single-line syntax
greet() { echo "Hello, $1!"; }
```

**Function Calls:**

```bash
# Direct function calls (fully supported)
greet Alice           # $1 = Alice
greet Bob Charlie     # $1 = Bob, $2 = Charlie

# Functions can call other functions
outer() {
    echo "Calling inner..."
    inner
}

inner() {
    echo "Inside inner function"
}

outer
```

**Local Variables:**

```bash
counter() {
    local count=0          # Declare local variable
    count=$((count + 1))
    echo $count
}

# Local variables don't affect global scope
x=100
test_scope() {
    local x=10
    echo "Inside: $x"     # Prints: Inside: 10
}
test_scope
echo "Outside: $x"        # Prints: Outside: 100
```

**Return Values:**

```bash
is_positive() {
    if [ $1 -gt 0 ]; then
        return 0          # Success
    else
        return 1          # Failure
    fi
}

is_positive 5
echo "Exit code: $?"      # Prints: Exit code: 0
```

**Positional Parameters:**

```bash
show_args() {
    echo "Function: $0"   # Function name
    echo "Arg count: $#"  # Number of arguments
    echo "All args: $@"   # All arguments
    echo "First: $1"      # First argument
    echo "Second: $2"     # Second argument
}

show_args apple banana cherry
```

**Functions with Control Flow:**

```bash
# Functions with if/else
check_file() {
    if [ -f $1 ]; then
        echo "File exists: $1"
        return 0
    else
        echo "File not found: $1"
        return 1
    fi
}

check_file /local/tmp/test.txt

# Functions with loops
sum_numbers() {
    local total=0
    for num in $@; do
        total=$((total + num))
    done
    echo "Total: $total"
}

sum_numbers 1 2 3 4 5    # Total: 15

# Functions with arithmetic
calculate() {
    local a=$1
    local b=$2
    local sum=$((a + b))
    local product=$((a * b))
    echo "Sum: $sum, Product: $product"
}

calculate 5 3            # Sum: 8, Product: 15
```

**Recursive Functions:**

```bash
# Recursive functions are fully supported
factorial() {
    if [ $1 -le 1 ]; then
        echo 1
    else
        local prev=$(factorial $(($1 - 1)))
        echo $(($1 * prev))
    fi
}

result=$(factorial 5)
echo "factorial(5) = $result"  # Output: factorial(5) = 120

# Fibonacci example
fib() {
    if [ $1 -le 1 ]; then
        echo $1
    else
        local a=$(fib $(($1 - 1)))
        local b=$(fib $(($1 - 2)))
        echo $((a + b))
    fi
}

result=$(fib 7)
echo "fib(7) = $result"  # Output: fib(7) = 13
```

### Heredoc

```bash
# Variable expansion (default)
cat << EOF > /local/tmp/config.txt
Application: $APP_NAME
Version: $VERSION
Date: $(date)
EOF

# Literal mode (no expansion)
cat << 'EOF' > /local/tmp/script.sh
#!/bin/bash
echo "Price: $100"
VAR="literal"
EOF

# With indentation
cat <<- EOF
    Indented text
    Multiple lines
EOF
```

## Built-in Commands

### File System Commands

All file operations use AGFS paths (e.g., `/local/`, `/s3fs/`, `/sqlfs/`).

#### cd [path]
Change current directory.

```bash
cd /local/mydir          # Absolute path
cd mydir                 # Relative path
cd ..                    # Parent directory
cd                       # Home directory (/)
```

#### pwd
Print current working directory.

```bash
pwd                      # /local/mydir
```

#### ls [-l] [path]
List directory contents. Symlinks are displayed in cyan color with arrow notation.

```bash
ls                       # List current directory
ls /local                # List specific directory
ls -l                    # Long format with details
ls -l /local/*.txt       # List with glob pattern

# Symlink display example:
# aws2 -> /s3fs/aws      # Symlinks shown with arrow and target
```

#### tree [OPTIONS] [path]
Display directory tree structure.

```bash
tree /local              # Show tree
tree -L 2 /local         # Max depth 2
tree -d /local           # Directories only
tree -a /local           # Show hidden files
tree -h /local           # Human-readable sizes
```

#### cat [file...]
Concatenate and print files or stdin.

```bash
cat /local/tmp/file.txt      # Display file
cat file1.txt file2.txt      # Concatenate multiple
cat                          # Read from stdin
echo "hello" | cat           # Via pipeline
```

#### mkdir path
Create directory.

```bash
mkdir /local/tmp/newdir

# Note: mkdir does not support -p flag for creating parent directories
# Create directories one by one:
mkdir /local/tmp/a
mkdir /local/tmp/a/b
mkdir /local/tmp/a/b/c
```

#### touch path
Create empty file or update timestamp.

```bash
touch /local/tmp/newfile.txt
touch file1.txt file2.txt file3.txt
```

#### rm [-r] path
Remove file or directory.

```bash
rm /local/tmp/file.txt       # Remove file
rm -r /local/tmp/mydir       # Remove directory recursively
```

#### truncate -s SIZE FILE...
Truncate or extend file to specified size.

```bash
truncate -s 0 file.txt           # Truncate file to zero bytes (empty file)
truncate -s 1024 file.txt        # Truncate/extend file to 1024 bytes
truncate --size=100 f1 f2        # Truncate multiple files to 100 bytes
```

**Options:**
- `-s SIZE` or `--size=SIZE`: Set file size to SIZE bytes

**Behavior:**
- If SIZE is less than current file size, extra data is lost (file is truncated)
- If SIZE is greater than current file size, file is extended with null bytes
- SIZE must be a non-negative integer (number of bytes)

**Use Cases:**
- Clear log files without deleting them: `truncate -s 0 /local/tmp/app.log`
- Preallocate file space: `truncate -s 1048576 large.dat`
- Reset files to empty state while preserving permissions

#### mv source dest
Move or rename files/directories.

```bash
mv /local/tmp/old.txt /local/tmp/new.txt     # Rename
mv /local/tmp/file.txt /local/tmp/backup/    # Move to directory
mv local:~/file.txt /local/tmp/              # From local filesystem to AGFS
mv /local/tmp/file.txt local:~/              # From AGFS to local filesystem
```

#### stat path
Display file status and metadata.

```bash
stat /local/tmp/file.txt
```

#### cp [-r] source dest
Copy files between local filesystem and AGFS.

```bash
cp /local/tmp/file.txt /local/tmp/backup/file.txt           # Within AGFS
cp local:~/data.csv /local/tmp/imports/data.csv             # Local to AGFS
cp /local/tmp/report.txt local:~/Desktop/report.txt         # AGFS to local
cp -r /local/tmp/mydir /local/tmp/backup/mydir              # Recursive copy
```

#### upload [-r] local_path agfs_path
Upload files/directories from local to AGFS.

```bash
upload ~/Documents/report.pdf /local/tmp/backup/
upload -r ~/Projects/myapp /local/tmp/projects/
```

#### download [-r] agfs_path local_path
Download files/directories from AGFS to local.

```bash
download /local/tmp/data.json ~/Downloads/
download -r /local/tmp/logs ~/backup/logs/
```

#### ln [-s] target link_path
Create symbolic links. The `-s` flag is required (hard links are not supported).

```bash
# Create a symbolic link
ln -s /s3fs/aws /s3fs/backup

# Create a symlink to a directory
ln -s /local/data /shortcuts/mydata

# Relative path symlinks
cd /local/tmp
ln -s ../config/app.conf local_config

# Cross-mount symlinks work
ln -s /memfs/cache /local/shortcuts/cache

# Symlinks appear in ls with cyan color and arrow notation
ls -l /shortcuts
# mydata -> /local/data
```

**Features:**
- Virtual symlinks at the AGFS layer (no backend support required)
- Support for relative and absolute paths
- Cross-mount symlinks (link across different filesystems)
- Symlink chain resolution with cycle detection
- Symlinks work with `cd`, `ls`, `cat`, and other file operations

### Text Processing

#### echo [args...]
Print arguments to stdout.

```bash
echo "Hello, World!"
echo -n "No newline"
echo $HOME
```

#### grep [OPTIONS] PATTERN [files]
Search for patterns in text.

```bash
grep "error" /local/tmp/app.log          # Basic search
grep -i "ERROR" /local/tmp/app.log       # Case-insensitive
grep -n "function" /local/tmp/code.py    # Show line numbers
grep -c "TODO" /local/tmp/*.py           # Count matches
grep -v "debug" /local/tmp/app.log       # Invert match (exclude)
grep -l "import" /local/tmp/*.py         # Show filenames only
grep "^error" /local/tmp/app.log         # Lines starting with 'error'

# Multiple files
grep "pattern" file1.txt file2.txt

# With pipeline
cat /local/tmp/app.log | grep -i error | grep -v warning
```

#### fsgrep [OPTIONS] PATTERN PATH
Server-side grep using filesystem's custom implementation. **Supports VectorFS semantic search** and other custom grep implementations.

```bash
# VectorFS semantic search
fsgrep "container orchestration" /vectorfs/project/docs
fsgrep -n 5 "infrastructure automation" /vectorfs/namespace/docs

# Regular text grep on other filesystems
fsgrep "error" /local/tmp/app.log
fsgrep -i "warning" /s3fs/aws/logs/
fsgrep -c "TODO" /sqlfs/tidb/code/
```

**Options:**
- `-r`: Recursive search (default for directories)
- `-i`: Case insensitive (for text grep, not VectorFS)
- `-n NUM`: Return top N results (for VectorFS, default 10)
- `-c`: Count matches only
- `-q`: Quiet mode (only show if matches found)

**VectorFS Features:**
- **Semantic search** using embeddings instead of exact pattern matching
- Returns results ranked by relevance with similarity scores
- Automatically searches all documents in namespace
- Use `-n` to control how many results to return
- Perfect for finding conceptually similar content

**Examples:**

```bash
# Semantic search in documentation
fsgrep "how to deploy containers" /vectorfs/docs
# Output shows semantically similar results with relevance scores

# Limit results
fsgrep -n 3 "database migration" /vectorfs/project/docs

# Count semantic matches
fsgrep -c "API authentication" /vectorfs/codebase

# Regular grep on other filesystems
fsgrep -r "ERROR" /local/tmp/logs/
fsgrep -i "exception" /s3fs/aws/app.log
```

**Use Cases:**
- Find documentation by concept rather than exact keywords
- Discover related code or content semantically
- Search across large document collections efficiently
- Fallback to traditional grep on non-VectorFS filesystems

#### jq filter [files]
Process JSON data.

```bash
echo '{"name":"Alice","age":30}' | jq .              # Pretty print
echo '{"name":"Alice"}' | jq '.name'                 # Extract field
cat data.json | jq '.items[]'                        # Array iteration
cat users.json | jq '.[] | select(.active == true)'  # Filter
echo '[{"id":1},{"id":2}]' | jq '.[].id'            # Map

# Real-world example
cat /local/tmp/api_response.json | \
    jq '.users[] | select(.role == "admin") | .name'
```

#### wc [-l] [-w] [-c]
Count lines, words, and bytes.

```bash
wc /local/tmp/file.txt           # All counts
wc -l /local/tmp/file.txt        # Lines only
wc -w /local/tmp/file.txt        # Words only
cat /local/tmp/file.txt | wc -l  # Via pipeline
```

#### head [-n count]
Output first N lines (default 10).

```bash
head /local/tmp/file.txt         # First 10 lines
head -n 5 /local/tmp/file.txt    # First 5 lines
cat /local/tmp/file.txt | head -n 20
```

#### tail [-n count] [-f] [-F] [file...]
Output last N lines (default 10). With `-f`, continuously follow the file and output new lines as they are appended. **Only works with AGFS files.**

```bash
tail /local/tmp/file.txt         # Last 10 lines
tail -n 5 /local/tmp/file.txt    # Last 5 lines
tail -f /local/tmp/app.log       # Follow mode: show last 10 lines, then continuously follow
tail -n 20 -f /local/tmp/app.log # Show last 20 lines, then follow
tail -F /streamfs/live.log       # Stream mode: continuously read from stream
tail -F /streamrotate/metrics.log | grep ERROR  # Filter stream data
cat /local/tmp/file.txt | tail -n 20  # Via pipeline
```

**Follow Mode (`-f`):**
- For regular files on localfs, s3fs, etc.
- First shows the last n lines, then follows new content
- Polls the file every 100ms for size changes
- Perfect for monitoring log files
- Press Ctrl+C to exit follow mode
- Uses efficient offset-based reading to only fetch new content

**Stream Mode (`-F`):**
- **For filesystems that support stream API** (streamfs, streamrotatefs, etc.)
- Continuously reads from the stream without loading history
- Does NOT show historical data - only new data from the moment you start
- Uses streaming read to handle infinite streams efficiently
- Will error if the filesystem doesn't support streaming
- Perfect for real-time monitoring: `tail -F /streamfs/events.log`
- Works great with pipelines: `tail -F /streamrotate/app.log | grep ERROR`
- Press Ctrl+C to exit

#### sort [-r]
Sort lines alphabetically.

```bash
sort /local/tmp/file.txt         # Ascending
sort -r /local/tmp/file.txt      # Descending
cat /local/tmp/data.txt | sort | uniq
```

#### uniq
Remove duplicate adjacent lines.

```bash
cat /local/tmp/file.txt | sort | uniq
```

#### tr set1 set2
Translate characters.

```bash
echo "hello" | tr 'h' 'H'            # Hello
echo "HELLO" | tr 'A-Z' 'a-z'        # hello
echo "hello world" | tr -d ' '       # helloworld
```

#### rev
Reverse each line character by character.

```bash
echo "hello" | rev                   # olleh
cat /local/tmp/file.txt | rev
```

#### cut [OPTIONS]
Extract sections from lines.

```bash
# Extract fields (CSV)
echo "John,Doe,30" | cut -f 1,2 -d ','       # John,Doe

# Extract character positions
echo "Hello World" | cut -c 1-5              # Hello
echo "2024-01-15" | cut -c 6-                # 01-15

# Process file
cat /local/tmp/data.csv | cut -f 2,4 -d ',' | sort
```

#### tee [-a] [file...]
Read from stdin and write to both stdout and files. **Only works with AGFS files.**

```bash
# Output to screen and save to file
echo "Hello" | tee /local/tmp/output.txt

# Multiple files
cat /local/tmp/app.log | grep ERROR | tee /local/tmp/errors.txt /s3fs/aws/logs/errors.log

# Append mode
echo "New line" | tee -a /local/tmp/log.txt

# Real-world pipeline example
tail -f /local/tmp/app.log | grep ERROR | tee /s3fs/aws/log/errors.log

# With tail -F for streams
tail -F /streamfs/events.log | grep CRITICAL | tee /local/tmp/critical.log
```

**Options:**
- `-a`: Append to files instead of overwriting

**Features:**
- **Streaming output**: Writes to stdout line-by-line with immediate flush for real-time display
- **Streaming write**: Uses iterator-based streaming write to AGFS (non-append mode)
- **Multiple files**: Can write to multiple destinations simultaneously
- Works seamlessly in pipelines with `tail -f` and `tail -F`

**Use Cases:**
- Save pipeline output while still viewing it
- Log filtered data to multiple destinations
- Monitor logs in real-time while saving errors to a file

### Path Utilities

#### basename PATH [SUFFIX]
Extract filename from path.

```bash
basename /local/path/to/file.txt             # file.txt
basename /local/path/to/file.txt .txt        # file

# In scripts
for file in /local/tmp/*.csv; do
    filename=$(basename $file .csv)
    echo "Processing: $filename"
done
```

#### dirname PATH
Extract directory from path.

```bash
dirname /local/tmp/path/to/file.txt              # /local/tmp/path/to
dirname /local/tmp/file.txt                      # /local/tmp
dirname file.txt                                 # .

# In scripts
filepath=/local/tmp/data/file.txt
dirpath=$(dirname $filepath)
echo "Directory: $dirpath"
```

### Environment Variables

#### export [VAR=value ...]
Set environment variables.

```bash
export PATH=/usr/local/bin
export DATABASE_URL="postgres://localhost/mydb"
export LOG_LEVEL=debug

# Multiple variables
export VAR1=value1 VAR2=value2

# View all
export
```

#### env
Display all environment variables.

```bash
env                          # Show all
env | grep PATH              # Filter
```

#### unset VAR [VAR ...]
Remove environment variables.

```bash
unset DATABASE_URL
unset VAR1 VAR2
```

### Conditional Testing

#### test EXPRESSION
#### [ EXPRESSION ]

Evaluate conditional expressions.

**File Tests:**
```bash
[ -f /local/tmp/file.txt ]       # File exists and is regular file
[ -d /local/tmp/mydir ]          # Directory exists
[ -e /local/tmp/path ]           # Path exists

# Example
if [ -f /local/tmp/config.json ]; then
    cat /local/tmp/config.json
fi
```

**String Tests:**
```bash
[ -z "$VAR" ]                # String is empty
[ -n "$VAR" ]                # String is not empty
[ "$A" = "$B" ]              # Strings are equal
[ "$A" != "$B" ]             # Strings are not equal

# Example
if [ -z "$NAME" ]; then
    echo "Name is empty"
fi
```

**Integer Tests:**
```bash
[ $A -eq $B ]                # Equal
[ $A -ne $B ]                # Not equal
[ $A -gt $B ]                # Greater than
[ $A -lt $B ]                # Less than
[ $A -ge $B ]                # Greater or equal
[ $A -le $B ]                # Less or equal

# Example
if [ $COUNT -gt 10 ]; then
    echo "Count exceeds limit"
fi
```

**Logical Operators:**
```bash
[ ! -f file.txt ]            # NOT (negation)
[ -f file1.txt -a -f file2.txt ]    # AND
[ -f file1.txt -o -f file2.txt ]    # OR

# Example
if [ -f /local/tmp/input.txt -a -f /local/tmp/output.txt ]; then
    cat /local/tmp/input.txt > /local/tmp/output.txt
fi
```

### Control Flow Commands

#### break
Exit from the innermost for loop.

```bash
for i in 1 2 3 4 5; do
    if [ $i -eq 3 ]; then
        break
    fi
    echo $i
done
# Output: 1, 2
```

#### continue
Skip to next iteration of loop.

```bash
for i in 1 2 3 4 5; do
    if [ $i -eq 3 ]; then
        continue
    fi
    echo $i
done
# Output: 1, 2, 4, 5
```

#### exit [n]
Exit script or shell with status code.

```bash
exit            # Exit with status 0
exit 1          # Exit with status 1
exit $?         # Exit with last command's exit code

# In script
if [ ! -f /local/tmp/required.txt ]; then
    echo "Error: Required file not found"
    exit 1
fi
```

#### local VAR=value
Declare local variables (only valid within functions).

```bash
myfunction() {
    local counter=0        # Local to this function
    local name=$1          # Local copy of first argument
    counter=$((counter + 1))
    echo "Counter: $counter"
}

myfunction test           # Prints: Counter: 1
# 'counter' variable doesn't exist outside the function
```

#### return [n]
Return from a function with an optional exit status.

```bash
is_valid() {
    if [ $1 -gt 0 ]; then
        return 0          # Success
    else
        return 1          # Failure
    fi
}

is_valid 5
if [ $? -eq 0 ]; then
    echo "Valid number"
fi
```

### Job Control Commands

#### jobs [-l]
List background jobs with their status.

```bash
# Start some background jobs
sleep 10 &
sleep 5 &
cat /local/tmp/large.txt | grep pattern &

# List all jobs
jobs
# Output:
# [1] Running      sleep 10
# [2] Running      sleep 5
# [3] Running      cat /local/tmp/large.txt | grep pattern

# List with thread IDs
jobs -l
# Output:
# [1] 140234567890123 Running      sleep 10
# [2] 140234567890456 Running      sleep 5
# [3] 140234567890789 Running      cat /local/tmp/large.txt | grep pattern

# After jobs complete
jobs
# Output:
# (empty - completed jobs are automatically cleaned up after notification)
```

**Options:**
- `-l`: Show thread IDs along with job information

**Job Status:**
- `Running`: Job is currently executing
- `Done`: Job completed successfully (exit code 0)
- `Failed (exit N)`: Job completed with non-zero exit code N

**Behavior:**
- Jobs are listed with job ID, status, and command
- Completed jobs are shown once as notification, then automatically removed
- Job IDs are assigned sequentially starting from 1 within each shell session
- Thread-safe implementation for concurrent job management

#### wait [job_id]
Wait for background jobs to complete.

```bash
# Wait for all jobs
sleep 5 & sleep 3 &
wait                # Blocks until both complete
echo "All done"

# Wait for specific job
sleep 10 &          # Returns: [1] 140234567890123
sleep 5 &           # Returns: [2] 140234567890456
wait 2              # Wait only for job [2]
jobs                # Shows: [1] Running sleep 10
wait 1              # Now wait for job [1]

# Use wait's exit code
sleep 2 &
wait 1
echo "Exit code: $?"    # 0 if job succeeded

# Wait in scripts
process_data() {
    # Start parallel processing
    process_file1 &
    process_file2 &
    process_file3 &

    # Wait for all to complete before continuing
    wait
    echo "All files processed"
}
```

**Usage:**
- `wait` - Wait for all background jobs to complete
- `wait <job_id>` - Wait for specific job to complete

**Return Value:**
- Returns the exit code of the waited job
- Returns 0 when waiting for all jobs
- Returns 127 if job ID not found
- Returns 1 if job manager unavailable

**Examples:**

```bash
# Parallel data processing
for file in /local/tmp/data/*.txt; do
    (cat $file | grep pattern > $file.filtered) &
done
wait    # Wait for all filtering to complete
echo "All files filtered"

# Error handling with wait
long_task &
job_id=$!
if wait $job_id; then
    echo "Task succeeded"
else
    echo "Task failed with code: $?"
fi

# Sequential processing with background tasks
task1 &
wait                # Wait for task1
task2 &
wait                # Wait for task2
echo "Both tasks complete"
```

**Thread Safety:**
- All job operations are thread-safe
- Safe to call from multiple concurrent contexts
- Jobs can be waited on from any thread

#### source FILENAME [ARGUMENTS...]
#### . FILENAME [ARGUMENTS...]
Execute commands from a file in the current shell environment.

Variables and functions defined in the sourced file persist after execution.
This is equivalent to Bash's `source` or `.` command.

```bash
# Create a library file
cat << 'EOF' > /local/tmp/lib.sh
MY_VAR=hello
greet() {
    echo "Hello, $1!"
}
EOF

# Source the library
source /local/tmp/lib.sh
# Or using the dot syntax:
. /local/tmp/lib.sh

# Now variables and functions are available
echo $MY_VAR           # Output: hello
greet World            # Output: Hello, World!

# Pass arguments to sourced script
source /local/tmp/script.sh arg1 arg2
# Inside script.sh: $1=arg1, $2=arg2
```

### AGFS Management

#### chroot [PATH | --exit]
Change the root directory context for the shell session. This restricts file operations to within the specified directory, similar to Unix `chroot`.

```bash
# Show current chroot status
chroot
# Output: No chroot set (full access)

# Change root to a directory
chroot /local/data
# Output: Changed root to: /local/data

# After chroot, all paths are relative to the new root
pwd
# Output: /

ls /
# Lists contents of /local/data (the new root)

cd subdir
pwd
# Output: /subdir (actually /local/data/subdir)

# Exit chroot and return to full access
chroot --exit
# or
chroot -e
# Output: Exited chroot

# After exit, you're back to normal root
pwd
# Output: /
```

**Features:**
- Restricts file operations to within a specified directory tree
- All paths are interpreted relative to the chroot root
- Prompt changes to `agfs[chroot]:/>` to indicate chroot mode
- Use `--exit` or `-e` to exit chroot mode
- Virtual path display: `pwd` and paths shown are relative to chroot root
- Works with all file operations: `cd`, `ls`, `cat`, `mkdir`, etc.

**Use Cases:**
- Isolate operations to a specific project directory
- Prevent accidental access to files outside a workspace
- Create sandboxed environments for testing
- Simplify path management within a deep directory structure

**Example - Project Isolation:**
```bash
# Work on a specific project
chroot /local/projects/myapp

# Now all operations are confined to /local/projects/myapp
mkdir src
cd src
touch main.py
ls /
# Shows: src (and other contents of /local/projects/myapp)

# Exit when done
chroot --exit
```

#### plugins
Manage AGFS plugins.

```bash
plugins list

# Output:
# Builtin Plugins: (15)
#   localfs              -> /local/tmp
#   s3fs                 -> /etc, /s3fs/aws
#   ...
#
# No external plugins loaded
```

#### mount [PLUGIN] [PATH] [OPTIONS]
Mount a new AGFS plugin.

```bash
# Mount S3 filesystem
mount s3fs /s3-backup bucket=my-backup-bucket,region=us-west-2

# Mount SQL filesystem
mount sqlfs /sqldb connection=postgresql://localhost/mydb

# Mount custom plugin
mount customfs /custom option1=value1,option2=value2
```

### Utility Commands

#### alias [name[=value] ...]
Define or display command aliases. Create shortcuts for frequently used commands.

```bash
# List all aliases
alias

# Show specific alias
alias ll

# Create aliases
alias ll='ls -l'
alias la='ls -la'
alias ..='cd ..'
alias grep='grep --color=auto'

# Use alias in commands
ll /local/tmp
..
```

**Features:**
- Aliases are expanded before command execution
- Support for any command with arguments
- Aliases persist within shell session
- Can create aliases for pipelines and complex commands

**Examples:**

```bash
# Shortcut for long commands
alias lh='ls -lh'
alias count='wc -l'

# Pipeline aliases
alias errors='grep -i error'
alias tofile='tee /local/tmp/output.txt'

# Directory navigation
alias home='cd /local'
alias docs='cd /local/docs'

# Use aliases
cat /local/tmp/app.log | errors | count
```

**Note:** To prevent alias expansion, quote the command or use a backslash: `\command`

#### unalias [-a] name [name ...]
Remove command aliases.

```bash
# Remove single alias
unalias ll

# Remove multiple aliases
unalias ll la lh

# Remove all aliases
unalias -a
```

**Options:**
- `-a`: Remove all alias definitions

**Examples:**

```bash
# Create and remove aliases
alias test='echo test'
test                    # Output: test
unalias test
test                    # Error: command not found

# Clean up all aliases
unalias -a
alias                   # Shows no aliases
```

#### sleep seconds
Pause execution for specified seconds (supports decimals).

```bash
sleep 1              # Sleep for 1 second
sleep 0.5            # Sleep for half a second
sleep 2.5            # Sleep for 2.5 seconds

# In scripts
echo "Starting process..."
sleep 2
echo "Process started"

# Rate limiting
for i in 1 2 3 4 5; do
    echo "Processing item $i"
    sleep 1
done
```

#### date [FORMAT]
Display current date and time.

```bash
date                          # Wed Dec  6 10:23:45 PST 2025
date "+%Y-%m-%d"              # 2025-12-06
date "+%Y-%m-%d %H:%M:%S"     # 2025-12-06 10:23:45
date "+%H:%M:%S"              # 10:23:45

# Use in scripts
TIMESTAMP=$(date "+%Y%m%d_%H%M%S")
echo "Backup: backup_$TIMESTAMP.tar"

LOG_DATE=$(date "+%Y-%m-%d")
echo "[$LOG_DATE] Process started" >> /local/tmp/log.txt
```

#### help
Show help message.

```bash
help                 # Display comprehensive help
```

### Network Commands

#### http METHOD URL [options]
Make HTTP requests with persistent configuration. A minimalist HTTP client designed for shell scripting with bash-like state management.

```bash
# Basic requests
http GET https://api.example.com/users
http POST https://api.example.com/users -j '{"name":"alice"}'
http DELETE https://api.example.com/users/123

# With query parameters
http GET https://httpbin.org/get -q term=hello -q limit=10

# With custom headers
http GET https://httpbin.org/headers -H "Authorization:Bearer token123"
http POST https://httpbin.org/post -H "Content-Type:application/xml" -d '<data>...</data>'

# Download binary files
http GET https://example.com/file.tar.gz --stdout > file.tar.gz

# Set base URL for cleaner commands
http set base https://httpbin.org
http GET /get
http POST /post -j '{"name":"bob"}'

# Set default headers (persist across requests)
http set header Authorization "Bearer secret-token"
http set header X-API-Version "v2"
http GET /headers

# Set timeout
http set timeout 10s
http set timeout 5000ms

# Fail on non-2xx status codes
http GET https://httpbin.org/status/404 -f

# Show response headers
http GET https://httpbin.org/get -i

# Save response to variable
http GET https://httpbin.org/get -o response
```

**Options:**

| Option | Description |
|--------|-------------|
| `-H key:value` | Add request header |
| `-j JSON` | Send JSON body (auto-sets Content-Type: application/json) |
| `-d DATA` | Send raw body data |
| `-q key=value` | Add query parameter (can be used multiple times) |
| `-f` | Fail on non-2xx status codes (returns exit code 1) |
| `-i` | Show response headers in output |
| `-o var` | Save response to shell variable |
| `--stdout` | Output only raw response body (for binary downloads) |

**Configuration Commands:**

```bash
http set base <url>              # Set base URL for relative paths
http set header <key> <value>    # Set default header for all requests
http set timeout <duration>      # Set timeout (e.g., 5s, 1000ms)
```

**State Management:**

The HTTP client maintains persistent state within a shell session:
- Base URL: Set once, use relative paths in subsequent requests
- Default headers: Apply to all requests automatically
- Timeout: Configurable per session

State is stored in the shell and persists across commands in the same session.

**Response Format:**

Normal mode shows status line, optional headers, and body:
```
HTTP 200 (342ms)
{"result":"success"}
```

With `--stdout` flag, only raw body is output (useful for piping or downloading):
```bash
http GET https://example.com/data.json --stdout | jq .
http GET https://example.com/image.png --stdout > image.png
```

**Examples:**

```bash
# REST API workflow
http set base https://jsonplaceholder.typicode.com
http GET /posts/1
http POST /posts -j '{"title":"foo","body":"bar","userId":1}'
http PUT /posts/1 -j '{"id":1,"title":"updated","body":"content","userId":1}'
http DELETE /posts/1

# With authentication
http set base https://api.github.com
http set header Authorization "token ghp_xxxxxxxxxxxx"
http set header Accept "application/vnd.github.v3+json"
http GET /user/repos

# Download files
http GET https://example.com/archive.zip --stdout > archive.zip
http GET https://cdn.example.com/image.jpg --stdout > image.jpg

# Error handling
http GET https://httpbin.org/status/200 -f -o result
if [ $? -eq 0 ]; then
    echo "Request successful"
else
    echo "Request failed"
fi

# Pipeline integration
http GET https://httpbin.org/get --stdout | jq '.headers'
http GET https://jsonplaceholder.typicode.com/users --stdout | jq '.[] | .name'
```

**Use Cases:**
- REST API testing and interaction
- Downloading files from URLs
- CI/CD script integration
- API automation in shell scripts
- Quick HTTP requests without external tools

**Technical Details:**
- Uses Python's built-in `urllib.request` (no external dependencies)
- Supports GET, POST, PUT, DELETE, PATCH, and all HTTP methods
- Automatic JSON content-type headers with `-j` flag
- Binary-safe for downloading files with `--stdout`
- Reports response time in milliseconds

### AI Integration

#### llm [OPTIONS] [PROMPT]
Interact with LLM models using AI integration.

```bash
# Basic query
llm "What is the capital of France?"

# Process text through pipeline
echo "Translate to Spanish: Hello World" | llm

# Analyze file content
cat /local/code.py | llm "Explain what this code does"

# Use specific model
llm -m gpt-4 "Complex question requiring advanced reasoning"

# With system prompt
llm -s "You are a coding assistant" "How do I reverse a list in Python?"

# Process JSON data
cat /local/data.json | llm "Summarize this data in 3 bullet points"

# Analyze images (if model supports it)
cat /local/screenshot.png | llm -m gpt-4-vision "What's in this image?"

# Debugging help
cat /local/error.log | llm "Analyze these errors and suggest fixes"
```

**Options:**
- `-m MODEL` - Specify model (default: gpt-4o-mini)
- `-s SYSTEM` - System prompt
- `-k KEY` - API key (overrides config)
- `-c CONFIG` - Config file path

**Configuration:**
Create `/etc/llm.yaml` (in agfs)

```yaml
models:
  - name: gpt-4o-mini
    provider: openai
    api_key: sk-...
  - name: gpt-4
    provider: openai
    api_key: sk-...
```

## Script Files

Script files use the `.as` extension (AGFS Shell scripts).

### Creating Scripts

```bash
cat > example.as << 'EOF'
#!/usr/bin/env uv run agfs-shell

# Example script demonstrating AGFS shell features

# Variables
SOURCE_DIR=/local/tmp/data
BACKUP_DIR=/local/tmp/backup
TIMESTAMP=$(date "+%Y%m%d_%H%M%S")

# Create backup directory
mkdir $BACKUP_DIR

# Process files
count=0
for file in $SOURCE_DIR/*.txt; do
    count=$((count + 1))

    # Check file size
    echo "Processing file $count: $file"

    # Backup file with timestamp
    basename=$(basename $file .txt)
    cp $file $BACKUP_DIR/${basename}_${TIMESTAMP}.txt
done

echo "Backed up $count files to $BACKUP_DIR"
exit 0
EOF

chmod +x example.as
./example.as
```

### Script Arguments

Scripts can access command-line arguments:

```bash
cat > greet.as << 'EOF'
#!/usr/bin/env uv run agfs-shell

# Access arguments
echo "Script name: $0"
echo "First argument: $1"
echo "Second argument: $2"
echo "Number of arguments: $#"
echo "All arguments: $@"

# Use arguments
if [ $# -lt 1 ]; then
    echo "Usage: $0 <name>"
    exit 1
fi

echo "Hello, $1!"
EOF

chmod +x greet.as
./greet.as Alice Bob
```

### Advanced Script Example

```bash
cat > backup_system.as << 'EOF'
#!/usr/bin/env uv run agfs-shell

# Advanced backup script with error handling

# Configuration
BACKUP_ROOT=/local/tmp/backups
SOURCE_DIRS="/local/tmp/data /local/tmp/config /local/tmp/logs"
DATE=$(date "+%Y-%m-%d")
BACKUP_DIR=$BACKUP_ROOT/$DATE
ERROR_LOG=$BACKUP_DIR/errors.log

# Create backup directory
mkdir $BACKUP_ROOT
mkdir $BACKUP_DIR

# Initialize error log
echo "Backup started at $(date)" > $ERROR_LOG

# Backup function simulation with loop
backup_count=0
error_count=0

for src in $SOURCE_DIRS; do
    if [ -d $src ]; then
        echo "Backing up $src..." | tee -a $ERROR_LOG

        dest_name=$(basename $src)
        if cp -r $src $BACKUP_DIR/$dest_name 2>> $ERROR_LOG; then
            backup_count=$((backup_count + 1))
            echo "  Success: $src" >> $ERROR_LOG
        else
            error_count=$((error_count + 1))
            echo "  Error: Failed to backup $src" >> $ERROR_LOG
        fi
    else
        echo "Warning: $src not found, skipping" | tee -a $ERROR_LOG
        error_count=$((error_count + 1))
    fi
done

# Create manifest
cat << MANIFEST > $BACKUP_DIR/manifest.txt
Backup Manifest
===============
Date: $DATE
Time: $(date "+%H:%M:%S")
Source Directories: $SOURCE_DIRS
Successful Backups: $backup_count
Errors: $error_count
MANIFEST

# Generate tree of backup
tree -h $BACKUP_DIR > $BACKUP_DIR/contents.txt

echo "Backup completed: $BACKUP_DIR"
echo "Summary: $backup_count successful, $error_count errors"

# Exit with appropriate code
if [ $error_count -gt 0 ]; then
    exit 1
else
    exit 0
fi
EOF

chmod +x backup_system.as
./backup_system.as
```

## Interactive Features

### Command History

- **Persistent History**: Commands saved in `~/.agfs_shell_history`
- **Navigation**: Use ↑/↓ arrow keys
- **Customizable**: Set `HISTFILE` variable to change location

```bash
agfs:/> export HISTFILE=/tmp/my_history.txt
agfs:/> # Commands now saved to /tmp/my_history.txt
```

### Tab Completion

- **Command Completion**: Tab completes command names
- **Path Completion**: Tab completes file and directory paths
- **AGFS-Aware**: Works with AGFS filesystem

```bash
agfs:/> ec<Tab>              # Completes to "echo"
agfs:/> cat /lo<Tab>         # Completes to "/local/"
agfs:/> ls /local/tmp/te<Tab>    # Completes to "/local/tmp/test.txt"
```

### Multiline Editing

- **Backslash Continuation**: End line with `\`
- **Quote Matching**: Unclosed quotes continue to next line
- **Bracket Matching**: Unclosed `()` or `{}` continue

```bash
agfs:/> echo "This is a \
> very long \
> message"
This is a very long message

agfs:/> if [ -f /local/tmp/file.txt ]; then
>   cat /local/tmp/file.txt
> fi
```

### Keyboard Shortcuts

- **Ctrl-A**: Move to beginning of line
- **Ctrl-E**: Move to end of line
- **Ctrl-K**: Delete from cursor to end
- **Ctrl-U**: Delete from cursor to beginning
- **Ctrl-W**: Delete word before cursor
- **Ctrl-L**: Clear screen
- **Ctrl-D**: Exit shell (when line empty)
- **Ctrl-C**: Cancel current input

## Startup Scripts (initrc)

When agfs-shell starts, it automatically checks for and executes initialization scripts from the AGFS filesystem. This allows you to set up environment variables, define functions, aliases, and perform other initialization tasks.

### Command Line Options

Control initrc execution with these options:

```bash
# Skip all initrc scripts
agfs-shell --skip-initrc

# Use a custom initrc script (skips default scripts)
agfs-shell --initrc /path/to/custom_initrc.as

# Custom initrc from AGFS path
agfs-shell --initrc /etc/my_profile

# Custom initrc from local filesystem
agfs-shell --initrc ~/my_initrc.as
```

### Default Initialization File Locations

When no `--initrc` or `--skip-initrc` is specified, the following files are checked in order. If a file exists, it is executed as an agfs-shell script:

1. `/etc/rc`
2. `/etc/initrc`
3. `/initrc.as`
4. `/etc/profile`
5. `/etc/profile.as`

All paths are in the AGFS filesystem, not the local filesystem.

### Example initrc File

Create an initialization script in AGFS:

```bash
# /etc/profile.as - System-wide initialization script

# Set custom environment variables
export BACKUP_DIR=/local/tmp/backups
export DATA_DIR=/local/tmp/data

# Define utility functions
greet() {
    echo "Hello, $1! Welcome to AGFS Shell."
}

backup() {
    local src=$1
    local timestamp=$(date "+%Y%m%d_%H%M%S")
    cp $src ${src}.backup_${timestamp}
    echo "Backed up $src"
}

# Define aliases using functions
ll() {
    ls -l $@
}

la() {
    ls -a $@
}

# Display welcome message
echo "AGFS Shell initialized at $(date)"
echo "Type 'help' for available commands"
```

### Behavior

- Scripts are executed in order; later scripts can override earlier definitions
- Errors in initrc scripts are reported as warnings but do not prevent shell startup
- Functions and variables defined in initrc are available in the shell session
- initrc is executed for all modes: interactive REPL, script execution, and `-c` command mode
- initrc is NOT executed in webapp mode

### Creating initrc Files

```bash
# Create /etc directory if needed
mkdir /etc

# Create profile script
cat << 'EOF' > /etc/profile.as
# My AGFS Shell profile
export MY_VAR="hello"

myhelper() {
    echo "Custom helper function"
}
EOF

# Verify it exists
cat /etc/profile.as
```

## Complex Examples

### Example 1: Log Analysis Pipeline

```bash
#!/usr/bin/env uv run agfs-shell

# Analyze application logs across multiple servers

LOG_DIR=/local/tmp/logs
OUTPUT_DIR=/local/tmp/analysis

# Create directories
mkdir /local/tmp/logs
mkdir /local/tmp/analysis

# Create sample log files for demonstration
for server in web1 web2 web3; do
    echo "Creating sample log for $server..."
    echo "INFO: Server $server started" > $LOG_DIR/$server.log
    echo "ERROR: Connection failed" >> $LOG_DIR/$server.log
    echo "CRITICAL: System failure" >> $LOG_DIR/$server.log
done

# Find all errors
cat $LOG_DIR/*.log | grep -i error > $OUTPUT_DIR/all_errors.txt

# Count errors by server
echo "Error Summary:" > $OUTPUT_DIR/summary.txt
for server in web1 web2 web3; do
    count=$(cat $LOG_DIR/$server.log | grep -i error | wc -l)
    echo "$server: $count errors" >> $OUTPUT_DIR/summary.txt
done

# Extract unique error messages
cat $OUTPUT_DIR/all_errors.txt | \
    cut -c 21- | \
    sort | \
    uniq > $OUTPUT_DIR/unique_errors.txt

# Find critical errors
cat $LOG_DIR/*.log | \
    grep -i critical > $OUTPUT_DIR/critical.txt

# Generate report
cat << EOF > $OUTPUT_DIR/report.txt
Log Analysis Report
===================
Generated: $(date)

$(cat $OUTPUT_DIR/summary.txt)

Unique Errors:
$(cat $OUTPUT_DIR/unique_errors.txt)

Critical Errors: $(cat $OUTPUT_DIR/critical.txt | wc -l)
EOF

cat $OUTPUT_DIR/report.txt
```

### Example 2: Data Processing Pipeline

```bash
#!/usr/bin/env uv run agfs-shell

# Process CSV data and generate JSON reports

INPUT_DIR=/local/tmp/data
OUTPUT_DIR=/local/tmp/reports
TEMP_DIR=/local/tmp/temp
TIMESTAMP=$(date "+%Y%m%d_%H%M%S")

# Create directories
mkdir $INPUT_DIR
mkdir $OUTPUT_DIR
mkdir $TEMP_DIR

# Create sample CSV files
echo "name,value,category,score" > $INPUT_DIR/data1.csv
echo "Alice,100,A,95" >> $INPUT_DIR/data1.csv
echo "Bob,200,B,85" >> $INPUT_DIR/data1.csv
echo "Charlie,150,A,90" >> $INPUT_DIR/data1.csv

# Process each CSV file
for csv_file in $INPUT_DIR/*.csv; do
    filename=$(basename $csv_file .csv)
    echo "Processing $filename..."

    # Extract specific columns (name and score - columns 1 and 4)
    cat $csv_file | \
        tail -n +2 | \
        cut -f 1,4 -d ',' > $TEMP_DIR/extracted_${filename}.txt

    # Count lines
    line_count=$(cat $TEMP_DIR/extracted_${filename}.txt | wc -l)
    echo "  Processed $line_count records from $filename"
done

# Generate summary JSON
cat << EOF > $OUTPUT_DIR/summary_${TIMESTAMP}.json
{
    "timestamp": "$(date "+%Y-%m-%d %H:%M:%S")",
    "files_processed": $(ls $INPUT_DIR/*.csv | wc -l),
    "output_directory": "$OUTPUT_DIR"
}
EOF

echo "Processing complete. Reports in $OUTPUT_DIR"
```

### Example 3: Backup with Verification

```bash
#!/usr/bin/env uv run agfs-shell

# Comprehensive backup with verification

SOURCE=/local/tmp/important
BACKUP_NAME=backup_$(date "+%Y%m%d")
BACKUP=/local/tmp/backups/$BACKUP_NAME
MANIFEST=$BACKUP/manifest.txt

# Create backup directories
mkdir /local/tmp/backups
mkdir $BACKUP

# Copy files
echo "Starting backup..." > $MANIFEST
echo "Date: $(date)" >> $MANIFEST
echo "Source: $SOURCE" >> $MANIFEST
echo "" >> $MANIFEST

file_count=0
byte_count=0

for file in $SOURCE/*; do
    if [ -f $file ]; then
        filename=$(basename $file)
        echo "Backing up: $filename"

        cp $file $BACKUP/$filename

        if [ $? -eq 0 ]; then
            file_count=$((file_count + 1))
            size=$(stat $file | grep Size | cut -d: -f2)
            byte_count=$((byte_count + size))
            echo "  [OK] $filename" >> $MANIFEST
        else
            echo "  [FAILED] $filename" >> $MANIFEST
        fi
    fi
done

echo "" >> $MANIFEST
echo "Summary:" >> $MANIFEST
echo "  Files backed up: $file_count" >> $MANIFEST
echo "  Total size: $byte_count bytes" >> $MANIFEST

# Verification
echo "" >> $MANIFEST
echo "Verification:" >> $MANIFEST

for file in $SOURCE/*; do
    if [ -f $file ]; then
        filename=$(basename $file)
        backup_file=$BACKUP/$filename

        if [ -f $backup_file ]; then
            echo "  [OK] $filename verified" >> $MANIFEST
        else
            echo "  [MISSING] $filename" >> $MANIFEST
        fi
    fi
done

cat $MANIFEST
echo "Backup completed: $BACKUP"
```

### Example 4: Multi-Environment Configuration Manager

```bash
#!/usr/bin/env uv run agfs-shell

# Manage configurations across multiple environments

# Check arguments
if [ $# -lt 1 ]; then
    echo "Usage: $0 <environment>"
    echo "Environments: dev, staging, production"
    exit 1
fi

ENV=$1
CONFIG_DIR=/local/tmp/config
DEPLOY_DIR=/local/tmp/deployed

# Validate environment
if [ "$ENV" != "dev" -a "$ENV" != "staging" -a "$ENV" != "production" ]; then
    echo "Error: Invalid environment '$ENV'"
    exit 1
fi

echo "Deploying configuration for: $ENV"

# Load environment-specific config
CONFIG_FILE=$CONFIG_DIR/$ENV.env

if [ ! -f $CONFIG_FILE ]; then
    echo "Error: Configuration file not found: $CONFIG_FILE"
    exit 1
fi

# Parse and export variables
for line in $(cat $CONFIG_FILE); do
    export $line
done

# Generate deployment manifest
MANIFEST=$DEPLOY_DIR/manifest_$ENV.txt

cat << EOF > $MANIFEST
Deployment Manifest
===================
Environment: $ENV
Deployed: $(date)

Configuration:
$(cat $CONFIG_FILE)

Mounted Filesystems:
$(plugins list | grep "->")

Status: SUCCESS
EOF

# Deploy to all relevant filesystems
for mount in /local/tmp /s3fs; do
    if [ -d $mount ]; then
        echo "Deploying to $mount..."
        mkdir $mount/config
        cp $CONFIG_FILE $mount/config/current.env

        if [ $? -eq 0 ]; then
            echo "  [OK] Deployed to $mount"
        else
            echo "  [FAILED] Failed to deploy to $mount"
        fi
    fi
done

echo "Deployment complete. Manifest: $MANIFEST"
cat $MANIFEST
```

## Architecture

### Recent Refactoring (2026-01-17)

**Phase 1-3 Complete:** The codebase has undergone significant refactoring to improve maintainability and code quality:

- **99.1% Code Reduction**: `builtins.py` reduced from 3,780 lines to 33 lines
- **Full Modularity**: All 57 built-in commands now in separate modules under `commands/`
- **Test Coverage**: Increased from <20% to 25% with 92 test cases
- **Zero Regressions**: All refactoring validated with comprehensive test suite
- **Better Organization**: Eliminated code duplication, shared error handling utilities

For detailed information:
- **Refactoring Completion Report**: `PHASE_1-3_COMPLETION_REPORT.md`
- **Architecture Documentation**: `ARCHITECTURE.md`
- **Work Progress**: `WORK.md`
- **Lessons Learned**: `AGENTS.md`
- **Refactoring Decisions**: `REFACTORING.md`

### Project Structure

```
agfs-shell/
├── agfs_shell/
│   ├── __init__.py          # Package initialization
│   ├── streams.py           # Stream classes (InputStream, OutputStream, ErrorStream)
│   ├── process.py           # Process class for command execution
│   ├── pipeline.py          # Pipeline class for chaining processes
│   ├── parser.py            # Command line parser
│   ├── builtins.py          # Command registry (33 lines, formerly 3,780)
│   ├── filesystem.py        # AGFS filesystem abstraction
│   ├── http_client.py       # HTTP client with persistent state
│   ├── config.py            # Configuration management
│   ├── shell.py             # Shell with REPL and control flow (2,749 lines)
│   ├── expression.py        # Expression expansion and variables (1,155 lines)
│   ├── completer.py         # Tab completion
│   ├── cli.py               # CLI entry point
│   ├── exit_codes.py        # Exit code constants
│   ├── command_decorators.py # Command metadata
│   ├── utils/               # Shared utilities
│   │   ├── io_wrappers.py   # BufferedTextIO and stream utilities
│   │   └── formatters.py    # Output formatting utilities
│   └── commands/            # Built-in command modules (57 commands)
│       ├── __init__.py      # Command registry and loader
│       ├── base.py          # Shared error handling utilities
│       ├── http.py          # HTTP command
│       ├── cat.py, ls.py, grep.py...  # File operations
│       ├── echo.py, wc.py, sort.py...  # Text processing
│       ├── jobs.py, wait.py  # Job control
│       └── ...              # 50+ other commands
├── tests/                   # Test suite (92 tests, 25% coverage)
│   ├── conftest.py          # Test fixtures and MockFileSystem
│   ├── test_builtins.py     # Built-in command tests
│   ├── test_parser.py       # Parser tests
│   ├── test_pipeline.py     # Pipeline tests
│   ├── test_process.py      # Process tests
│   └── test_shell_core.py   # Shell core functionality tests
├── pyproject.toml           # Project configuration
├── README.md                # This file
├── ARCHITECTURE.md          # Detailed architecture documentation
├── WORK.md                  # Development progress tracking
├── AGENTS.md                # Refactoring lessons learned
├── REFACTORING.md           # Refactoring decision log
├── PHASE_1-3_COMPLETION_REPORT.md  # Phase 1-3 completion summary
└── examples/
    ├── example.as           # Example scripts
    ├── backup_system.as
    └── data_pipeline.as
```

### Design Philosophy

1. **Stream Abstraction**: Everything as streams (stdin/stdout/stderr)
2. **Process Composition**: Simple commands compose into complex operations
3. **Pipeline Execution**: Output of one process → input of next
4. **AGFS Integration**: All file I/O through AGFS (no local filesystem)
5. **Pure Python**: No subprocess for built-ins (educational)

### Key Features

- Unix-style pipelines (`|`)
- Background job control (`command &`, `jobs`, `wait`)
- I/O Redirection (`<`, `>`, `>>`, `2>`, `2>>`)
- Heredoc (`<<` with expansion)
- Variables (`VAR=value`, `$VAR`, `${VAR}`)
- Special variables (`$?`, `$1`, `$@`, etc.)
- Arithmetic expansion (`$((expr))`)
- Command substitution (`$(cmd)`, backticks)
- Glob expansion (`*.txt`, `[abc]`)
- Control flow (`if/then/else/fi`, `for/do/done`)
- Conditional testing (`test`, `[ ]`)
- Loop control (`break`, `continue`)
- User-defined functions with local variables
- Tab completion and history
- 50+ built-in commands
- Script execution (`.as` files)
- HTTP client with persistent state (`http` command)
- AI integration (`llm` command)
- Chroot support for directory isolation
- Command aliasing (`alias`/`unalias`)
- VectorFS semantic search (`fsgrep`)

## Testing

### Run Built-in Tests

```bash
# Run Python tests
uv run pytest

# Run specific test
uv run pytest tests/test_builtins.py -v

# Run shell script tests
./test_simple_for.agfsh
./test_for.agfsh
./test_for_with_comment.agfsh

# Run function tests
./test_functions_working.as      # Comprehensive test of all working features
```

### Manual Testing

```bash
# Start interactive shell
uv run agfs-shell

# Test pipelines
agfs:/> echo "hello world" | grep hello | wc -w

# Test variables
agfs:/> NAME="Alice"
agfs:/> echo "Hello, $NAME"

# Test arithmetic
agfs:/> count=0
agfs:/> count=$((count + 1))
agfs:/> echo $count

# Test control flow
agfs:/> for i in 1 2 3; do echo $i; done

# Test file operations
agfs:/> echo "test" > /local/tmp/test.txt
agfs:/> cat /local/tmp/test.txt

# Test functions
agfs:/> add() { echo $(($1 + $2)); }
agfs:/> add 5 3
8

agfs:/> greet() { echo "Hello, $1!"; }
agfs:/> greet Alice
Hello, Alice!
```

## Configuration

### Server URL

Configure AGFS server URL:

```bash
# Via environment variable (preferred)
export AGFS_API_URL=http://192.168.1.100:8080
uv run agfs-shell

# Via command line argument
uv run agfs-shell --agfs-api-url http://192.168.1.100:8080

# Via config file
# Create ~/.agfs_shell_config with:
# server_url: http://192.168.1.100:8080
```

### Timeout

Set request timeout:

```bash
export AGFS_TIMEOUT=60
uv run agfs-shell --timeout 60
```

### Environment Variables

Inject environment variables at startup using `--env`:

```bash
# Single variable
agfs-shell --env FOO=bar

# Multiple variables
agfs-shell --env FOO=bar --env BAZ=qux -c 'echo $FOO $BAZ'

# Bulk injection from shell environment
agfs-shell --env "$(env)" -c 'echo $PATH'

# Bulk injection from custom format (KEY=VALUE per line)
agfs-shell --env "$(cat my_env_file)" -c 'env'
```

**Features:**
- Single `KEY=VALUE` format for individual variables
- Multi-line format for bulk injection (one `KEY=VALUE` per line)
- Can be combined: `--env "$(env)" --env CUSTOM=value`
- Variables are available immediately in shell session

## Technical Notes

### Function Implementation

The function implementation supports:
- Function definition and direct calls
- Parameters (`$1`, `$2`, `$@`, etc.)
- Local variables with `local` command
- Return values with `return` command
- Control flow (`if`, `for`) inside functions
- Arithmetic expressions with local variables
- Recursive functions with command substitution
- Nested command substitutions

**Example - Recursive Factorial:**

```bash
factorial() {
    if [ $1 -le 1 ]; then
        echo 1
    else
        local prev=$(factorial $(($1 - 1)))
        echo $(($1 * prev))
    fi
}

result=$(factorial 5)
echo "factorial(5) = $result"  # Output: factorial(5) = 120
```

**Example - Nested Command Substitution:**

```bash
echo "Triple nested: $(echo $(echo $(echo hello)))"  # Output: Triple nested: hello
```

## Contributing

This is an experimental/educational project. Contributions welcome!

1. Fork the repository
2. Create your feature branch
3. Add tests for new features
4. Submit a pull request

**Areas for Contribution:**
- Add more built-in commands
- Enhance error handling
- Improve performance

## License

[Add your license here]

## Credits

Built with:
- [pyagfs](https://github.com/c4pt0r/pyagfs) - Python client for AGFS
- [Rich](https://github.com/Textualize/rich) - Terminal formatting
- Pure Python - No external dependencies for core shell

---

**Note**: This is an experimental shell for educational purposes and AGFS integration. Not recommended for production use.
