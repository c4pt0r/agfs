import React, { useState, useRef } from 'react';

const MenuBar = ({ onNewFile, onSave, onUpload, currentFile, currentDirectory, hasUnsavedChanges }) => {
  const [showNewFileDialog, setShowNewFileDialog] = useState(false);
  const [newFilePath, setNewFilePath] = useState('');
  const fileInputRef = useRef(null);

  const handleNewFile = () => {
    setShowNewFileDialog(true);
    // Set default path to current directory
    const defaultPath = currentDirectory === '/' ? '/' : `${currentDirectory}/`;
    setNewFilePath(defaultPath);
  };

  const handleCreateFile = async () => {
    if (newFilePath.trim()) {
      await onNewFile(newFilePath.trim());
      setShowNewFileDialog(false);
      setNewFilePath('');
    }
  };

  const handleCancel = () => {
    setShowNewFileDialog(false);
    setNewFilePath('');
  };

  const handleKeyDown = (e) => {
    if (e.key === 'Enter') {
      handleCreateFile();
    } else if (e.key === 'Escape') {
      handleCancel();
    }
  };

  const isSaveDisabled = !currentFile || !hasUnsavedChanges;
  const saveLabel = hasUnsavedChanges ? 'Save' : 'Saved';

  const handleDownload = () => {
    if (!currentFile) return;
    const downloadUrl = `/api/files/download?path=${encodeURIComponent(currentFile.path)}`;
    const link = document.createElement('a');
    link.href = downloadUrl;
    link.download = currentFile.name;
    document.body.appendChild(link);
    link.click();
    document.body.removeChild(link);
  };

  const handleUploadClick = () => {
    fileInputRef.current?.click();
  };

  const handleFileChange = (e) => {
    const files = Array.from(e.target.files || []);
    if (files.length > 0) {
      onUpload(files);
    }
    // Reset input so same file can be uploaded again
    e.target.value = '';
  };

  return (
    <>
      <div className="menu-bar">
        <div className="menu-items">
          <div className="menu-item" onClick={handleNewFile}>
            <span className="menu-icon">📄</span>
            <span>New File</span>
          </div>
          <div
            className={`menu-item ${isSaveDisabled ? 'disabled' : ''}`}
            onClick={!isSaveDisabled ? onSave : null}
          >
            <span className="menu-icon">{hasUnsavedChanges ? '💾' : '✓'}</span>
            <span>{saveLabel}</span>
            <span className="menu-shortcut">Ctrl+S</span>
          </div>
          <div
            className={`menu-item ${!currentFile ? 'disabled' : ''}`}
            onClick={currentFile ? handleDownload : null}
          >
            <span className="menu-icon">⬇️</span>
            <span>Download</span>
          </div>
          <div className="menu-item" onClick={handleUploadClick}>
            <span className="menu-icon">⬆️</span>
            <span>Upload</span>
          </div>
        </div>
        <div className="menu-info">
          <span className="menu-info-item">📁 {currentDirectory}</span>
          {currentFile && (
            <span className="menu-info-item">📝 {currentFile.name}</span>
          )}
        </div>
      </div>
      <input
        ref={fileInputRef}
        type="file"
        multiple
        style={{ display: 'none' }}
        onChange={handleFileChange}
      />

      {showNewFileDialog && (
        <div className="dialog-overlay" onClick={handleCancel}>
          <div className="dialog" onClick={(e) => e.stopPropagation()}>
            <div className="dialog-header">Create New File</div>
            <div className="dialog-body">
              <label>File Path:</label>
              <input
                type="text"
                value={newFilePath}
                onChange={(e) => setNewFilePath(e.target.value)}
                onKeyDown={handleKeyDown}
                placeholder="/path/to/file.txt"
                autoFocus
              />
            </div>
            <div className="dialog-footer">
              <button className="button button-secondary" onClick={handleCancel}>
                Cancel
              </button>
              <button className="button button-primary" onClick={handleCreateFile}>
                Create
              </button>
            </div>
          </div>
        </div>
      )}
    </>
  );
};

export default MenuBar;
