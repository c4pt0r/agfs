"""Web application server for agfs-shell"""

import asyncio
import json
import os
import sys
import io
from pathlib import Path
from typing import Optional

try:
    from aiohttp import web
    import aiohttp_cors
    AIOHTTP_AVAILABLE = True
except ImportError:
    AIOHTTP_AVAILABLE = False


class ShellSession:
    """A shell session for a WebSocket connection"""

    def __init__(self, shell, ws):
        self.shell = shell
        self.ws = ws
        self.buffer = ""
        # Initialize completer
        from .completer import ShellCompleter
        self.completer = ShellCompleter(self.shell.filesystem)
        self.completer.shell = self.shell

    async def send(self, data: str):
        """Send data to the WebSocket"""
        if self.ws and not self.ws.closed:
            await self.ws.send_str(data)

    def get_completions(self, text: str, line: str, cursor_pos: int) -> list:
        """Get completion suggestions for the given text

        Args:
            text: The word being completed
            line: The full command line
            cursor_pos: Cursor position in the line

        Returns:
            List of completion suggestions
        """
        # Determine if we're completing a command or a path
        before_cursor = line[:cursor_pos]

        # Check if we're at the beginning (completing command)
        if not before_cursor.strip() or before_cursor.strip() == text:
            # Complete command names
            return self.completer._complete_command(text)
        else:
            # Complete paths
            return self.completer._complete_path(text)

    async def handle_command(self, command: str):
        """Execute a command and send output to WebSocket"""
        # Create a wrapper that has both text and binary interfaces
        class BufferedTextIO:
            def __init__(self):
                self.text_buffer = io.StringIO()
                self.byte_buffer = io.BytesIO()
                # Create buffer attribute for binary writes
                self.buffer = self

            def write(self, data):
                if isinstance(data, bytes):
                    self.byte_buffer.write(data)
                else:
                    self.text_buffer.write(data)
                return len(data)

            def flush(self):
                pass

            def getvalue(self):
                text = self.text_buffer.getvalue()
                binary = self.byte_buffer.getvalue()
                if binary:
                    try:
                        text += binary.decode('utf-8', errors='replace')
                    except:
                        pass
                return text

        # Capture stdout and stderr
        old_stdout = sys.stdout
        old_stderr = sys.stderr
        stdout_buffer = BufferedTextIO()
        stderr_buffer = BufferedTextIO()

        sys.stdout = stdout_buffer
        sys.stderr = stderr_buffer

        try:
            # Execute the command through shell
            exit_code = self.shell.execute(command)

            # Get output
            stdout = stdout_buffer.getvalue()
            stderr = stderr_buffer.getvalue()

            # Send output to terminal (convert \n to \r\n for terminal)
            if stdout:
                stdout_formatted = stdout.replace('\n', '\r\n')
                await self.send(stdout_formatted)
            if stderr:
                # Send stderr in red color (convert \n to \r\n)
                stderr_formatted = stderr.replace('\n', '\r\n')
                await self.send(f'\x1b[31m{stderr_formatted}\x1b[0m')

            return exit_code

        except Exception as e:
            # Send error in red
            await self.send(f'\x1b[31mError: {str(e)}\x1b[0m\r\n')
            return 1
        finally:
            sys.stdout = old_stdout
            sys.stderr = old_stderr


