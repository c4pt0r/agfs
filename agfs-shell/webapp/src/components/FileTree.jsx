import React, { useState, useEffect } from 'react';
import ContextMenu from './ContextMenu';

const FileTreeItem = ({ item, depth, onSelect, selectedFile, onToggle, expanded, expandedDirs, onContextMenu }) => {
  const isDirectory = item.type === 'directory';
  const isSelected = selectedFile && selectedFile.path === item.path;

  const handleClick = () => {
    if (isDirectory) {
      onToggle(item.path);
    }
    onSelect(item);
  };

  const handleContextMenu = (e) => {
    e.preventDefault();
    onContextMenu(e, item);
  };

  return (
    <>
      <div
        className={`file-tree-item ${isDirectory ? 'directory' : ''} ${isSelected ? 'selected' : ''}`}
        style={{ '--depth': depth }}
        onClick={handleClick}
        onContextMenu={handleContextMenu}
      >
        {isDirectory && (
          <span className={`expand-icon ${expanded ? 'expanded' : ''}`}>
            ▶
          </span>
        )}
        {!isDirectory && <span style={{ width: '16px' }}></span>}
        <span className="file-icon">
          {isDirectory ? '📁' : '📄'}
        </span>
        <span>{item.name}</span>
      </div>
      {isDirectory && expanded && item.children && (
        item.children.map((child, index) => (
          <FileTreeItem
            key={child.path}
            item={child}
            depth={depth + 1}
            onSelect={onSelect}
            selectedFile={selectedFile}
            onToggle={onToggle}
            expanded={expandedDirs[child.path]}
            expandedDirs={expandedDirs}
            onContextMenu={onContextMenu}
          />
        ))
      )}
    </>
  );
};

