# StreamRotateFS Plugin - Rotating Streaming File System

This plugin extends StreamFS with automatic file rotation support. Data is streamed to readers while being saved to rotating files on local filesystem.

## Features

- **All StreamFS features**: Multiple readers/writers, ring buffer, fanout
- **Time-based rotation**: Rotate files at specified intervals (e.g., every 5 minutes)
- **Size-based rotation**: Rotate files when reaching size threshold (e.g., 100MB)
- **Configurable output path**: Save to local directory
- **Customizable filename pattern**: Use variables for dynamic naming
- **Concurrent operation**: Rotation doesn't interrupt streaming

## Rotation Triggers

- **Time interval**: Files rotate after specified duration (`rotation_interval`)
- **File size**: Files rotate when reaching size threshold (`rotation_size`)
- Both can be enabled simultaneously (triggers on first condition met)

## Filename Pattern Variables

- `{channel}` - Channel/stream name
- `{timestamp}` - Unix timestamp (seconds)
- `{date}` - Date in YYYYMMDD format
- `{time}` - Time in HHMMSS format
- `{datetime}` - Date and time in YYYYMMDD_HHMMSS format
- `{index}` - Rotation file index (6-digit zero-padded)

## Usage Examples

### Write to rotating stream
```bash
cat video.mp4 | agfs write --stream /streamrotatefs/channel1
```

### Read from stream (live)
```bash
agfs cat --stream /streamrotatefs/channel1 | ffplay -
```

### List rotated files
```bash
agfs ls /s3fs/bucket/streams/
# OR
agfs ls /localfs/data/
```

## Configuration

```toml
[plugins.streamrotatefs]
enabled = true
path = "/streamrotatefs"

  [plugins.streamrotatefs.config]
  # Stream buffer settings (same as streamfs)
  channel_buffer_size = "6MB"
  ring_buffer_size = "6MB"

  # Rotation settings
  rotation_interval = "5m"              # Rotate every 5 minutes
  rotation_size = "100MB"               # Rotate at 100MB

  # Output path - must be an AGFS path
  output_path = "/s3fs/my-bucket/streams"  # Save to S3 via s3fs
  # OR
  # output_path = "/localfs/data"            # Save via localfs

  filename_pattern = "{channel}_{datetime}_{index}.dat"
```

### Output Path

- **Must be an AGFS path** (starts with `/`)
  - Example: `"/s3fs/bucket/path"` - Save to S3
  - Example: `"/localfs/data"` - Save via localfs plugin
  - Supports any mounted agfs filesystem
  - The target mount point must be already mounted and writable

## Configuration Examples

### Time-based rotation (every hour)
```toml
rotation_interval = "1h"
rotation_size = ""  # Disabled
```

### Size-based rotation (100MB chunks)
```toml
rotation_interval = ""  # Disabled
rotation_size = "100MB"
```

### Combined (whichever comes first)
```toml
rotation_interval = "10m"
rotation_size = "50MB"
```

## Filename Pattern Examples

```
{channel}_{timestamp}.dat
  → channel1_1702345678.dat

{date}/{channel}_{time}.mp4
  → 20231207/channel1_143058.mp4

{channel}/segment_{index}.ts
  → channel1/segment_000001.ts
```

## Important Notes

- **Output path must be an AGFS path** (e.g., `/s3fs/bucket` or `/localfs/data`)
- The target mount point must be already mounted and writable
- Parent directories will be created automatically if the filesystem supports it
- Stream continues uninterrupted during rotation
- Old rotation files are not automatically deleted
- Readers receive live data regardless of rotation
- File index increments with each rotation

## Dynamic Mounting

### Interactive shell - Default settings
```bash
agfs:/> mount streamrotatefs /rotate output_path=/localfs/rotated
```

### Interactive shell - Custom settings
```bash
agfs:/> mount streamrotatefs /rotate rotation_interval=5m rotation_size=100MB output_path=/s3fs/data
```

### Direct command
```bash
uv run agfs mount streamrotatefs /rotate rotation_size=50MB output_path=/s3fs/output
```

## License

Apache License 2.0