class WebAppServer:
    """HTTP server for the web application"""

    def __init__(self, shell, host='localhost', port=3000):
        if not AIOHTTP_AVAILABLE:
            raise ImportError(
                "aiohttp is required for web app server. "
                "Install with: uv sync --extra webapp"
            )

        self.shell = shell
        self.host = host
        self.port = port
        self.app = None
        self.runner = None
        self.sessions = {}  # WebSocket sessions

    async def handle_explorer(self, request):
        """Get directory structure for Explorer (optimized API)"""
        path = request.query.get('path', '/')

        try:
            # Use filesystem API directly for better performance
            entries = self.shell.filesystem.list_directory(path)

            # Format entries for frontend
            files = []
            for entry in entries:
                name = entry.get('name', '')
                if name and name not in ['.', '..']:
                    # AGFS API returns 'isDir' instead of 'type'
                    is_dir = entry.get('isDir', False)
                    file_type = 'directory' if is_dir else 'file'

                    files.append({
                        'name': name,
                        'path': f"{path.rstrip('/')}/{name}" if path != '/' else f"/{name}",
                        'type': file_type,
                        'size': entry.get('size', 0),
                        'mtime': entry.get('mtime', ''),
                    })

            # Sort: directories first, then by name
            files.sort(key=lambda x: (x['type'] != 'directory', x['name'].lower()))

            return web.json_response({
                'path': path,
                'files': files
            })

        except Exception as e:
            return web.json_response(
                {'error': str(e), 'path': path},
                status=500
            )

    async def handle_list_files(self, request):
        """List files in a directory (legacy, kept for compatibility)"""
        path = request.query.get('path', '/')

        try:
            # Use filesystem API directly
            entries = self.shell.filesystem.list_directory(path)

            files = []
            for entry in entries:
                name = entry.get('name', '')
                if name and name not in ['.', '..']:
                    # AGFS API returns 'isDir' instead of 'type'
                    is_dir = entry.get('isDir', False)
                    file_type = 'directory' if is_dir else 'file'

                    files.append({
                        'name': name,
                        'type': file_type
                    })

            return web.json_response({'files': files})

        except Exception as e:
            return web.json_response(
                {'error': str(e)},
                status=500
            )

    async def handle_read_file(self, request):
        """Read file contents"""
        path = request.query.get('path', '')

        if not path:
            return web.json_response(
                {'error': 'Path is required'},
                status=400
            )

        try:
            # Use BufferedTextIO to handle both text and binary output
            class BufferedTextIO:
                def __init__(self):
                    self.text_buffer = io.StringIO()
                    self.byte_buffer = io.BytesIO()
                    self.buffer = self

                def write(self, data):
                    if isinstance(data, bytes):
                        self.byte_buffer.write(data)
                    else:
                        self.text_buffer.write(data)
                    return len(data)

                def flush(self):
                    pass

                def getvalue(self):
                    text = self.text_buffer.getvalue()
                    binary = self.byte_buffer.getvalue()
                    if binary:
                        try:
                            text += binary.decode('utf-8', errors='replace')
                        except:
                            pass
                    return text

            # Capture output
            old_stdout = sys.stdout
            old_stderr = sys.stderr
            stdout_buffer = BufferedTextIO()
            stderr_buffer = BufferedTextIO()

            sys.stdout = stdout_buffer
            sys.stderr = stderr_buffer

            try:
                self.shell.execute(f'cat {path}')
                content = stdout_buffer.getvalue()
            finally:
                sys.stdout = old_stdout
                sys.stderr = old_stderr

            return web.json_response({'content': content})

        except Exception as e:
            return web.json_response(
                {'error': str(e)},
                status=500
            )

    async def handle_write_file(self, request):
        """Write file contents"""
        try:
            data = await request.json()
            path = data.get('path', '')
            content = data.get('content', '')

            if not path:
                return web.json_response(
                    {'error': 'Path is required'},
                    status=400
                )

            # Write file using filesystem API directly
            try:
                # Convert content to bytes
                content_bytes = content.encode('utf-8')

                # Write to filesystem
                self.shell.filesystem.write_file(path, content_bytes)

                return web.json_response({'success': True})
            except Exception as e:
                return web.json_response(
                    {'error': f'Failed to write file: {str(e)}'},
                    status=500
                )

        except Exception as e:
            return web.json_response(
                {'error': str(e)},
                status=500
            )

    async def handle_download_file(self, request):
        """Download file contents (for binary/non-text files)"""
        path = request.query.get('path', '')

        if not path:
            return web.json_response(
                {'error': 'Path is required'},
                status=400
            )

        try:
            # Read file using filesystem API
            content = self.shell.filesystem.read_file(path)

            # Get filename from path
            filename = path.split('/')[-1]

            # Determine content type based on extension
            import mimetypes
            content_type, _ = mimetypes.guess_type(filename)
            if content_type is None:
                content_type = 'application/octet-stream'

            # Return file with download headers
            return web.Response(
                body=content,
                headers={
                    'Content-Type': content_type,
                    'Content-Disposition': f'attachment; filename="{filename}"'
                }
            )

        except Exception as e:
            return web.json_response(
                {'error': str(e)},
                status=500
            )

    async def handle_copy_file(self, request):
        """Copy file from source to target"""
        try:
            data = await request.json()
            source_path = data.get('sourcePath', '')
            target_path = data.get('targetPath', '')

            if not source_path or not target_path:
                return web.json_response(
                    {'error': 'Source and target paths are required'},
                    status=400
                )

            # Read source file
            content = self.shell.filesystem.read_file(source_path)

            # Write to target
            self.shell.filesystem.write_file(target_path, content)

            return web.json_response({'success': True})

        except Exception as e:
            return web.json_response(
                {'error': str(e)},
                status=500
            )

    async def handle_delete_file(self, request):
        """Delete a file or directory"""
        try:
            data = await request.json()
            path = data.get('path', '')

            if not path:
                return web.json_response(
                    {'error': 'Path is required'},
                    status=400
                )

            # Delete using filesystem API
            self.shell.filesystem.delete_file(path)

            return web.json_response({'success': True})

        except Exception as e:
            return web.json_response(
                {'error': str(e)},
                status=500
            )

    async def handle_upload_file(self, request):
        """Upload a file to the filesystem"""
        try:
            reader = await request.multipart()

            directory = '/'
            file_data = None
            filename = None

            # Read multipart data
            async for field in reader:
                if field.name == 'directory':
                    directory = await field.text()
                elif field.name == 'file':
                    filename = field.filename
                    file_data = await field.read()

            if not file_data or not filename:
                return web.json_response(
                    {'error': 'No file provided'},
                    status=400
                )

            # Construct target path
            target_path = f"{directory.rstrip('/')}/{filename}" if directory != '/' else f"/{filename}"

            # Write file to filesystem
            self.shell.filesystem.write_file(target_path, file_data)

            return web.json_response({
                'success': True,
                'path': target_path
            })

        except Exception as e:
            return web.json_response(
                {'error': str(e)},
                status=500
            )

    async def handle_websocket(self, request):
        """Handle WebSocket connection for terminal"""
        ws = web.WebSocketResponse()
        await ws.prepare(request)

        # Create a new shell session for this WebSocket
        session = ShellSession(self.shell, ws)
        session_id = id(ws)
        self.sessions[session_id] = session

        try:
            # Send welcome message
            from . import __version__
            await session.send(f'\x1b[32magfs-shell v{__version__} ready\x1b[0m\r\n')
            await session.send(f'\x1b[90mConnected to {self.shell.server_url}\x1b[0m\r\n')
            await session.send('$ ')

            # Handle incoming messages
            async for msg in ws:
                if msg.type == web.WSMsgType.TEXT:
                    try:
                        data = json.loads(msg.data)
                        msg_type = data.get('type')

                        if msg_type == 'command':
                            command = data.get('data', '')

                            if command.strip():
                                # Execute command
                                exit_code = await session.handle_command(command)

                                # Send new prompt
                                await session.send('$ ')
                            else:
                                # Empty command, just show prompt
                                await session.send('$ ')

                        elif msg_type == 'explorer':
                            # Get directory listing for Explorer
                            path = data.get('path', '/')

                            try:
                                entries = self.shell.filesystem.list_directory(path)

                                # Format entries
                                files = []
                                for entry in entries:
                                    name = entry.get('name', '')
                                    if name and name not in ['.', '..']:
                                        # AGFS API returns 'isDir' instead of 'type'
                                        is_dir = entry.get('isDir', False)
                                        file_type = 'directory' if is_dir else 'file'

                                        files.append({
                                            'name': name,
                                            'path': f"{path.rstrip('/')}/{name}" if path != '/' else f"/{name}",
                                            'type': file_type,
                                            'size': entry.get('size', 0),
                                            'mtime': entry.get('modTime', ''),
                                        })

                                # Sort: directories first, then by name
                                files.sort(key=lambda x: (x['type'] != 'directory', x['name'].lower()))

                                await ws.send_json({
                                    'type': 'explorer',
                                    'path': path,
                                    'files': files
                                })
                            except Exception as e:
                                await ws.send_json({
                                    'type': 'explorer',
                                    'path': path,
                                    'error': str(e),
                                    'files': []
                                })

                        elif msg_type == 'complete':
                            # Tab completion request
                            text = data.get('text', '')
                            line = data.get('line', '')
                            cursor_pos = data.get('cursor_pos', len(line))

                            try:
                                completions = session.get_completions(text, line, cursor_pos)
                                # Send completions back to client
                                await ws.send_json({
                                    'type': 'completions',
                                    'completions': completions
                                })
                            except Exception as e:
                                # Send empty completions on error
                                await ws.send_json({
                                    'type': 'completions',
                                    'completions': []
                                })

                        elif msg_type == 'resize':
                            # Terminal resize event (can be used for future enhancements)
                            pass

                    except json.JSONDecodeError:
                        # If not JSON, treat as raw command
                        await session.send('\x1b[31mInvalid message format\x1b[0m\r\n$ ')
                    except Exception as e:
                        await session.send(f'\x1b[31mError: {str(e)}\x1b[0m\r\n$ ')

                elif msg.type == web.WSMsgType.ERROR:
                    print(f'WebSocket error: {ws.exception()}')

        finally:
            # Clean up session
            if session_id in self.sessions:
                del self.sessions[session_id]

        return ws

    async def handle_static(self, request):
        """Serve static files"""
        # Serve the built React app
        webapp_dir = Path(__file__).parent.parent / 'webapp' / 'dist'

        path = request.match_info.get('path', 'index.html')
        if path == '':
            path = 'index.html'

        file_path = webapp_dir / path

        # Handle client-side routing - serve index.html for non-existent paths
        if not file_path.exists() or file_path.is_dir():
            file_path = webapp_dir / 'index.html'

        if file_path.exists() and file_path.is_file():
            return web.FileResponse(file_path)
        else:
            return web.Response(text='Not found', status=404)

    async def init_app(self):
        """Initialize the web application"""
        self.app = web.Application()

        # Setup CORS
        cors = aiohttp_cors.setup(self.app, defaults={
            "*": aiohttp_cors.ResourceOptions(
                allow_credentials=True,
                expose_headers="*",
                allow_headers="*",
            )
        })

        # API routes
        api_routes = [
            self.app.router.add_get('/api/files/list', self.handle_list_files),
            self.app.router.add_get('/api/files/read', self.handle_read_file),
            self.app.router.add_post('/api/files/write', self.handle_write_file),
            self.app.router.add_get('/api/files/download', self.handle_download_file),
            self.app.router.add_post('/api/files/copy', self.handle_copy_file),
            self.app.router.add_post('/api/files/delete', self.handle_delete_file),
            self.app.router.add_post('/api/files/upload', self.handle_upload_file),
        ]

        # WebSocket route (no CORS needed)
        self.app.router.add_get('/ws/terminal', self.handle_websocket)

        # Static files (serve React app)
        self.app.router.add_get('/', self.handle_static)
        self.app.router.add_get('/{path:.*}', self.handle_static)

        # Configure CORS for API routes only
        for route in api_routes:
            cors.add(route)

    async def start(self):
        """Start the web server"""
        await self.init_app()

        self.runner = web.AppRunner(self.app)
        await self.runner.setup()

        site = web.TCPSite(self.runner, self.host, self.port)
        await site.start()

        print(f'\n\x1b[32mWeb app server running at http://{self.host}:{self.port}\x1b[0m\n')

    async def stop(self):
        """Stop the web server"""
        # Close all WebSocket connections
        for session in list(self.sessions.values()):
            if session.ws and not session.ws.closed:
                await session.ws.close()

        if self.runner:
            await self.runner.cleanup()


def run_server(shell, host='localhost', port=3000):
    """Run the web app server"""
    server = WebAppServer(shell, host, port)

    loop = asyncio.new_event_loop()
    asyncio.set_event_loop(loop)

    try:
        loop.run_until_complete(server.start())
        loop.run_forever()
    except KeyboardInterrupt:
        print('\n\x1b[33mShutting down...\x1b[0m')
    finally:
        loop.run_until_complete(server.stop())
        loop.close()