const FileTree = ({ currentPath, onFileSelect, selectedFile, wsRef }) => {
  const [tree, setTree] = useState([]);
  const [loading, setLoading] = useState(true);
  const [expandedDirs, setExpandedDirs] = useState({ '/': true });
  const [pendingRequests, setPendingRequests] = useState(new Map());
  const [contextMenu, setContextMenu] = useState(null);
  const [copiedItem, setCopiedItem] = useState(null);

  const loadDirectory = (path) => {
    return new Promise((resolve, reject) => {
      const ws = wsRef?.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        // Fallback to HTTP if WebSocket not available
        fetch(`/api/files/list?path=${encodeURIComponent(path)}`)
          .then(res => res.json())
          .then(data => resolve(data.files || []))
          .catch(reject);
        return;
      }

      // Use WebSocket
      const requestId = `${path}-${Date.now()}`;
      setPendingRequests(prev => new Map(prev).set(requestId, { resolve, reject, path }));

      ws.send(JSON.stringify({
        type: 'explorer',
        path: path,
        requestId: requestId
      }));

      // Timeout after 5 seconds
      setTimeout(() => {
        setPendingRequests(prev => {
          const newMap = new Map(prev);
          if (newMap.has(requestId)) {
            newMap.delete(requestId);
            reject(new Error('Request timeout'));
          }
          return newMap;
        });
      }, 5000);
    });
  };

  // Handle WebSocket messages for explorer
  useEffect(() => {
    const ws = wsRef?.current;
    if (!ws) return;

    const handleMessage = (event) => {
      try {
        const data = JSON.parse(event.data);
        if (data.type === 'explorer') {
          // Find matching pending request
          setPendingRequests(prev => {
            const newMap = new Map(prev);
            for (const [requestId, request] of newMap) {
              if (request.path === data.path) {
                newMap.delete(requestId);
                if (data.error) {
                  request.reject(new Error(data.error));
                } else {
                  request.resolve(data.files || []);
                }
                break;
              }
            }
            return newMap;
          });
        }
      } catch (e) {
        // Not a JSON message or not for us
      }
    };

    ws.addEventListener('message', handleMessage);
    return () => ws.removeEventListener('message', handleMessage);
  }, [wsRef, pendingRequests]);

  const buildTree = async (path, depth = 0) => {
    // Load directory contents
    const items = await loadDirectory(path);
    const result = [];

    for (const item of items) {
      // WebSocket API already provides full path
      const fullPath = item.path || (path === '/' ? `/${item.name}` : `${path}/${item.name}`);
      const treeItem = {
        name: item.name,
        path: fullPath,
        type: item.type,
        size: item.size,
        mtime: item.mtime,
      };

      // Recursively load children if directory is expanded
      if (item.type === 'directory' && expandedDirs[fullPath]) {
        treeItem.children = await buildTree(fullPath, depth + 1);
      }

      result.push(treeItem);
    }

    return result.sort((a, b) => {
      if (a.type === b.type) return a.name.localeCompare(b.name);
      return a.type === 'directory' ? -1 : 1;
    });
  };

  const handleToggle = async (path) => {
    const newExpanded = { ...expandedDirs };
    newExpanded[path] = !newExpanded[path];
    setExpandedDirs(newExpanded);
  };

  const handleContextMenu = (e, item) => {
    setContextMenu({
      x: e.clientX,
      y: e.clientY,
      item: item
    });
  };

  const handleCopy = () => {
    setCopiedItem(contextMenu.item);
  };

  const handlePaste = async () => {
    if (!copiedItem || !contextMenu.item) return;

    const targetDir = contextMenu.item.type === 'directory'
      ? contextMenu.item.path
      : contextMenu.item.path.substring(0, contextMenu.item.path.lastIndexOf('/')) || '/';

    const fileName = copiedItem.path.split('/').pop();
    const targetPath = targetDir === '/' ? `/${fileName}` : `${targetDir}/${fileName}`;

    try {
      const response = await fetch('/api/files/copy', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          sourcePath: copiedItem.path,
          targetPath: targetPath
        })
      });

      if (response.ok) {
        // Refresh tree
        setExpandedDirs({ ...expandedDirs });
      } else {
        const data = await response.json();
        alert(`Failed to copy: ${data.error}`);
      }
    } catch (error) {
      alert(`Failed to copy: ${error.message}`);
    }
  };

  const handleDownload = () => {
    if (!contextMenu.item) return;
    const downloadUrl = `/api/files/download?path=${encodeURIComponent(contextMenu.item.path)}`;
    const link = document.createElement('a');
    link.href = downloadUrl;
    link.download = contextMenu.item.name;
    document.body.appendChild(link);
    link.click();
    document.body.removeChild(link);
  };

  const handleDelete = async () => {
    if (!contextMenu.item) return;

    if (!confirm(`Are you sure you want to delete "${contextMenu.item.name}"?`)) {
      return;
    }

    try {
      const response = await fetch('/api/files/delete', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path: contextMenu.item.path })
      });

      if (response.ok) {
        // Refresh tree
        setExpandedDirs({ ...expandedDirs });
      } else {
        const data = await response.json();
        alert(`Failed to delete: ${data.error}`);
      }
    } catch (error) {
      alert(`Failed to delete: ${error.message}`);
    }
  };

  useEffect(() => {
    const loadTree = async () => {
      setLoading(true);
      const data = await buildTree(currentPath);
      setTree(data);
      setLoading(false);
    };
    loadTree();
  }, [currentPath, expandedDirs]);

  if (loading) {
    return <div className="loading">Loading...</div>;
  }

  const menuItems = contextMenu ? [
    {
      icon: '📋',
      label: 'Copy',
      onClick: handleCopy
    },
    {
      icon: '📄',
      label: 'Paste',
      onClick: handlePaste,
      disabled: !copiedItem
    },
    { separator: true },
    {
      icon: '⬇️',
      label: 'Download',
      onClick: handleDownload,
      disabled: contextMenu.item.type === 'directory'
    },
    {
      icon: '🗑️',
      label: 'Delete',
      onClick: handleDelete
    }
  ] : [];

  return (
    <div className="file-tree">
      {tree.map((item, index) => (
        <FileTreeItem
          key={item.path}
          item={item}
          depth={0}
          onSelect={onFileSelect}
          selectedFile={selectedFile}
          onToggle={handleToggle}
          expanded={expandedDirs[item.path]}
          expandedDirs={expandedDirs}
          onContextMenu={handleContextMenu}
        />
      ))}
      {contextMenu && (
        <ContextMenu
          x={contextMenu.x}
          y={contextMenu.y}
          items={menuItems}
          onClose={() => setContextMenu(null)}
        />
      )}
    </div>
  );
};

export default FileTree;
