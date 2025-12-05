import React, { useEffect, useRef, forwardRef, useImperativeHandle } from 'react';
import MonacoEditor from '@monaco-editor/react';

const Editor = forwardRef(({ file, content, onSave, onChange }, ref) => {
  const editorRef = useRef(null);

  // Expose save method to parent via ref
  useImperativeHandle(ref, () => ({
    save: () => {
      if (editorRef.current) {
        const value = editorRef.current.getValue();
        onSave(value);
      }
    }
  }));

  const handleEditorDidMount = (editor, monaco) => {
    editorRef.current = editor;

    // Add save shortcut (Ctrl+S / Cmd+S)
    editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, () => {
      const value = editor.getValue();
      onSave(value);
    });
  };

  const handleEditorChange = (value) => {
    // Notify parent of content change
    if (onChange) {
      onChange(value);
    }
  };

  return (
    <>
      <div className="editor-tabs">
        {file ? (
          <div className="editor-tab active">
            <span className="file-icon">📄</span>
            <span>{file.name}</span>
          </div>
        ) : (
          <div className="editor-tab active">
            <span>Welcome</span>
          </div>
        )}
      </div>
      <div className="editor-wrapper">
        {file ? (
          <MonacoEditor
            height="100%"
            language={getLanguageFromFilename(file.name)}
            theme="vs-dark"
            value={content}
            onChange={handleEditorChange}
            onMount={handleEditorDidMount}
            options={{
              minimap: { enabled: false },
              fontSize: 14,
              lineNumbers: 'on',
              roundedSelection: false,
              scrollBeyondLastLine: false,
              automaticLayout: true,
            }}
          />
        ) : (
          <div className="loading" style={{ color: '#969696' }}>
            Select a file to edit
          </div>
        )}
      </div>
    </>
  );
});

// Helper function to determine language from file extension
const getLanguageFromFilename = (filename) => {
  const ext = filename.split('.').pop().toLowerCase();
  const languageMap = {
    js: 'javascript',
    jsx: 'javascript',
    ts: 'typescript',
    tsx: 'typescript',
    py: 'python',
    java: 'java',
    c: 'c',
    cpp: 'cpp',
    cs: 'csharp',
    php: 'php',
    rb: 'ruby',
    go: 'go',
    rs: 'rust',
    sql: 'sql',
    sh: 'shell',
    bash: 'shell',
    json: 'json',
    xml: 'xml',
    html: 'html',
    css: 'css',
    scss: 'scss',
    sass: 'sass',
    md: 'markdown',
    yaml: 'yaml',
    yml: 'yaml',
    toml: 'toml',
    ini: 'ini',
    txt: 'plaintext',
  };
  return languageMap[ext] || 'plaintext';
};

export default Editor;
