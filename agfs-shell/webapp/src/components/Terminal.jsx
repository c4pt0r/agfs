import React, { useEffect, useRef, useState } from 'react';
import { Terminal as XTerm } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import '@xterm/xterm/css/xterm.css';

const Terminal = ({ wsRef }) => {
  const terminalRef = useRef(null);
  const xtermRef = useRef(null);
  const fitAddonRef = useRef(null);
  const currentLineRef = useRef('');
  const commandHistoryRef = useRef([]);
  const historyIndexRef = useRef(-1);
  const completionsRef = useRef([]);
  const completionIndexRef = useRef(0);
  const lastCompletionTextRef = useRef('');
  const pendingCompletionRef = useRef(false);
  const completionLineRef = useRef('');

  useEffect(() => {
    if (!terminalRef.current) return;

    // Initialize xterm
    const term = new XTerm({
      cursorBlink: true,
      fontSize: 14,
      fontFamily: 'Menlo, Monaco, "Courier New", monospace',
      theme: {
        background: '#1e1e1e',
        foreground: '#cccccc',
        cursor: '#ffffff',
        selection: '#264f78',
        black: '#000000',
        red: '#cd3131',
        green: '#0dbc79',
        yellow: '#e5e510',
        blue: '#2472c8',
        magenta: '#bc3fbc',
        cyan: '#11a8cd',
        white: '#e5e5e5',
        brightBlack: '#666666',
        brightRed: '#f14c4c',
        brightGreen: '#23d18b',
        brightYellow: '#f5f543',
        brightBlue: '#3b8eea',
        brightMagenta: '#d670d6',
        brightCyan: '#29b8db',
        brightWhite: '#ffffff',
      },
      allowProposedApi: true,
    });

    const fitAddon = new FitAddon();
    term.loadAddon(fitAddon);
    term.open(terminalRef.current);
    fitAddon.fit();

    xtermRef.current = term;
    fitAddonRef.current = fitAddon;

    // WebSocket connection for terminal
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsUrl = `${protocol}//${window.location.host}/ws/terminal`;
    const ws = new WebSocket(wsUrl);

    // Store in provided ref so FileTree can use it too
    if (wsRef) {
      wsRef.current = ws;
    }

    ws.onopen = () => {
      console.log('WebSocket connected');
    };

    ws.onmessage = (event) => {
      // Try to parse as JSON first (for completion responses and other structured data)
      try {
        const data = JSON.parse(event.data);

        // Handle completions
        if (data.type === 'completions') {
          // Only process if still pending and line hasn't changed
          if (!pendingCompletionRef.current || currentLineRef.current !== completionLineRef.current) {
            // User has already typed more, ignore stale completions
            pendingCompletionRef.current = false;
            return;
          }

          pendingCompletionRef.current = false;

          // Handle completion response
          const completions = data.completions || [];
          completionsRef.current = completions;

          if (completions.length === 0) {
            // No completions, do nothing
          } else if (completions.length === 1) {
            // Single completion - auto complete
            const completion = completions[0];
            const currentLine = currentLineRef.current;

            // Find the last space to replace from there
            const lastSpaceIndex = currentLine.lastIndexOf(' ');
            let newLine;
            if (lastSpaceIndex >= 0) {
              // Replace text after last space
              newLine = currentLine.substring(0, lastSpaceIndex + 1) + completion;
            } else {
              // Replace entire line
              newLine = completion;
            }

            // Clear current line and write new one
            term.write('\r\x1b[K$ ' + newLine);
            currentLineRef.current = newLine;
          } else {
            // Multiple completions - show them
            term.write('\r\n');
            const maxPerLine = 3;
            for (let i = 0; i < completions.length; i += maxPerLine) {
              const slice = completions.slice(i, i + maxPerLine);
              term.write(slice.join('  ') + '\r\n');
            }
            term.write('$ ' + currentLineRef.current);
            completionIndexRef.current = 0;
          }
          return;
        }

        // Ignore explorer messages (handled by FileTree component)
        if (data.type === 'explorer') {
          return;
        }

        // Ignore other JSON messages that are not for terminal display
        return;
      } catch (e) {
        // Not JSON, treat as regular output
      }

      // Write server output directly to terminal
      term.write(event.data);
    };

    ws.onerror = (error) => {
      console.error('WebSocket error:', error);
      term.write('\r\n\x1b[31mWebSocket connection error\x1b[0m\r\n');
    };

    ws.onclose = () => {
      console.log('WebSocket closed');
      term.write('\r\n\x1b[33mConnection closed. Please refresh the page.\x1b[0m\r\n');
    };

    // Handle terminal input
    // Note: currentLine is kept in currentLineRef, which is shared between onData and onmessage
    term.onData((data) => {
      const code = data.charCodeAt(0);
      let currentLine = currentLineRef.current || '';

      // Handle Enter key
      if (code === 13) {
        term.write('\r\n');

        if (currentLine.trim()) {
          // Add to history
          commandHistoryRef.current.push(currentLine);
          historyIndexRef.current = commandHistoryRef.current.length;

          // Send command to server via WebSocket
          if (ws.readyState === WebSocket.OPEN) {
            ws.send(JSON.stringify({
              type: 'command',
              data: currentLine
            }));
          } else {
            term.write('\x1b[31mNot connected to server\x1b[0m\r\n$ ');
          }

          currentLine = '';
          currentLineRef.current = '';
        } else {
          // Empty line, send to server to get new prompt
          if (ws.readyState === WebSocket.OPEN) {
            ws.send(JSON.stringify({
              type: 'command',
              data: ''
            }));
          }
        }
      }
      // Handle Backspace
      else if (code === 127) {
        if (currentLine.length > 0) {
          currentLine = currentLine.slice(0, -1);
          currentLineRef.current = currentLine;
          term.write('\b \b');
        }
      }
      // Handle Ctrl+C
      else if (code === 3) {
        term.write('^C\r\n$ ');
        currentLine = '';
        currentLineRef.current = '';
      }
      // Handle Ctrl+L (clear screen)
      else if (code === 12) {
        term.clear();
        term.write('$ ' + currentLine);
      }
      // Handle Ctrl+U (clear line)
      else if (code === 21) {
        // Clear current line
        const lineLength = currentLine.length;
        term.write('\r$ ');
        term.write(' '.repeat(lineLength));
        term.write('\r$ ');
        currentLine = '';
        currentLineRef.current = '';
      }
      // Handle arrow up (previous command in history)
      else if (data === '\x1b[A') {
        if (commandHistoryRef.current.length > 0 && historyIndexRef.current > 0) {
          // Clear current line
          term.write('\r\x1b[K$ ');

          // Go back in history
          historyIndexRef.current--;
          currentLine = commandHistoryRef.current[historyIndexRef.current];
          currentLineRef.current = currentLine;

          // Write the command
          term.write(currentLine);
        }
      }
      // Handle arrow down (next command in history)
      else if (data === '\x1b[B') {
        // Clear current line
        term.write('\r\x1b[K$ ');

        if (historyIndexRef.current < commandHistoryRef.current.length - 1) {
          // Go forward in history
          historyIndexRef.current++;
          currentLine = commandHistoryRef.current[historyIndexRef.current];
        } else {
          // At the end of history, clear line
          historyIndexRef.current = commandHistoryRef.current.length;
          currentLine = '';
        }

        currentLineRef.current = currentLine;
        term.write(currentLine);
      }
      // Handle Ctrl+A (go to beginning of line)
      else if (code === 1) {
        term.write('\r$ ');
      }
      // Handle Ctrl+E (go to end of line)
      else if (code === 5) {
        term.write('\r$ ' + currentLine);
      }
      // Handle Ctrl+W (delete word before cursor)
      else if (code === 23) {
        if (currentLine.length > 0) {
          // Find the last word boundary (space)
          let newLine = currentLine.trimEnd();
          const lastSpaceIndex = newLine.lastIndexOf(' ');

          if (lastSpaceIndex >= 0) {
            // Delete from last space to end
            newLine = newLine.substring(0, lastSpaceIndex + 1);
          } else {
            // No space found, delete entire line
            newLine = '';
          }

          // Clear line and rewrite
          term.write('\r\x1b[K$ ' + newLine);
          currentLine = newLine;
          currentLineRef.current = newLine;
        }
      }
      // Handle Tab (autocomplete)
      else if (code === 9) {
        if (ws.readyState === WebSocket.OPEN) {
          // Mark as pending completion and save current line
          pendingCompletionRef.current = true;
          completionLineRef.current = currentLine;

          // Extract the word being completed
          // Find the last space or start of line
          const beforeCursor = currentLine;
          const lastSpaceIndex = beforeCursor.lastIndexOf(' ');
          const text = lastSpaceIndex >= 0 ? beforeCursor.substring(lastSpaceIndex + 1) : beforeCursor;

          // Send completion request
          ws.send(JSON.stringify({
            type: 'complete',
            text: text,
            line: currentLine,
            cursor_pos: currentLine.length
          }));
        }
      }
      // Handle arrow left/right (for now, ignore)
      else if (data === '\x1b[C' || data === '\x1b[D') {
        // Ignore arrow left/right for simplicity
      }
      // Handle regular characters
      else if (code >= 32 && code < 127) {
        currentLine += data;
        currentLineRef.current = currentLine;
        term.write(data);
      }
    });

    // Handle window resize
    const handleResize = () => {
      fitAddon.fit();

      // Send resize event to server
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({
          type: 'resize',
          data: {
            cols: term.cols,
            rows: term.rows
          }
        }));
      }
    };

    window.addEventListener('resize', handleResize);

    // Prevent Ctrl+W from closing the browser tab
    // Use capture phase and window-level listener for reliability
    const handleKeyDown = (e) => {
      // Check for Ctrl+W (or Cmd+W on Mac)
      if ((e.ctrlKey || e.metaKey) && e.key === 'w') {
        e.preventDefault();
        e.stopPropagation();
      }
    };

    // Add keydown listener to window with capture phase
    window.addEventListener('keydown', handleKeyDown, true);

    // Cleanup
    return () => {
      window.removeEventListener('resize', handleResize);
      window.removeEventListener('keydown', handleKeyDown, true);
      if (ws.readyState === WebSocket.OPEN) {
        ws.close();
      }
      term.dispose();
    };
  }, []);

  return (
    <>
      <div className="terminal-header">
        <span>TERMINAL</span>
      </div>
      <div className="terminal-wrapper" ref={terminalRef}></div>
    </>
  );
};

export default Terminal;
