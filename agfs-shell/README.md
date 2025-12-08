# agfs-shell

An experimental shell implementation with Unix-style pipeline support and **AGFS integration**, written in pure Python.

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
  - [Functions](#functions)
  - [Heredoc](#heredoc)
- [Built-in Commands](#built-in-commands)
  - [File System Commands](#file-system-commands)
  - [Text Processing](#text-processing)
  - [Environment Variables](#environment-variables)
  - [Conditional Testing](#conditional-testing)
  - [Control Flow Commands](#control-flow-commands)
  - [AGFS Management](#agfs-management)
  - [Utility Commands](#utility-commands)
  - [AI Integration](#ai-integration)
- [Script Files](#script-files)
- [Interactive Features](#interactive-features)
- [Complex Examples](#complex-examples)
- [Architecture](#architecture)
- [Testing](#testing)

## Overview

agfs-shell is a lightweight, educational shell that demonstrates Unix pipeline concepts while integrating with the AGFS (Aggregated File System) server. All file operations go through AGFS, allowing you to work with multiple backend filesystems (local, S3, SQL, etc.) through a unified interface.

**Key Features:**
- Unix-style pipelines and redirection
- Full scripting support with control flow
- User-defined functions with local variables
- AGFS integration for distributed file operations
- Tab completion and command history
- AI-powered command (llm integration)
- Pure Python implementation (no subprocess for builtins)

## Features

### Core Shell Features
- **Pipelines**: Chain commands with `|` operator
- **I/O Redirection**: `<`, `>`, `>>`, `2>`, `2>>`
- **Heredoc**: Multi-line input with `<<` (supports variable expansion)
- **Variables**: Assignment, expansion, special variables (`$?`, `$1`, `$@`, etc.)
- **Arithmetic**: `$((expression))` for calculations
- **Command Substitution**: `$(command)` or backticks
- **Glob Expansion**: `*.txt`, `file?.dat`, `[abc]`
- **Control Flow**: `if/then/elif/else/fi` and `for/in/do/done`
- **Functions**: User-defined functions with parameters and local variables
- **Comments**: `#` and `//` style comments

### Built-in Commands (39+)
- **File Operations**: cd, pwd, ls, tree, cat, mkdir, touch, rm, mv, stat, cp, upload, download
- **Text Processing**: echo, grep, jq, wc, head, tail, sort, uniq, tr, rev, cut
- **Path Utilities**: basename, dirname
- **Variables**: export, env, unset, local
- **Testing**: test, [ ]
- **Control Flow**: break, continue, exit, return
- **Utilities**: sleep, date, plugins, mount, help
- **AI**: llm (LLM integration)

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
# Option 1: Run from source
cd agfs-server
go run main.go

# Option 2: Use Docker
docker run -p 8080:8080 c4pt0r/agfs-server:latest
```

## Installation

```bash
cd agfs-shell
uv sync
```

## Quick Start

### Interactive Mode

```bash
uv run agfs-shell

agfs:/> echo "Hello, World!" > /local/hello.txt
agfs:/> cat /local/hello.txt
Hello, World!

agfs:/> ls /local | grep txt
hello.txt

agfs:/> for i in 1 2 3; do
>   echo "Count: $i"
> done
Count: 1
Count: 2
Count: 3
```

### Execute Command String

```bash
# Using -c flag
uv run agfs-shell -c "echo 'test' > /local/test.txt"

# With pipeline
uv run agfs-shell -c "cat /local/data.txt | sort | uniq > /local/sorted.txt"
```

### Execute Script File

Create a script file with `.as` extension:

```bash
cat > example.as << 'EOF'
#!/usr/bin/env uv run agfs-shell

# Count files in directory
count=0
for file in /local/*; do
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
cat /local/data.txt | grep "error" | wc -l
ls /local | sort | head -n 10
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
echo "Hello, $NAME!"    # In double quotes

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
file_count=$(ls /local | wc -l)
today=$(date "+%Y-%m-%d")

# Using backticks (also works)
files=`ls /local`

# Nested substitution
newest_file=$(ls -t /local | head -n 1)

# In strings
echo "There are $(ls /local | wc -l) files in the directory"
```

### Glob Patterns

```bash
# Wildcard matching
*.txt                   # All .txt files
file?.dat               # file followed by any single character
test[123].log           # test1.log, test2.log, or test3.log
file[a-z].txt           # file with single letter a-z

# Examples
cat /local/*.txt        # Concatenate all text files
rm /local/temp_*        # Remove all temp_ files
for file in /local/data_[0-9]*.json; do
    echo "Processing $file"
done
```

### Control Flow

**If Statements:**

```bash
# Basic if
if [ -f /local/file.txt ]; then
    echo "File exists"
fi

# If-else
if [ -d /local/mydir ]; then
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
for file in /local/*.txt; do
    echo "Processing $file"
    cat $file | wc -l
done

# Loop with command substitution
for user in $(cat /local/users.txt); do
    echo "User: $user"
done

# Nested loops
for dir in /local/projects/*; do
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
# Call with arguments
greet Alice           # $1 = Alice
greet Bob Charlie     # $1 = Bob, $2 = Charlie

# Use in command substitution
result=$(add 5 3)
echo "Result: $result"
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

**Advanced Examples:**

```bash
# Recursive function
factorial() {
    local n=$1
    if [ $n -le 1 ]; then
        echo 1
    else
        local prev=$(factorial $((n - 1)))
        echo $((n * prev))
    fi
}

result=$(factorial 5)
echo "5! = $result"       # Prints: 5! = 120

# Function composition
add() { echo $((${1} + ${2})); }
multiply() { echo $((${1} * ${2})); }

compute() {
    local sum=$(add $1 $2)
    local product=$(multiply $sum $3)
    echo $product
}

compute 5 3 4             # (5 + 3) * 4 = 32

# Functions with loops
process_files() {
    for file in $@; do
        if [ -f $file ]; then
            echo "Processing: $file"
            cat $file | wc -l
        fi
    done
}

process_files /local/*.txt
```

### Heredoc

```bash
# Variable expansion (default)
cat << EOF > /local/config.txt
Application: $APP_NAME
Version: $VERSION
Date: $(date)
EOF

# Literal mode (no expansion)
cat << 'EOF' > /local/script.sh
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
List directory contents.

```bash
ls                       # List current directory
ls /local                # List specific directory
ls -l                    # Long format with details
ls -l /local/*.txt       # List with glob pattern
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
cat /local/file.txt      # Display file
cat file1.txt file2.txt  # Concatenate multiple
cat                      # Read from stdin
echo "hello" | cat       # Via pipeline
```

#### mkdir path
Create directory.

```bash
mkdir /local/newdir

# Note: mkdir does not support -p flag for creating parent directories
# Create directories one by one:
mkdir /local/a
mkdir /local/a/b
mkdir /local/a/b/c
```

#### touch path
Create empty file or update timestamp.

```bash
touch /local/newfile.txt
touch file1.txt file2.txt file3.txt
```

#### rm [-r] path
Remove file or directory.

```bash
rm /local/file.txt       # Remove file
rm -r /local/mydir       # Remove directory recursively
```

#### mv source dest
Move or rename files/directories.

```bash
mv /local/old.txt /local/new.txt     # Rename
mv /local/file.txt /local/backup/    # Move to directory
mv local:~/file.txt /local/          # From local filesystem to AGFS
mv /local/file.txt local:~/          # From AGFS to local filesystem
```

#### stat path
Display file status and metadata.

```bash
stat /local/file.txt
```

#### cp [-r] source dest
Copy files between local filesystem and AGFS.

```bash
cp /local/file.txt /local/backup/file.txt           # Within AGFS
cp local:~/data.csv /local/imports/data.csv         # Local to AGFS
cp /local/report.txt local:~/Desktop/report.txt     # AGFS to local
cp -r /local/mydir /local/backup/mydir              # Recursive copy
```

#### upload [-r] local_path agfs_path
Upload files/directories from local to AGFS.

```bash
upload ~/Documents/report.pdf /local/backup/
upload -r ~/Projects/myapp /local/projects/
```

#### download [-r] agfs_path local_path
Download files/directories from AGFS to local.

```bash
download /local/data.json ~/Downloads/
download -r /local/logs ~/backup/logs/
```

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
grep "error" /local/app.log          # Basic search
grep -i "ERROR" /local/app.log       # Case-insensitive
grep -n "function" /local/code.py    # Show line numbers
grep -c "TODO" /local/*.py           # Count matches
grep -v "debug" /local/app.log       # Invert match (exclude)
grep -l "import" /local/*.py         # Show filenames only
grep "^error" /local/app.log         # Lines starting with 'error'

# Multiple files
grep "pattern" file1.txt file2.txt

# With pipeline
cat /local/app.log | grep -i error | grep -v warning
```

#### jq filter [files]
Process JSON data.

```bash
echo '{"name":"Alice","age":30}' | jq .              # Pretty print
echo '{"name":"Alice"}' | jq '.name'                 # Extract field
cat data.json | jq '.items[]'                        # Array iteration
cat users.json | jq '.[] | select(.active == true)'  # Filter
echo '[{"id":1},{"id":2}]' | jq '.[].id'            # Map

# Real-world example
cat /local/api_response.json | \
    jq '.users[] | select(.role == "admin") | .name'
```

#### wc [-l] [-w] [-c]
Count lines, words, and bytes.

```bash
wc /local/file.txt           # All counts
wc -l /local/file.txt        # Lines only
wc -w /local/file.txt        # Words only
cat /local/file.txt | wc -l  # Via pipeline
```

#### head [-n count]
Output first N lines (default 10).

```bash
head /local/file.txt         # First 10 lines
head -n 5 /local/file.txt    # First 5 lines
cat /local/file.txt | head -n 20
```

#### tail [-n count]
Output last N lines (default 10).

```bash
tail /local/file.txt         # Last 10 lines
tail -n 5 /local/file.txt    # Last 5 lines
cat /local/file.txt | tail -n 20
```

#### sort [-r]
Sort lines alphabetically.

```bash
sort /local/file.txt         # Ascending
sort -r /local/file.txt      # Descending
cat /local/data.txt | sort | uniq
```

#### uniq
Remove duplicate adjacent lines.

```bash
cat /local/file.txt | sort | uniq
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
cat /local/file.txt | rev
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
cat /local/data.csv | cut -f 2,4 -d ',' | sort
```

### Path Utilities

#### basename PATH [SUFFIX]
Extract filename from path.

```bash
basename /local/path/to/file.txt             # file.txt
basename /local/path/to/file.txt .txt        # file

# In scripts
for file in /local/*.csv; do
    filename=$(basename $file .csv)
    echo "Processing: $filename"
done
```

#### dirname PATH
Extract directory from path.

```bash
dirname /local/path/to/file.txt              # /local/path/to
dirname /local/file.txt                      # /local
dirname file.txt                             # .

# In scripts
filepath=/local/data/file.txt
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
[ -f /local/file.txt ]       # File exists and is regular file
[ -d /local/mydir ]          # Directory exists
[ -e /local/path ]           # Path exists

# Example
if [ -f /local/config.json ]; then
    cat /local/config.json
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
if [ -f /local/input.txt -a -w /local/output.txt ]; then
    cat /local/input.txt > /local/output.txt
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
if [ ! -f /local/required.txt ]; then
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

### AGFS Management

#### plugins
List all mounted AGFS plugins.

```bash
plugins

# Output:
# Plugin: localfs
#   Mount Point: /local
#   Type: localfs
#   Description: Local file system plugin
#
# Plugin: s3fs
#   Mount Point: /s3fs
#   Type: s3fs
#   Description: S3 file system plugin
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
echo "[$LOG_DATE] Process started" >> /local/log.txt
```

#### help
Show help message.

```bash
help                 # Display comprehensive help
```

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
Create `/etc/llm.yaml` or `~/.llm.yaml`:

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
SOURCE_DIR=/local/data
BACKUP_DIR=/local/backup
TIMESTAMP=$(date "+%Y%m%d_%H%M%S")

# Create backup directory
mkdir /local/backup

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
BACKUP_ROOT=/local/backups
SOURCE_DIRS="/local/data /local/config /local/logs"
DATE=$(date "+%Y-%m-%d")
BACKUP_DIR=$BACKUP_ROOT/$DATE
ERROR_LOG=$BACKUP_DIR/errors.log

# Create backup directory
mkdir /local/backups
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
agfs:/> ls /local/te<Tab>    # Completes to "/local/test.txt"
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

agfs:/> if [ -f /local/file.txt ]; then
>   cat /local/file.txt
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

## Complex Examples

### Example 1: Log Analysis Pipeline

```bash
#!/usr/bin/env uv run agfs-shell

# Analyze application logs across multiple servers

LOG_DIR=/local/logs
OUTPUT_DIR=/local/analysis

# Create directories
mkdir /local/logs
mkdir /local/analysis

# Create sample log files for demonstration
for server in web1 web2 web3; do
    echo "Creating sample log for $server..."
    echo "INFO: Server $server started" > /local/logs/$server.log
    echo "ERROR: Connection failed" >> /local/logs/$server.log
    echo "CRITICAL: System failure" >> /local/logs/$server.log
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
    uniq -c | \
    sort -rn > $OUTPUT_DIR/unique_errors.txt

# Find critical errors
cat $LOG_DIR/*.log | \
    grep -iE "(critical|fatal)" > $OUTPUT_DIR/critical.txt

# Generate report
cat << EOF > $OUTPUT_DIR/report.txt
Log Analysis Report
===================
Generated: $(date)

$(cat $OUTPUT_DIR/summary.txt)

Top 10 Unique Errors:
$(head -n 10 $OUTPUT_DIR/unique_errors.txt)

Critical Errors: $(cat $OUTPUT_DIR/critical.txt | wc -l)
EOF

cat $OUTPUT_DIR/report.txt
```

### Example 2: Data Processing Pipeline

```bash
#!/usr/bin/env uv run agfs-shell

# Process CSV data and generate JSON reports

INPUT_DIR=/local/data
OUTPUT_DIR=/local/reports
TIMESTAMP=$(date "+%Y%m%d_%H%M%S")

# Create directories
mkdir /local/data
mkdir /local/reports
mkdir /local/temp

# Create sample CSV files
echo "name,value,category,score" > /local/data/data1.csv
echo "Alice,100,A,95" >> /local/data/data1.csv
echo "Bob,200,B,85" >> /local/data/data1.csv
echo "Charlie,150,A,90" >> /local/data/data1.csv

# Process each CSV file
for csv_file in $INPUT_DIR/*.csv; do
    filename=$(basename $csv_file .csv)
    echo "Processing $filename..."

    # Extract specific columns (name and score - columns 1 and 4)
    cat $csv_file | \
        tail -n +2 | \
        cut -f 1,4 -d ',' > /local/temp/extracted_${filename}.txt

    # Count lines
    line_count=$(cat /local/temp/extracted_${filename}.txt | wc -l)
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

SOURCE=/local/important
BACKUP_NAME=backup_$(date "+%Y%m%d")
BACKUP=/local/backups/$BACKUP_NAME
MANIFEST=$BACKUP/manifest.txt

# Create backup directories
mkdir /local/backups
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
CONFIG_DIR=/local/config
DEPLOY_DIR=/local/deployed

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
$(plugins | grep "Mount Point")

Status: SUCCESS
EOF

# Deploy to all relevant filesystems
for mount in /local /s3fs /sqlfs; do
    if [ -d $mount ]; then
        echo "Deploying to $mount..."
        cp $CONFIG_FILE $mount/config/current.env 2>/dev/null

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

### Project Structure

```
agfs-shell/
├── agfs_shell/
│   ├── __init__.py          # Package initialization
│   ├── streams.py           # Stream classes (InputStream, OutputStream, ErrorStream)
│   ├── process.py           # Process class for command execution
│   ├── pipeline.py          # Pipeline class for chaining processes
│   ├── parser.py            # Command line parser
│   ├── builtins.py          # Built-in command implementations
│   ├── filesystem.py        # AGFS filesystem abstraction
│   ├── config.py            # Configuration management
│   ├── shell.py             # Shell with REPL and control flow
│   ├── completer.py         # Tab completion
│   ├── cli.py               # CLI entry point
│   ├── exit_codes.py        # Exit code constants
│   └── command_decorators.py # Command metadata
├── pyproject.toml           # Project configuration
├── README.md                # This file
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
- 39+ built-in commands
- Script execution (`.as` files)
- AI integration (`llm` command)

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
./test_functions.as           # Basic function tests
./test_functions_advanced.as  # Advanced function tests
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
agfs:/> echo "test" > /local/test.txt
agfs:/> cat /local/test.txt

# Test functions
agfs:/> add() { echo $((${1} + ${2})); }
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

## Contributing

This is an experimental/educational project. Contributions welcome!

1. Fork the repository
2. Create your feature branch
3. Add tests for new features
4. Submit a pull request

## License

[Add your license here]

## Credits

Built with:
- [pyagfs](https://github.com/c4pt0r/pyagfs) - Python client for AGFS
- [Rich](https://github.com/Textualize/rich) - Terminal formatting
- Pure Python - No external dependencies for core shell

---

**Note**: This is an experimental shell for educational purposes and AGFS integration. Not recommended for production use.
