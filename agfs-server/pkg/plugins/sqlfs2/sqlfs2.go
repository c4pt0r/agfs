package sqlfs2

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/c4pt0r/agfs/agfs-server/pkg/filesystem"
	"github.com/c4pt0r/agfs/agfs-server/pkg/plugin"
	"github.com/c4pt0r/agfs/agfs-server/pkg/plugin/config"
	log "github.com/sirupsen/logrus"
)

const (
	PluginName = "sqlfs2"
)

// Session represents a Plan 9 style session for SQL operations
type Session struct {
	id         int64            // Numeric session ID
	dbName     string
	tableName  string
	tx         *sql.Tx          // SQL transaction
	result     []byte           // Query result (JSON)
	lastError  string           // Error message
	lastAccess time.Time        // Last access time
	mu         sync.Mutex
}

// SessionManager manages all active sessions
type SessionManager struct {
	sessions map[string]*Session // key: "dbName/tableName/sid"
	nextID   int64
	timeout  time.Duration // Configurable timeout (0 = no timeout)
	mu       sync.RWMutex
	stopCh   chan struct{}
}

// NewSessionManager creates a new session manager
func NewSessionManager(timeout time.Duration) *SessionManager {
	sm := &SessionManager{
		sessions: make(map[string]*Session),
		nextID:   1,
		timeout:  timeout,
		stopCh:   make(chan struct{}),
	}
	if timeout > 0 {
		go sm.cleanupLoop()
	}
	return sm
}

// cleanupLoop periodically cleans up expired sessions
func (sm *SessionManager) cleanupLoop() {
	ticker := time.NewTicker(sm.timeout / 2)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			sm.cleanupExpired()
		case <-sm.stopCh:
			return
		}
	}
}

// cleanupExpired removes expired sessions
func (sm *SessionManager) cleanupExpired() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	now := time.Now()
	for key, session := range sm.sessions {
		session.mu.Lock()
		if now.Sub(session.lastAccess) > sm.timeout {
			if session.tx != nil {
				session.tx.Rollback()
			}
			delete(sm.sessions, key)
			log.Debugf("[sqlfs2] Session %d expired and cleaned up", session.id)
		}
		session.mu.Unlock()
	}
}

// Stop stops the cleanup goroutine
func (sm *SessionManager) Stop() {
	if sm.timeout > 0 {
		close(sm.stopCh)
	}
}

// CreateSession creates a new session for the given db/table
func (sm *SessionManager) CreateSession(db *sql.DB, dbName, tableName string) (*Session, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Start a new transaction
	tx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}

	id := sm.nextID
	sm.nextID++

	session := &Session{
		id:         id,
		dbName:     dbName,
		tableName:  tableName,
		tx:         tx,
		lastAccess: time.Now(),
	}

	key := fmt.Sprintf("%s/%s/%d", dbName, tableName, id)
	sm.sessions[key] = session

	log.Debugf("[sqlfs2] Created session %d for %s.%s", id, dbName, tableName)
	return session, nil
}

// GetSession retrieves a session by db/table/id
func (sm *SessionManager) GetSession(dbName, tableName, sid string) *Session {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	key := fmt.Sprintf("%s/%s/%s", dbName, tableName, sid)
	session := sm.sessions[key]
	if session != nil {
		session.mu.Lock()
		session.lastAccess = time.Now()
		session.mu.Unlock()
	}
	return session
}

// CloseSession closes and removes a session
func (sm *SessionManager) CloseSession(dbName, tableName, sid string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	key := fmt.Sprintf("%s/%s/%s", dbName, tableName, sid)
	session, exists := sm.sessions[key]
	if !exists {
		return fmt.Errorf("session not found: %s", sid)
	}

	session.mu.Lock()
	defer session.mu.Unlock()

	if session.tx != nil {
		session.tx.Rollback()
	}
	delete(sm.sessions, key)

	log.Debugf("[sqlfs2] Closed session %d", session.id)
	return nil
}

// ListSessions returns all session IDs for a given db/table
func (sm *SessionManager) ListSessions(dbName, tableName string) []string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	prefix := fmt.Sprintf("%s/%s/", dbName, tableName)
	var sids []string
	for key := range sm.sessions {
		if strings.HasPrefix(key, prefix) {
			sid := strings.TrimPrefix(key, prefix)
			sids = append(sids, sid)
		}
	}
	return sids
}

// SQLFS2Plugin provides a SQL interface through file system operations
// Directory structure: /sqlfs2/<dbName>/<tableName>/{ctl, schema, count, <sid>/...}
type SQLFS2Plugin struct {
	db             *sql.DB
	backend        Backend
	config         map[string]interface{}
	sessionManager *SessionManager // Shared across all filesystem instances
}

// NewSQLFS2Plugin creates a new SQLFS2 plugin
func NewSQLFS2Plugin() *SQLFS2Plugin {
	return &SQLFS2Plugin{}
}

func (p *SQLFS2Plugin) Name() string {
	return PluginName
}

func (p *SQLFS2Plugin) Validate(cfg map[string]interface{}) error {
	allowedKeys := []string{"backend", "db_path", "dsn", "user", "password", "host", "port", "database",
		"enable_tls", "tls_server_name", "tls_skip_verify", "mount_path", "session_timeout"}
	if err := config.ValidateOnlyKnownKeys(cfg, allowedKeys); err != nil {
		return err
	}

	// Validate backend type
	backendType := config.GetStringConfig(cfg, "backend", "sqlite")
	validBackends := map[string]bool{
		"sqlite":  true,
		"sqlite3": true,
		"mysql":   true,
		"tidb":    true,
	}
	if !validBackends[backendType] {
		return fmt.Errorf("unsupported database backend: %s (valid options: sqlite, sqlite3, mysql, tidb)", backendType)
	}

	// Validate optional string parameters
	for _, key := range []string{"db_path", "dsn", "user", "password", "host", "database", "tls_server_name"} {
		if err := config.ValidateStringType(cfg, key); err != nil {
			return err
		}
	}

	// Validate optional integer parameters
	for _, key := range []string{"port"} {
		if err := config.ValidateIntType(cfg, key); err != nil {
			return err
		}
	}

	// Validate optional boolean parameters
	for _, key := range []string{"enable_tls", "tls_skip_verify"} {
		if err := config.ValidateBoolType(cfg, key); err != nil {
			return err
		}
	}

	return nil
}

func (p *SQLFS2Plugin) Initialize(cfg map[string]interface{}) error {
	p.config = cfg

	backendType := config.GetStringConfig(cfg, "backend", "sqlite")

	// Create backend instance
	backend := newBackend(backendType)
	if backend == nil {
		return fmt.Errorf("unsupported backend: %s", backendType)
	}
	p.backend = backend

	// Initialize database connection using the backend
	db, err := backend.Initialize(cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize %s backend: %w", backendType, err)
	}
	p.db = db

	// Initialize session manager (shared across all filesystem instances)
	var timeout time.Duration
	if timeoutStr := config.GetStringConfig(cfg, "session_timeout", ""); timeoutStr != "" {
		if parsed, err := time.ParseDuration(timeoutStr); err == nil {
			timeout = parsed
		}
	}
	p.sessionManager = NewSessionManager(timeout)

	log.Infof("[sqlfs2] Initialized with backend: %s", backendType)
	return nil
}

func (p *SQLFS2Plugin) GetFileSystem() filesystem.FileSystem {
	return &sqlfs2FS{
		plugin:         p,
		handles:        make(map[int64]*SQLFileHandle),
		nextHandleID:   1,
		sessionManager: p.sessionManager, // Use shared session manager
	}
}

func (p *SQLFS2Plugin) GetReadme() string {
	return getReadme()
}

func (p *SQLFS2Plugin) GetConfigParams() []plugin.ConfigParameter {
	return []plugin.ConfigParameter{
		{
			Name:        "backend",
			Type:        "string",
			Required:    false,
			Default:     "sqlite",
			Description: "Database backend (sqlite, sqlite3, mysql, tidb)",
		},
		{
			Name:        "db_path",
			Type:        "string",
			Required:    false,
			Default:     "",
			Description: "Database file path (for SQLite)",
		},
		{
			Name:        "dsn",
			Type:        "string",
			Required:    false,
			Default:     "",
			Description: "Database connection string (DSN)",
		},
		{
			Name:        "user",
			Type:        "string",
			Required:    false,
			Default:     "",
			Description: "Database username",
		},
		{
			Name:        "password",
			Type:        "string",
			Required:    false,
			Default:     "",
			Description: "Database password",
		},
		{
			Name:        "host",
			Type:        "string",
			Required:    false,
			Default:     "",
			Description: "Database host",
		},
		{
			Name:        "port",
			Type:        "int",
			Required:    false,
			Default:     "",
			Description: "Database port",
		},
		{
			Name:        "database",
			Type:        "string",
			Required:    false,
			Default:     "",
			Description: "Database name",
		},
		{
			Name:        "enable_tls",
			Type:        "bool",
			Required:    false,
			Default:     "false",
			Description: "Enable TLS for database connection",
		},
		{
			Name:        "tls_server_name",
			Type:        "string",
			Required:    false,
			Default:     "",
			Description: "TLS server name for verification",
		},
		{
			Name:        "tls_skip_verify",
			Type:        "bool",
			Required:    false,
			Default:     "false",
			Description: "Skip TLS certificate verification",
		},
		{
			Name:        "session_timeout",
			Type:        "string",
			Required:    false,
			Default:     "",
			Description: "Session timeout duration (e.g., '10m', '1h'). Empty means no timeout.",
		},
	}
}

func (p *SQLFS2Plugin) Shutdown() error {
	if p.sessionManager != nil {
		p.sessionManager.Stop()
	}
	if p.db != nil {
		return p.db.Close()
	}
	return nil
}

// sqlfs2FS implements the FileSystem interface for SQL operations
type sqlfs2FS struct {
	plugin         *SQLFS2Plugin
	handles        map[int64]*SQLFileHandle
	handlesMu      sync.RWMutex
	nextHandleID   int64
	sessionManager *SessionManager
}

// isSessionID checks if the given string is a numeric session ID
func isSessionID(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// isTableLevelFile checks if the given name is a table-level special file
func isTableLevelFile(name string) bool {
	return name == "ctl" || name == "schema" || name == "count"
}

// isRootLevelFile checks if the given name is a root-level special file
func isRootLevelFile(name string) bool {
	return name == "ctl"
}

// isSessionFile checks if the given name is a session-level file
func isSessionFile(name string) bool {
	return name == "ctl" || name == "query" || name == "result" || name == "data" || name == "error"
}

// isDatabaseLevelFile checks if the given name is a database-level special file
func isDatabaseLevelFile(name string) bool {
	return name == "ctl"
}

// parsePath parses a path into (dbName, tableName, sid, operation)
// Supported paths:
//   /                              -> ("", "", "", "")
//   /ctl                           -> ("", "", "", "ctl") - root level ctl
//   /<sid>                         -> ("", "", sid, "")   - root level session
//   /<sid>/query                   -> ("", "", sid, "query")
//   /dbName                        -> (dbName, "", "", "")
//   /dbName/ctl                    -> (dbName, "", "", "ctl") - database level ctl
//   /dbName/<sid>                  -> (dbName, "", sid, "") - database level session
//   /dbName/<sid>/query            -> (dbName, "", sid, "query") - database level session file
//   /dbName/tableName              -> (dbName, tableName, "", "")
//   /dbName/tableName/ctl          -> (dbName, tableName, "", "ctl")
//   /dbName/tableName/schema       -> (dbName, tableName, "", "schema")
//   /dbName/tableName/count        -> (dbName, tableName, "", "count")
//   /dbName/tableName/<sid>        -> (dbName, tableName, sid, "")
//   /dbName/tableName/<sid>/query  -> (dbName, tableName, sid, "query")
//   /dbName/tableName/<sid>/result -> (dbName, tableName, sid, "result")
//   /dbName/tableName/<sid>/ctl    -> (dbName, tableName, sid, "ctl")
//   /dbName/tableName/<sid>/data   -> (dbName, tableName, sid, "data")
//   /dbName/tableName/<sid>/error  -> (dbName, tableName, sid, "error")
func (fs *sqlfs2FS) parsePath(path string) (dbName, tableName, sid, operation string, err error) {
	path = strings.TrimPrefix(path, "/")
	parts := strings.Split(path, "/")

	if len(parts) == 0 || path == "" {
		// Root directory
		return "", "", "", "", nil
	}

	if len(parts) == 1 {
		// Could be:
		// - /ctl -> root level ctl file
		// - /<sid> -> root level session directory
		// - /dbName -> database directory
		if isRootLevelFile(parts[0]) {
			return "", "", "", parts[0], nil
		}
		if isSessionID(parts[0]) {
			return "", "", parts[0], "", nil
		}
		// Database level: /dbName
		return parts[0], "", "", "", nil
	}

	if len(parts) == 2 {
		// Could be:
		// - /<sid>/query -> root level session file
		// - /dbName/ctl -> database level ctl file
		// - /dbName/<sid> -> database level session directory
		// - /dbName/tableName -> table directory
		if isSessionID(parts[0]) && isSessionFile(parts[1]) {
			return "", "", parts[0], parts[1], nil
		}
		if isDatabaseLevelFile(parts[1]) {
			// Database level ctl: /dbName/ctl
			return parts[0], "", "", parts[1], nil
		}
		if isSessionID(parts[1]) {
			// Database level session: /dbName/<sid>
			return parts[0], "", parts[1], "", nil
		}
		// Table level: /dbName/tableName
		return parts[0], parts[1], "", "", nil
	}

	if len(parts) == 3 {
		// Could be:
		// - /dbName/<sid>/query -> database level session file
		// - /dbName/tableName/ctl -> table-level ctl
		// - /dbName/tableName/schema -> table-level schema
		// - /dbName/tableName/count -> table-level count
		// - /dbName/tableName/<sid> -> session directory
		if isSessionID(parts[1]) && isSessionFile(parts[2]) {
			// Database level session file: /dbName/<sid>/query
			return parts[0], "", parts[1], parts[2], nil
		}
		if isTableLevelFile(parts[2]) {
			return parts[0], parts[1], "", parts[2], nil
		}
		if isSessionID(parts[2]) {
			return parts[0], parts[1], parts[2], "", nil
		}
		return "", "", "", "", fmt.Errorf("invalid path component: %s", parts[2])
	}

	if len(parts) == 4 {
		// Session-level file: /dbName/tableName/<sid>/operation
		if !isSessionID(parts[2]) {
			return "", "", "", "", fmt.Errorf("invalid session ID: %s", parts[2])
		}
		return parts[0], parts[1], parts[2], parts[3], nil
	}

	return "", "", "", "", fmt.Errorf("invalid path: %s", path)
}

// tableExists checks if a table exists in the specified database
func (fs *sqlfs2FS) tableExists(dbName, tableName string) (bool, error) {
	if dbName == "" || tableName == "" {
		return false, fmt.Errorf("dbName and tableName must not be empty")
	}

	tables, err := fs.plugin.backend.ListTables(fs.plugin.db, dbName)
	if err != nil {
		return false, err
	}

	for _, t := range tables {
		if t == tableName {
			return true, nil
		}
	}

	return false, nil
}

func (fs *sqlfs2FS) Read(path string, offset int64, size int64) ([]byte, error) {
	dbName, tableName, sid, operation, err := fs.parsePath(path)
	if err != nil {
		return nil, err
	}

	// Root-level files (no db, no table, no session)
	if dbName == "" && tableName == "" && sid == "" {
		switch operation {
		case "ctl":
			// Root-level ctl: creates a global session (no table binding)
			session, err := fs.sessionManager.CreateSession(fs.plugin.db, "", "")
			if err != nil {
				return nil, err
			}
			data := []byte(fmt.Sprintf("%d\n", session.id))
			return plugin.ApplyRangeRead(data, offset, size)

		case "":
			// Root directory
			return nil, filesystem.NewInvalidArgumentError("path", path, "is a directory")

		default:
			return nil, fmt.Errorf("unknown root-level file: %s", operation)
		}
	}

	// Root-level session files (no db, no table, but has session)
	if dbName == "" && tableName == "" && sid != "" {
		session := fs.sessionManager.GetSession("", "", sid)
		if session == nil {
			return nil, fmt.Errorf("session not found: %s", sid)
		}

		switch operation {
		case "result":
			session.mu.Lock()
			result := session.result
			session.mu.Unlock()

			if result == nil {
				return []byte{}, nil
			}
			return plugin.ApplyRangeRead(result, offset, size)

		case "error":
			session.mu.Lock()
			errMsg := session.lastError
			session.mu.Unlock()

			if errMsg == "" {
				return []byte{}, nil
			}
			data := []byte(errMsg + "\n")
			return plugin.ApplyRangeRead(data, offset, size)

		case "query", "data", "ctl":
			return nil, fmt.Errorf("%s is write-only", operation)

		case "":
			// Session directory
			return nil, filesystem.NewInvalidArgumentError("path", path, "is a directory")

		default:
			return nil, fmt.Errorf("unknown session file: %s", operation)
		}
	}

	// Database-level files (has db, no table, no session)
	if dbName != "" && tableName == "" && sid == "" {
		switch operation {
		case "ctl":
			// Database-level ctl: creates a database-scoped session (no table binding)
			// Switch to database if needed
			if err := fs.plugin.backend.SwitchDatabase(fs.plugin.db, dbName); err != nil {
				return nil, err
			}

			session, err := fs.sessionManager.CreateSession(fs.plugin.db, dbName, "")
			if err != nil {
				return nil, err
			}
			data := []byte(fmt.Sprintf("%d\n", session.id))
			return plugin.ApplyRangeRead(data, offset, size)

		case "":
			// Database directory
			return nil, filesystem.NewInvalidArgumentError("path", path, "is a directory")

		default:
			return nil, fmt.Errorf("unknown database-level file: %s", operation)
		}
	}

	// Database-level session files (has db, no table, but has session)
	if dbName != "" && tableName == "" && sid != "" {
		session := fs.sessionManager.GetSession(dbName, "", sid)
		if session == nil {
			return nil, fmt.Errorf("session not found: %s", sid)
		}

		switch operation {
		case "result":
			session.mu.Lock()
			result := session.result
			session.mu.Unlock()

			if result == nil {
				return []byte{}, nil
			}
			return plugin.ApplyRangeRead(result, offset, size)

		case "error":
			session.mu.Lock()
			errMsg := session.lastError
			session.mu.Unlock()

			if errMsg == "" {
				return []byte{}, nil
			}
			data := []byte(errMsg + "\n")
			return plugin.ApplyRangeRead(data, offset, size)

		case "query", "data", "ctl":
			return nil, fmt.Errorf("%s is write-only", operation)

		case "":
			// Session directory
			return nil, filesystem.NewInvalidArgumentError("path", path, "is a directory")

		default:
			return nil, fmt.Errorf("unknown session file: %s", operation)
		}
	}

	// Table-level files (no session)
	if sid == "" {
		switch operation {
		case "ctl":
			// Reading ctl creates a new session and returns the session ID
			if dbName == "" || tableName == "" {
				return nil, fmt.Errorf("invalid path for ctl: %s", path)
			}

			// Check if table exists
			exists, err := fs.tableExists(dbName, tableName)
			if err != nil {
				return nil, fmt.Errorf("failed to check table existence: %w", err)
			}
			if !exists {
				return nil, fmt.Errorf("table '%s.%s' does not exist", dbName, tableName)
			}

			// Switch to database if needed
			if err := fs.plugin.backend.SwitchDatabase(fs.plugin.db, dbName); err != nil {
				return nil, err
			}

			// Create new session
			session, err := fs.sessionManager.CreateSession(fs.plugin.db, dbName, tableName)
			if err != nil {
				return nil, err
			}

			data := []byte(fmt.Sprintf("%d\n", session.id))
			return plugin.ApplyRangeRead(data, offset, size)

		case "schema":
			if dbName == "" || tableName == "" {
				return nil, fmt.Errorf("invalid path for schema: %s", path)
			}

			createTableStmt, err := fs.plugin.backend.GetTableSchema(fs.plugin.db, dbName, tableName)
			if err != nil {
				return nil, err
			}

			data := []byte(createTableStmt + "\n")
			return plugin.ApplyRangeRead(data, offset, size)

		case "count":
			if dbName == "" || tableName == "" {
				return nil, fmt.Errorf("invalid path for count: %s", path)
			}

			if err := fs.plugin.backend.SwitchDatabase(fs.plugin.db, dbName); err != nil {
				return nil, err
			}

			sqlStmt := fmt.Sprintf("SELECT COUNT(*) FROM %s.%s", dbName, tableName)
			var count int64
			err := fs.plugin.db.QueryRow(sqlStmt).Scan(&count)
			if err != nil {
				return nil, fmt.Errorf("count query error: %w", err)
			}

			data := []byte(fmt.Sprintf("%d\n", count))
			return plugin.ApplyRangeRead(data, offset, size)

		case "":
			// Directory read
			return nil, filesystem.NewInvalidArgumentError("path", path, "is a directory")

		default:
			return nil, fmt.Errorf("unknown table-level file: %s", operation)
		}
	}

	// Session-level files (table-bound sessions)
	session := fs.sessionManager.GetSession(dbName, tableName, sid)
	if session == nil {
		return nil, fmt.Errorf("session not found: %s", sid)
	}

	switch operation {
	case "result":
		session.mu.Lock()
		result := session.result
		session.mu.Unlock()

		if result == nil {
			return []byte{}, nil
		}
		return plugin.ApplyRangeRead(result, offset, size)

	case "error":
		session.mu.Lock()
		errMsg := session.lastError
		session.mu.Unlock()

		if errMsg == "" {
			return []byte{}, nil
		}
		data := []byte(errMsg + "\n")
		return plugin.ApplyRangeRead(data, offset, size)

	case "query", "data", "ctl":
		return nil, fmt.Errorf("%s is write-only", operation)

	case "":
		// Session directory
		return nil, filesystem.NewInvalidArgumentError("path", path, "is a directory")

	default:
		return nil, fmt.Errorf("unknown session file: %s", operation)
	}
}

func (fs *sqlfs2FS) Write(path string, data []byte, offset int64, flags filesystem.WriteFlag) (int64, error) {
	dbName, tableName, sid, operation, err := fs.parsePath(path)
	if err != nil {
		return 0, err
	}

	// Root-level files (no db, no table, no session)
	if dbName == "" && tableName == "" && sid == "" {
		switch operation {
		case "":
			return 0, fmt.Errorf("cannot write to directory: %s", path)
		case "ctl":
			return 0, fmt.Errorf("ctl is read-only")
		default:
			return 0, fmt.Errorf("unknown root-level file: %s", operation)
		}
	}

	// Root-level session files (no db, no table, but has session)
	if dbName == "" && tableName == "" && sid != "" {
		session := fs.sessionManager.GetSession("", "", sid)
		if session == nil {
			return 0, fmt.Errorf("session not found: %s", sid)
		}

		session.mu.Lock()
		defer session.mu.Unlock()

		switch operation {
		case "ctl":
			cmd := strings.TrimSpace(string(data))
			if cmd == "close" {
				session.mu.Unlock()
				err := fs.sessionManager.CloseSession("", "", sid)
				session.mu.Lock()
				if err != nil {
					return 0, err
				}
				return int64(len(data)), nil
			}
			return 0, fmt.Errorf("unknown ctl command: %s", cmd)

		case "query":
			// Execute SQL query and store result
			sqlStmt := strings.TrimSpace(string(data))
			if sqlStmt == "" {
				session.lastError = "empty SQL statement"
				return 0, fmt.Errorf("empty SQL statement")
			}

			// Determine if this is a SELECT query
			upperSQL := strings.ToUpper(sqlStmt)
			isSelect := strings.HasPrefix(upperSQL, "SELECT") ||
				strings.HasPrefix(upperSQL, "SHOW") ||
				strings.HasPrefix(upperSQL, "DESCRIBE") ||
				strings.HasPrefix(upperSQL, "EXPLAIN")

			if isSelect {
				rows, err := session.tx.Query(sqlStmt)
				if err != nil {
					session.lastError = err.Error()
					session.result = nil
					return 0, fmt.Errorf("query error: %w", err)
				}
				defer rows.Close()

				columns, err := rows.Columns()
				if err != nil {
					session.lastError = err.Error()
					session.result = nil
					return 0, fmt.Errorf("failed to get columns: %w", err)
				}

				var results []map[string]interface{}
				for rows.Next() {
					values := make([]interface{}, len(columns))
					valuePtrs := make([]interface{}, len(columns))
					for i := range values {
						valuePtrs[i] = &values[i]
					}

					if err := rows.Scan(valuePtrs...); err != nil {
						session.lastError = err.Error()
						session.result = nil
						return 0, fmt.Errorf("scan error: %w", err)
					}

					row := make(map[string]interface{})
					for i, col := range columns {
						val := values[i]
						if b, ok := val.([]byte); ok {
							row[col] = string(b)
						} else {
							row[col] = val
						}
					}
					results = append(results, row)
				}

				if err := rows.Err(); err != nil {
					session.lastError = err.Error()
					session.result = nil
					return 0, fmt.Errorf("rows error: %w", err)
				}

				jsonData, err := json.MarshalIndent(results, "", "  ")
				if err != nil {
					session.lastError = err.Error()
					session.result = nil
					return 0, fmt.Errorf("json marshal error: %w", err)
				}
				session.result = append(jsonData, '\n')
				session.lastError = ""
			} else {
				result, err := session.tx.Exec(sqlStmt)
				if err != nil {
					session.lastError = err.Error()
					session.result = nil
					return 0, fmt.Errorf("execution error: %w", err)
				}

				rowsAffected, _ := result.RowsAffected()
				lastInsertId, _ := result.LastInsertId()

				resultMap := map[string]interface{}{
					"rows_affected":  rowsAffected,
					"last_insert_id": lastInsertId,
				}
				jsonData, _ := json.MarshalIndent(resultMap, "", "  ")
				session.result = append(jsonData, '\n')
				session.lastError = ""
			}

			return int64(len(data)), nil

		case "result", "error":
			return 0, fmt.Errorf("%s is read-only", operation)

		case "":
			return 0, fmt.Errorf("cannot write to directory: %s", path)

		default:
			return 0, fmt.Errorf("unknown session file: %s", operation)
		}
	}

	// Database-level files (has db, no table, no session)
	if dbName != "" && tableName == "" && sid == "" {
		switch operation {
		case "":
			return 0, fmt.Errorf("cannot write to directory: %s", path)
		case "ctl":
			return 0, fmt.Errorf("ctl is read-only")
		default:
			return 0, fmt.Errorf("unknown database-level file: %s", operation)
		}
	}

	// Database-level session files (has db, no table, but has session)
	if dbName != "" && tableName == "" && sid != "" {
		session := fs.sessionManager.GetSession(dbName, "", sid)
		if session == nil {
			return 0, fmt.Errorf("session not found: %s", sid)
		}

		session.mu.Lock()
		defer session.mu.Unlock()

		switch operation {
		case "ctl":
			cmd := strings.TrimSpace(string(data))
			if cmd == "close" {
				session.mu.Unlock()
				err := fs.sessionManager.CloseSession(dbName, "", sid)
				session.mu.Lock()
				if err != nil {
					return 0, err
				}
				return int64(len(data)), nil
			}
			return 0, fmt.Errorf("unknown ctl command: %s", cmd)

		case "query":
			// Execute SQL query and store result
			sqlStmt := strings.TrimSpace(string(data))
			if sqlStmt == "" {
				session.lastError = "empty SQL statement"
				return 0, fmt.Errorf("empty SQL statement")
			}

			// Determine if this is a SELECT query
			upperSQL := strings.ToUpper(sqlStmt)
			isSelect := strings.HasPrefix(upperSQL, "SELECT") ||
				strings.HasPrefix(upperSQL, "SHOW") ||
				strings.HasPrefix(upperSQL, "DESCRIBE") ||
				strings.HasPrefix(upperSQL, "EXPLAIN")

			if isSelect {
				rows, err := session.tx.Query(sqlStmt)
				if err != nil {
					session.lastError = err.Error()
					session.result = nil
					return 0, fmt.Errorf("query error: %w", err)
				}
				defer rows.Close()

				columns, err := rows.Columns()
				if err != nil {
					session.lastError = err.Error()
					session.result = nil
					return 0, fmt.Errorf("failed to get columns: %w", err)
				}

				var results []map[string]interface{}
				for rows.Next() {
					values := make([]interface{}, len(columns))
					valuePtrs := make([]interface{}, len(columns))
					for i := range values {
						valuePtrs[i] = &values[i]
					}

					if err := rows.Scan(valuePtrs...); err != nil {
						session.lastError = err.Error()
						session.result = nil
						return 0, fmt.Errorf("scan error: %w", err)
					}

					row := make(map[string]interface{})
					for i, col := range columns {
						val := values[i]
						if b, ok := val.([]byte); ok {
							row[col] = string(b)
						} else {
							row[col] = val
						}
					}
					results = append(results, row)
				}

				if err := rows.Err(); err != nil {
					session.lastError = err.Error()
					session.result = nil
					return 0, fmt.Errorf("rows error: %w", err)
				}

				jsonData, err := json.MarshalIndent(results, "", "  ")
				if err != nil {
					session.lastError = err.Error()
					session.result = nil
					return 0, fmt.Errorf("json marshal error: %w", err)
				}
				session.result = append(jsonData, '\n')
				session.lastError = ""
			} else {
				result, err := session.tx.Exec(sqlStmt)
				if err != nil {
					session.lastError = err.Error()
					session.result = nil
					return 0, fmt.Errorf("execution error: %w", err)
				}

				rowsAffected, _ := result.RowsAffected()
				lastInsertId, _ := result.LastInsertId()

				resultMap := map[string]interface{}{
					"rows_affected":  rowsAffected,
					"last_insert_id": lastInsertId,
				}
				jsonData, _ := json.MarshalIndent(resultMap, "", "  ")
				session.result = append(jsonData, '\n')
				session.lastError = ""
			}

			return int64(len(data)), nil

		case "result", "error":
			return 0, fmt.Errorf("%s is read-only", operation)

		case "":
			return 0, fmt.Errorf("cannot write to directory: %s", path)

		default:
			return 0, fmt.Errorf("unknown session file: %s", operation)
		}
	}

	// Table-level files (no session)
	if sid == "" {
		switch operation {
		case "":
			return 0, fmt.Errorf("cannot write to directory: %s", path)
		case "ctl", "schema", "count":
			return 0, fmt.Errorf("%s is read-only", operation)
		default:
			return 0, fmt.Errorf("unknown table-level file: %s", operation)
		}
	}

	// Session-level files (table-bound sessions)
	session := fs.sessionManager.GetSession(dbName, tableName, sid)
	if session == nil {
		return 0, fmt.Errorf("session not found: %s", sid)
	}

	session.mu.Lock()
	defer session.mu.Unlock()

	switch operation {
	case "ctl":
		// Writing "close" to ctl closes the session
		cmd := strings.TrimSpace(string(data))
		if cmd == "close" {
			session.mu.Unlock() // Unlock before closing
			err := fs.sessionManager.CloseSession(dbName, tableName, sid)
			session.mu.Lock() // Re-lock for deferred unlock
			if err != nil {
				return 0, err
			}
			return int64(len(data)), nil
		}
		return 0, fmt.Errorf("unknown ctl command: %s", cmd)

	case "query":
		// Execute SQL query and store result
		sqlStmt := strings.TrimSpace(string(data))
		if sqlStmt == "" {
			session.lastError = "empty SQL statement"
			return 0, fmt.Errorf("empty SQL statement")
		}

		// Determine if this is a SELECT query
		upperSQL := strings.ToUpper(sqlStmt)
		isSelect := strings.HasPrefix(upperSQL, "SELECT") ||
			strings.HasPrefix(upperSQL, "SHOW") ||
			strings.HasPrefix(upperSQL, "DESCRIBE") ||
			strings.HasPrefix(upperSQL, "EXPLAIN")

		if isSelect {
			// Execute SELECT query
			rows, err := session.tx.Query(sqlStmt)
			if err != nil {
				session.lastError = err.Error()
				session.result = nil
				return 0, fmt.Errorf("query error: %w", err)
			}
			defer rows.Close()

			// Get column names
			columns, err := rows.Columns()
			if err != nil {
				session.lastError = err.Error()
				session.result = nil
				return 0, fmt.Errorf("failed to get columns: %w", err)
			}

			// Read all results
			var results []map[string]interface{}
			for rows.Next() {
				values := make([]interface{}, len(columns))
				valuePtrs := make([]interface{}, len(columns))
				for i := range values {
					valuePtrs[i] = &values[i]
				}

				if err := rows.Scan(valuePtrs...); err != nil {
					session.lastError = err.Error()
					session.result = nil
					return 0, fmt.Errorf("scan error: %w", err)
				}

				row := make(map[string]interface{})
				for i, col := range columns {
					val := values[i]
					if b, ok := val.([]byte); ok {
						row[col] = string(b)
					} else {
						row[col] = val
					}
				}
				results = append(results, row)
			}

			if err := rows.Err(); err != nil {
				session.lastError = err.Error()
				session.result = nil
				return 0, fmt.Errorf("rows error: %w", err)
			}

			// Store results as JSON
			jsonData, err := json.MarshalIndent(results, "", "  ")
			if err != nil {
				session.lastError = err.Error()
				session.result = nil
				return 0, fmt.Errorf("json marshal error: %w", err)
			}
			session.result = append(jsonData, '\n')
			session.lastError = ""
		} else {
			// Execute DML statement (INSERT, UPDATE, DELETE, etc.)
			result, err := session.tx.Exec(sqlStmt)
			if err != nil {
				session.lastError = err.Error()
				session.result = nil
				return 0, fmt.Errorf("execution error: %w", err)
			}

			rowsAffected, _ := result.RowsAffected()
			lastInsertId, _ := result.LastInsertId()

			// Store result as JSON
			resultMap := map[string]interface{}{
				"rows_affected":  rowsAffected,
				"last_insert_id": lastInsertId,
			}
			jsonData, _ := json.MarshalIndent(resultMap, "", "  ")
			session.result = append(jsonData, '\n')
			session.lastError = ""
		}

		return int64(len(data)), nil

	case "data":
		// Insert JSON data
		columns, err := fs.plugin.backend.GetTableColumns(fs.plugin.db, dbName, tableName)
		if err != nil {
			session.lastError = err.Error()
			return 0, fmt.Errorf("failed to get table columns: %w", err)
		}

		if len(columns) == 0 {
			session.lastError = "no columns found for table"
			return 0, fmt.Errorf("no columns found for table %s", tableName)
		}

		columnNames := make([]string, len(columns))
		for i, col := range columns {
			columnNames[i] = col.Name
		}

		// Parse JSON (support single object, array, or NDJSON)
		var records []map[string]interface{}
		dataStr := string(data)
		lines := strings.Split(dataStr, "\n")

		// Check for NDJSON mode
		nonEmptyLines := 0
		firstNonEmptyIdx := -1
		for i, line := range lines {
			if strings.TrimSpace(line) != "" {
				nonEmptyLines++
				if firstNonEmptyIdx == -1 {
					firstNonEmptyIdx = i
				}
			}
		}

		isStreamMode := false
		if nonEmptyLines > 1 && firstNonEmptyIdx >= 0 {
			var testObj map[string]interface{}
			firstLine := strings.TrimSpace(lines[firstNonEmptyIdx])
			if err := json.Unmarshal([]byte(firstLine), &testObj); err == nil {
				isStreamMode = true
			}
		}

		if isStreamMode {
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				var record map[string]interface{}
				if err := json.Unmarshal([]byte(line), &record); err != nil {
					continue
				}
				records = append(records, record)
			}
		} else {
			var jsonData interface{}
			if err := json.Unmarshal(data, &jsonData); err != nil {
				session.lastError = err.Error()
				return 0, fmt.Errorf("invalid JSON: %w", err)
			}

			switch v := jsonData.(type) {
			case map[string]interface{}:
				records = append(records, v)
			case []interface{}:
				for i, item := range v {
					if record, ok := item.(map[string]interface{}); ok {
						records = append(records, record)
					} else {
						session.lastError = fmt.Sprintf("element at index %d is not a JSON object", i)
						return 0, fmt.Errorf("element at index %d is not a JSON object", i)
					}
				}
			default:
				session.lastError = "JSON must be an object or array of objects"
				return 0, fmt.Errorf("JSON must be an object or array of objects")
			}
		}

		if len(records) == 0 {
			session.lastError = "no records to insert"
			return 0, fmt.Errorf("no records to insert")
		}

		// Execute inserts in transaction
		insertedCount := 0
		for idx, record := range records {
			values := make([]interface{}, len(columnNames))
			for i, colName := range columnNames {
				if val, ok := record[colName]; ok {
					values[i] = val
				} else {
					values[i] = nil
				}
			}

			placeholders := make([]string, len(columnNames))
			for i := range placeholders {
				placeholders[i] = "?"
			}

			insertSQL := fmt.Sprintf("INSERT INTO %s.%s (%s) VALUES (%s)",
				dbName, tableName,
				strings.Join(columnNames, ", "),
				strings.Join(placeholders, ", "))

			if _, err := session.tx.Exec(insertSQL, values...); err != nil {
				session.lastError = fmt.Sprintf("insert error at record %d: %v", idx+1, err)
				session.result = nil
				return 0, fmt.Errorf("insert error at record %d: %w", idx+1, err)
			}
			insertedCount++
		}

		// Store result as JSON
		resultMap := map[string]interface{}{
			"inserted_count": insertedCount,
		}
		jsonData, _ := json.MarshalIndent(resultMap, "", "  ")
		session.result = append(jsonData, '\n')
		session.lastError = ""

		return int64(len(data)), nil

	case "result", "error":
		return 0, fmt.Errorf("%s is read-only", operation)

	case "":
		return 0, fmt.Errorf("cannot write to directory: %s", path)

	default:
		return 0, fmt.Errorf("unknown session file: %s", operation)
	}
}

func (fs *sqlfs2FS) Create(path string) error {
	return fmt.Errorf("operation not supported: create")
}

func (fs *sqlfs2FS) Mkdir(path string, perm uint32) error {
	return fmt.Errorf("operation not supported: mkdir")
}

func (fs *sqlfs2FS) Remove(path string) error {
	return fmt.Errorf("operation not supported: remove")
}

func (fs *sqlfs2FS) RemoveAll(path string) error {
	dbName, tableName, sid, operation, err := fs.parsePath(path)
	if err != nil {
		return err
	}

	// Support removing root-level session
	// Path should be /<sid>
	if dbName == "" && tableName == "" && sid != "" && operation == "" {
		return fs.sessionManager.CloseSession("", "", sid)
	}

	// Support removing database-level session
	// Path should be /dbName/<sid>
	if dbName != "" && tableName == "" && sid != "" && operation == "" {
		return fs.sessionManager.CloseSession(dbName, "", sid)
	}

	// Support removing database (DROP DATABASE)
	// Path should be /dbName
	if dbName != "" && tableName == "" && sid == "" && operation == "" {
		// Execute DROP DATABASE
		sqlStmt := fmt.Sprintf("DROP DATABASE IF EXISTS %s", dbName)
		_, err := fs.plugin.db.Exec(sqlStmt)
		if err != nil {
			return fmt.Errorf("failed to drop database: %w", err)
		}

		log.Infof("[sqlfs2] Dropped database: %s", dbName)
		return nil
	}

	// Support removing tables (DROP TABLE)
	// Path should be /dbName/tableName
	if dbName != "" && tableName != "" && sid == "" && operation == "" {
		// Switch to database if needed
		if err := fs.plugin.backend.SwitchDatabase(fs.plugin.db, dbName); err != nil {
			return err
		}

		// Execute DROP TABLE
		sqlStmt := fmt.Sprintf("DROP TABLE IF EXISTS %s.%s", dbName, tableName)
		_, err := fs.plugin.db.Exec(sqlStmt)
		if err != nil {
			return fmt.Errorf("failed to drop table: %w", err)
		}

		log.Infof("[sqlfs2] Dropped table: %s.%s", dbName, tableName)
		return nil
	}

	// Support removing session directory
	// Path should be /dbName/tableName/<sid>
	if dbName != "" && tableName != "" && sid != "" && operation == "" {
		return fs.sessionManager.CloseSession(dbName, tableName, sid)
	}

	return fmt.Errorf("operation not supported: can only remove databases, tables, or sessions")
}

func (fs *sqlfs2FS) ReadDir(path string) ([]filesystem.FileInfo, error) {
	dbName, tableName, sid, operation, err := fs.parsePath(path)
	if err != nil {
		return nil, err
	}

	now := time.Now()

	// Root directory: list ctl, databases, and root-level sessions
	if dbName == "" && tableName == "" && sid == "" && operation == "" {
		entries := []filesystem.FileInfo{
			{
				Name:    "ctl",
				Size:    0,
				Mode:    0444, // read-only (reading creates session)
				ModTime: now,
				IsDir:   false,
				Meta:    filesystem.MetaData{Name: PluginName, Type: "ctl"},
			},
		}

		// Add root-level sessions
		sids := fs.sessionManager.ListSessions("", "")
		for _, s := range sids {
			entries = append(entries, filesystem.FileInfo{
				Name:    s,
				Size:    0,
				Mode:    0755,
				ModTime: now,
				IsDir:   true,
				Meta:    filesystem.MetaData{Name: PluginName, Type: "session"},
			})
		}

		// Add databases
		dbNames, err := fs.plugin.backend.ListDatabases(fs.plugin.db)
		if err != nil {
			return nil, err
		}
		for _, name := range dbNames {
			entries = append(entries, filesystem.FileInfo{
				Name:    name,
				Size:    0,
				Mode:    0755,
				ModTime: now,
				IsDir:   true,
				Meta:    filesystem.MetaData{Name: PluginName, Type: "database"},
			})
		}
		return entries, nil
	}

	// Root-level session directory
	if dbName == "" && tableName == "" && sid != "" && operation == "" {
		session := fs.sessionManager.GetSession("", "", sid)
		if session == nil {
			return nil, fmt.Errorf("session not found: %s", sid)
		}

		return []filesystem.FileInfo{
			{
				Name:    "ctl",
				Size:    0,
				Mode:    0222,
				ModTime: now,
				IsDir:   false,
				Meta:    filesystem.MetaData{Name: PluginName, Type: "session-ctl"},
			},
			{
				Name:    "query",
				Size:    0,
				Mode:    0222,
				ModTime: now,
				IsDir:   false,
				Meta:    filesystem.MetaData{Name: PluginName, Type: "query"},
			},
			{
				Name:    "result",
				Size:    0,
				Mode:    0444,
				ModTime: now,
				IsDir:   false,
				Meta:    filesystem.MetaData{Name: PluginName, Type: "result"},
			},
			{
				Name:    "error",
				Size:    0,
				Mode:    0444,
				ModTime: now,
				IsDir:   false,
				Meta:    filesystem.MetaData{Name: PluginName, Type: "error"},
			},
		}, nil
	}

	// Database level: list ctl, tables, and database-level sessions
	if dbName != "" && tableName == "" && sid == "" && operation == "" {
		entries := []filesystem.FileInfo{
			{
				Name:    "ctl",
				Size:    0,
				Mode:    0444, // read-only (reading creates session)
				ModTime: now,
				IsDir:   false,
				Meta:    filesystem.MetaData{Name: PluginName, Type: "ctl"},
			},
		}

		// Add database-level sessions
		sids := fs.sessionManager.ListSessions(dbName, "")
		for _, s := range sids {
			entries = append(entries, filesystem.FileInfo{
				Name:    s,
				Size:    0,
				Mode:    0755,
				ModTime: now,
				IsDir:   true,
				Meta:    filesystem.MetaData{Name: PluginName, Type: "session"},
			})
		}

		// Add tables
		tableNames, err := fs.plugin.backend.ListTables(fs.plugin.db, dbName)
		if err != nil {
			return nil, err
		}
		for _, name := range tableNames {
			entries = append(entries, filesystem.FileInfo{
				Name:    name,
				Size:    0,
				Mode:    0755,
				ModTime: now,
				IsDir:   true,
				Meta:    filesystem.MetaData{Name: PluginName, Type: "table"},
			})
		}
		return entries, nil
	}

	// Database-level session directory
	if dbName != "" && tableName == "" && sid != "" && operation == "" {
		session := fs.sessionManager.GetSession(dbName, "", sid)
		if session == nil {
			return nil, fmt.Errorf("session not found: %s", sid)
		}

		return []filesystem.FileInfo{
			{
				Name:    "ctl",
				Size:    0,
				Mode:    0222,
				ModTime: now,
				IsDir:   false,
				Meta:    filesystem.MetaData{Name: PluginName, Type: "session-ctl"},
			},
			{
				Name:    "query",
				Size:    0,
				Mode:    0222,
				ModTime: now,
				IsDir:   false,
				Meta:    filesystem.MetaData{Name: PluginName, Type: "query"},
			},
			{
				Name:    "result",
				Size:    0,
				Mode:    0444,
				ModTime: now,
				IsDir:   false,
				Meta:    filesystem.MetaData{Name: PluginName, Type: "result"},
			},
			{
				Name:    "error",
				Size:    0,
				Mode:    0444,
				ModTime: now,
				IsDir:   false,
				Meta:    filesystem.MetaData{Name: PluginName, Type: "error"},
			},
		}, nil
	}

	// Table level: list ctl, schema, count, and session directories
	if sid == "" && operation == "" {
		// Check if table exists
		exists, err := fs.tableExists(dbName, tableName)
		if err != nil {
			return nil, fmt.Errorf("failed to check table existence: %w", err)
		}
		if !exists {
			return nil, fmt.Errorf("table '%s.%s' does not exist", dbName, tableName)
		}

		entries := []filesystem.FileInfo{
			{
				Name:    "ctl",
				Size:    0,
				Mode:    0444, // read-only (reading creates session)
				ModTime: now,
				IsDir:   false,
				Meta:    filesystem.MetaData{Name: PluginName, Type: "ctl"},
			},
			{
				Name:    "schema",
				Size:    0,
				Mode:    0444, // read-only
				ModTime: now,
				IsDir:   false,
				Meta:    filesystem.MetaData{Name: PluginName, Type: "schema"},
			},
			{
				Name:    "count",
				Size:    0,
				Mode:    0444, // read-only
				ModTime: now,
				IsDir:   false,
				Meta:    filesystem.MetaData{Name: PluginName, Type: "count"},
			},
		}

		// Add active session directories
		sids := fs.sessionManager.ListSessions(dbName, tableName)
		for _, s := range sids {
			entries = append(entries, filesystem.FileInfo{
				Name:    s,
				Size:    0,
				Mode:    0755,
				ModTime: now,
				IsDir:   true,
				Meta:    filesystem.MetaData{Name: PluginName, Type: "session"},
			})
		}

		return entries, nil
	}

	// Session directory: list session files
	if sid != "" && operation == "" {
		session := fs.sessionManager.GetSession(dbName, tableName, sid)
		if session == nil {
			return nil, fmt.Errorf("session not found: %s", sid)
		}

		return []filesystem.FileInfo{
			{
				Name:    "ctl",
				Size:    0,
				Mode:    0222, // write-only (writing closes session)
				ModTime: now,
				IsDir:   false,
				Meta:    filesystem.MetaData{Name: PluginName, Type: "session-ctl"},
			},
			{
				Name:    "query",
				Size:    0,
				Mode:    0222, // write-only
				ModTime: now,
				IsDir:   false,
				Meta:    filesystem.MetaData{Name: PluginName, Type: "query"},
			},
			{
				Name:    "result",
				Size:    0,
				Mode:    0444, // read-only
				ModTime: now,
				IsDir:   false,
				Meta:    filesystem.MetaData{Name: PluginName, Type: "result"},
			},
			{
				Name:    "data",
				Size:    0,
				Mode:    0222, // write-only
				ModTime: now,
				IsDir:   false,
				Meta:    filesystem.MetaData{Name: PluginName, Type: "data"},
			},
			{
				Name:    "error",
				Size:    0,
				Mode:    0444, // read-only
				ModTime: now,
				IsDir:   false,
				Meta:    filesystem.MetaData{Name: PluginName, Type: "error"},
			},
		}, nil
	}

	return nil, fmt.Errorf("not a directory: %s", path)
}

func (fs *sqlfs2FS) Stat(path string) (*filesystem.FileInfo, error) {
	dbName, tableName, sid, operation, err := fs.parsePath(path)
	if err != nil {
		return nil, err
	}

	now := time.Now()

	// Root directory
	if dbName == "" && tableName == "" && sid == "" && operation == "" {
		return &filesystem.FileInfo{
			Name:    "/",
			Size:    0,
			Mode:    0755,
			ModTime: now,
			IsDir:   true,
			Meta:    filesystem.MetaData{Name: PluginName},
		}, nil
	}

	// Root-level ctl file
	if dbName == "" && tableName == "" && sid == "" && operation == "ctl" {
		return &filesystem.FileInfo{
			Name:    "ctl",
			Size:    0,
			Mode:    0444,
			ModTime: now,
			IsDir:   false,
			Meta:    filesystem.MetaData{Name: PluginName, Type: "ctl"},
		}, nil
	}

	// Root-level session directory
	if dbName == "" && tableName == "" && sid != "" && operation == "" {
		session := fs.sessionManager.GetSession("", "", sid)
		if session == nil {
			return nil, fmt.Errorf("session not found: %s", sid)
		}
		return &filesystem.FileInfo{
			Name:    sid,
			Size:    0,
			Mode:    0755,
			ModTime: now,
			IsDir:   true,
			Meta:    filesystem.MetaData{Name: PluginName, Type: "session"},
		}, nil
	}

	// Root-level session files
	if dbName == "" && tableName == "" && sid != "" && operation != "" {
		session := fs.sessionManager.GetSession("", "", sid)
		if session == nil {
			return nil, fmt.Errorf("session not found: %s", sid)
		}
		var mode uint32
		switch operation {
		case "ctl", "query":
			mode = 0222 // write-only
		case "result", "error":
			mode = 0444 // read-only
		default:
			return nil, fmt.Errorf("unknown session file: %s", operation)
		}
		return &filesystem.FileInfo{
			Name:    operation,
			Size:    0,
			Mode:    mode,
			ModTime: now,
			IsDir:   false,
			Meta:    filesystem.MetaData{Name: PluginName, Type: operation},
		}, nil
	}

	// Database directory
	if dbName != "" && tableName == "" && sid == "" && operation == "" {
		return &filesystem.FileInfo{
			Name:    dbName,
			Size:    0,
			Mode:    0755,
			ModTime: now,
			IsDir:   true,
			Meta:    filesystem.MetaData{Name: PluginName, Type: "database"},
		}, nil
	}

	// Database-level ctl file
	if dbName != "" && tableName == "" && sid == "" && operation == "ctl" {
		return &filesystem.FileInfo{
			Name:    "ctl",
			Size:    0,
			Mode:    0444,
			ModTime: now,
			IsDir:   false,
			Meta:    filesystem.MetaData{Name: PluginName, Type: "ctl"},
		}, nil
	}

	// Database-level session directory
	if dbName != "" && tableName == "" && sid != "" && operation == "" {
		session := fs.sessionManager.GetSession(dbName, "", sid)
		if session == nil {
			return nil, fmt.Errorf("session not found: %s", sid)
		}
		return &filesystem.FileInfo{
			Name:    sid,
			Size:    0,
			Mode:    0755,
			ModTime: now,
			IsDir:   true,
			Meta:    filesystem.MetaData{Name: PluginName, Type: "session"},
		}, nil
	}

	// Database-level session files
	if dbName != "" && tableName == "" && sid != "" && operation != "" {
		session := fs.sessionManager.GetSession(dbName, "", sid)
		if session == nil {
			return nil, fmt.Errorf("session not found: %s", sid)
		}
		var mode uint32
		switch operation {
		case "ctl", "query":
			mode = 0222 // write-only
		case "result", "error":
			mode = 0444 // read-only
		default:
			return nil, fmt.Errorf("unknown session file: %s", operation)
		}
		return &filesystem.FileInfo{
			Name:    operation,
			Size:    0,
			Mode:    mode,
			ModTime: now,
			IsDir:   false,
			Meta:    filesystem.MetaData{Name: PluginName, Type: operation},
		}, nil
	}

	// Table directory
	if sid == "" && operation == "" {
		// Check if table exists
		exists, err := fs.tableExists(dbName, tableName)
		if err != nil {
			return nil, fmt.Errorf("failed to check table existence: %w", err)
		}
		if !exists {
			return nil, fmt.Errorf("table '%s.%s' does not exist", dbName, tableName)
		}

		return &filesystem.FileInfo{
			Name:    tableName,
			Size:    0,
			Mode:    0755,
			ModTime: now,
			IsDir:   true,
			Meta:    filesystem.MetaData{Name: PluginName, Type: "table"},
		}, nil
	}

	// Table-level files (ctl, schema, count)
	if sid == "" && operation != "" {
		mode := uint32(0444) // read-only by default
		return &filesystem.FileInfo{
			Name:    operation,
			Size:    0,
			Mode:    mode,
			ModTime: now,
			IsDir:   false,
			Meta:    filesystem.MetaData{Name: PluginName, Type: operation},
		}, nil
	}

	// Session directory
	if sid != "" && operation == "" {
		session := fs.sessionManager.GetSession(dbName, tableName, sid)
		if session == nil {
			return nil, fmt.Errorf("session not found: %s", sid)
		}

		return &filesystem.FileInfo{
			Name:    sid,
			Size:    0,
			Mode:    0755,
			ModTime: now,
			IsDir:   true,
			Meta:    filesystem.MetaData{Name: PluginName, Type: "session"},
		}, nil
	}

	// Session-level files
	if sid != "" && operation != "" {
		session := fs.sessionManager.GetSession(dbName, tableName, sid)
		if session == nil {
			return nil, fmt.Errorf("session not found: %s", sid)
		}

		var mode uint32
		switch operation {
		case "ctl", "query", "data":
			mode = 0222 // write-only
		case "result", "error":
			mode = 0444 // read-only
		default:
			return nil, fmt.Errorf("unknown session file: %s", operation)
		}

		return &filesystem.FileInfo{
			Name:    operation,
			Size:    0,
			Mode:    mode,
			ModTime: now,
			IsDir:   false,
			Meta:    filesystem.MetaData{Name: PluginName, Type: operation},
		}, nil
	}

	return nil, fmt.Errorf("invalid path: %s", path)
}

func (fs *sqlfs2FS) Rename(oldPath, newPath string) error {
	return fmt.Errorf("operation not supported: rename")
}

func (fs *sqlfs2FS) Chmod(path string, mode uint32) error {
	return fmt.Errorf("operation not supported: chmod")
}

func (fs *sqlfs2FS) Open(path string) (io.ReadCloser, error) {
	data, err := fs.Read(path, 0, -1)
	if err != nil && err != io.EOF {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (fs *sqlfs2FS) OpenWrite(path string) (io.WriteCloser, error) {
	return filesystem.NewBufferedWriter(path, fs.Write), nil
}

func getReadme() string {
	return `SQLFS2 Plugin - Plan 9 Style SQL Interface

This plugin provides a Plan 9 style SQL interface through file system operations.
Each SQL session is represented as a directory with control files.

DIRECTORY STRUCTURE:
  /sqlfs2/<dbName>/<tableName>/
    ctl              # Read to create new session, returns session ID
    schema           # Read-only: table structure (CREATE TABLE)
    count            # Read-only: row count
    <sid>/           # Session directory (numeric ID)
      ctl            # Write "close" to close session
      query          # Write SQL to execute
      result         # Read query results (JSON)
      data           # Write JSON to insert
      error          # Read error messages

BASIC WORKFLOW:

  # Create a session
  sid=$(cat /sqlfs2/mydb/users/ctl)

  # Execute query
  echo 'SELECT * FROM users' > /sqlfs2/mydb/users/$sid/query

  # Read results
  cat /sqlfs2/mydb/users/$sid/result

  # Close session
  echo close > /sqlfs2/mydb/users/$sid/ctl
  # or: rm -rf /sqlfs2/mydb/users/$sid

CONFIGURATION:

  SQLite Backend:
  [plugins.sqlfs2]
  enabled = true
  path = "/sqlfs2"

    [plugins.sqlfs2.config]
    backend = "sqlite"
    db_path = "sqlfs2.db"
    session_timeout = "10m"  # Optional: auto-cleanup idle sessions

  MySQL Backend:
  [plugins.sqlfs2]
  enabled = true
  path = "/sqlfs2"

    [plugins.sqlfs2.config]
    backend = "mysql"
    host = "localhost"
    port = "3306"
    user = "root"
    password = "password"
    database = "mydb"

  TiDB Backend:
  [plugins.sqlfs2]
  enabled = true
  path = "/sqlfs2"

    [plugins.sqlfs2.config]
    backend = "tidb"
    host = "127.0.0.1"
    port = "4000"
    user = "root"
    database = "test"
    enable_tls = true  # For TiDB Cloud

USAGE EXAMPLES:

  # View table schema
  cat /sqlfs2/mydb/users/schema

  # Get row count
  cat /sqlfs2/mydb/users/count

  # Create session and query
  sid=$(cat /sqlfs2/mydb/users/ctl)
  echo 'SELECT * FROM users WHERE age > 18' > /sqlfs2/mydb/users/$sid/query
  cat /sqlfs2/mydb/users/$sid/result

  # Execute INSERT/UPDATE/DELETE via query file
  echo 'INSERT INTO users (name, age) VALUES ("Alice", 25)' > /sqlfs2/mydb/users/$sid/query
  cat /sqlfs2/mydb/users/$sid/result  # Shows rows_affected

  # Insert JSON data (single object)
  echo '{"name": "Bob", "age": 30}' > /sqlfs2/mydb/users/$sid/data

  # Insert JSON array (multiple records)
  echo '[{"name": "Carol"}, {"name": "Dave"}]' > /sqlfs2/mydb/users/$sid/data

  # Insert NDJSON stream
  cat <<EOF > /sqlfs2/mydb/users/$sid/data
  {"name": "Eve", "age": 28}
  {"name": "Frank", "age": 35}
  EOF

  # Check for errors
  cat /sqlfs2/mydb/users/$sid/error

  # Close session
  echo close > /sqlfs2/mydb/users/$sid/ctl

  # List databases
  ls /sqlfs2/

  # List tables
  ls /sqlfs2/mydb/

  # List table files and sessions
  ls /sqlfs2/mydb/users/

SESSION MANAGEMENT:

  Sessions are created by reading the table-level ctl file.
  Each session has its own SQL transaction that is committed
  when queries succeed. Sessions can be closed by:
  - Writing "close" to the session's ctl file
  - Removing the session directory (rm -rf /sqlfs2/db/tbl/$sid)
  - Automatic timeout (if session_timeout is configured)

ADVANTAGES:
  - Plan 9 style interface: everything is a file
  - Session-based transactions
  - JSON output for query results
  - Support for SQLite, MySQL, and TiDB backends
  - Auto-generate INSERT from JSON documents
  - NDJSON streaming for large imports
  - Configurable session timeout
`
}

// Ensure SQLFS2Plugin implements ServicePlugin
var _ plugin.ServicePlugin = (*SQLFS2Plugin)(nil)
var _ filesystem.FileSystem = (*sqlfs2FS)(nil)
var _ filesystem.HandleFS = (*sqlfs2FS)(nil)

// ============================================================================
// HandleFS Implementation
// ============================================================================

// SQLFileHandle implements FileHandle using a SQL transaction
type SQLFileHandle struct {
	id        int64
	path      string
	flags     filesystem.OpenFlag
	fs        *sqlfs2FS
	tx        *sql.Tx
	committed bool
	closed    bool
	mu        sync.Mutex

	// Buffer for accumulating writes (for query operations)
	writeBuffer bytes.Buffer
	// Buffer for read results
	readBuffer bytes.Buffer
	readPos    int64

	// Parsed path components
	dbName    string
	tableName string
	sid       string
	operation string
}

// ID returns the unique identifier of this handle
func (h *SQLFileHandle) ID() int64 {
	return h.id
}

// Path returns the file path this handle is associated with
func (h *SQLFileHandle) Path() string {
	return h.path
}

// Flags returns the open flags used when opening this handle
func (h *SQLFileHandle) Flags() filesystem.OpenFlag {
	return h.flags
}

// Read reads up to len(buf) bytes from the current position
func (h *SQLFileHandle) Read(buf []byte) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return 0, fmt.Errorf("handle closed")
	}

	// Check read permission
	accessMode := h.flags & 0x3
	if accessMode != filesystem.O_RDONLY && accessMode != filesystem.O_RDWR {
		return 0, fmt.Errorf("handle not opened for reading")
	}

	// If read buffer is empty, populate it based on operation type
	if h.readBuffer.Len() == 0 && h.readPos == 0 {
		if err := h.populateReadBuffer(); err != nil {
			return 0, err
		}
	}

	data := h.readBuffer.Bytes()
	if h.readPos >= int64(len(data)) {
		return 0, io.EOF
	}

	n := copy(buf, data[h.readPos:])
	h.readPos += int64(n)
	return n, nil
}

// populateReadBuffer fills the read buffer based on the operation type
func (h *SQLFileHandle) populateReadBuffer() error {
	switch h.operation {
	case "ctl":
		// Reading ctl creates a new session and returns the session ID
		// For root-level ctl (no db, no table)
		if h.dbName == "" && h.tableName == "" {
			session, err := h.fs.sessionManager.CreateSession(h.fs.plugin.db, "", "")
			if err != nil {
				return err
			}
			h.readBuffer.WriteString(fmt.Sprintf("%d\n", session.id))
			return nil
		}

		// For table-level ctl
		if h.dbName != "" && h.tableName != "" {
			// Check if table exists
			exists, err := h.fs.tableExists(h.dbName, h.tableName)
			if err != nil {
				return fmt.Errorf("failed to check table existence: %w", err)
			}
			if !exists {
				return fmt.Errorf("table '%s.%s' does not exist", h.dbName, h.tableName)
			}

			// Switch to database if needed
			if err := h.fs.plugin.backend.SwitchDatabase(h.fs.plugin.db, h.dbName); err != nil {
				return err
			}

			// Create new session
			session, err := h.fs.sessionManager.CreateSession(h.fs.plugin.db, h.dbName, h.tableName)
			if err != nil {
				return err
			}
			h.readBuffer.WriteString(fmt.Sprintf("%d\n", session.id))
			return nil
		}

		return fmt.Errorf("invalid path for ctl")

	case "schema":
		if h.dbName == "" || h.tableName == "" {
			return fmt.Errorf("invalid path for schema")
		}
		createTableStmt, err := h.fs.plugin.backend.GetTableSchema(h.fs.plugin.db, h.dbName, h.tableName)
		if err != nil {
			return err
		}
		h.readBuffer.WriteString(createTableStmt + "\n")

	case "count":
		if h.dbName == "" || h.tableName == "" {
			return fmt.Errorf("invalid path for count")
		}
		// Use transaction for count query
		sqlStmt := fmt.Sprintf("SELECT COUNT(*) FROM %s.%s", h.dbName, h.tableName)
		var count int64
		var err error
		if h.tx != nil {
			err = h.tx.QueryRow(sqlStmt).Scan(&count)
		} else {
			err = h.fs.plugin.db.QueryRow(sqlStmt).Scan(&count)
		}
		if err != nil {
			return fmt.Errorf("count query error: %w", err)
		}
		h.readBuffer.WriteString(fmt.Sprintf("%d\n", count))

	case "result":
		// Result is read from session, but for handle-based access we need to get it from somewhere
		// For now, return empty - the session-based Read handles this case
		return nil

	case "error":
		// Error is read from session, but for handle-based access we need to get it from somewhere
		// For now, return empty - the session-based Read handles this case
		return nil

	case "query", "data", "execute", "insert_json":
		// These are write-only operations, return empty
		return nil

	default:
		return fmt.Errorf("unknown operation: %s", h.operation)
	}

	return nil
}

// ReadAt reads len(buf) bytes from the specified offset (pread)
func (h *SQLFileHandle) ReadAt(buf []byte, offset int64) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return 0, fmt.Errorf("handle closed")
	}

	// Check read permission
	accessMode := h.flags & 0x3
	if accessMode != filesystem.O_RDONLY && accessMode != filesystem.O_RDWR {
		return 0, fmt.Errorf("handle not opened for reading")
	}

	// If read buffer is empty, populate it
	if h.readBuffer.Len() == 0 {
		if err := h.populateReadBuffer(); err != nil {
			return 0, err
		}
	}

	data := h.readBuffer.Bytes()
	if offset >= int64(len(data)) {
		return 0, io.EOF
	}

	n := copy(buf, data[offset:])
	return n, nil
}

// Write writes data at the current position (appends to write buffer)
func (h *SQLFileHandle) Write(data []byte) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return 0, fmt.Errorf("handle closed")
	}

	// Check write permission
	accessMode := h.flags & 0x3
	if accessMode != filesystem.O_WRONLY && accessMode != filesystem.O_RDWR {
		return 0, fmt.Errorf("handle not opened for writing")
	}

	if h.operation == "schema" || h.operation == "count" {
		return 0, fmt.Errorf("%s is read-only", h.operation)
	}

	// Append to write buffer
	n, err := h.writeBuffer.Write(data)
	return n, err
}

// WriteAt writes data at the specified offset (pwrite)
func (h *SQLFileHandle) WriteAt(data []byte, offset int64) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return 0, fmt.Errorf("handle closed")
	}

	// Check write permission
	accessMode := h.flags & 0x3
	if accessMode != filesystem.O_WRONLY && accessMode != filesystem.O_RDWR {
		return 0, fmt.Errorf("handle not opened for writing")
	}

	if h.operation == "schema" || h.operation == "count" {
		return 0, fmt.Errorf("%s is read-only", h.operation)
	}

	// For SQL operations, we don't support random writes
	// Just append the data
	n, err := h.writeBuffer.Write(data)
	return n, err
}

// Seek moves the read/write position
func (h *SQLFileHandle) Seek(offset int64, whence int) (int64, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return 0, fmt.Errorf("handle closed")
	}

	// Only support seek for read operations
	data := h.readBuffer.Bytes()
	var newPos int64

	switch whence {
	case io.SeekStart:
		newPos = offset
	case io.SeekCurrent:
		newPos = h.readPos + offset
	case io.SeekEnd:
		newPos = int64(len(data)) + offset
	default:
		return 0, fmt.Errorf("invalid whence: %d", whence)
	}

	if newPos < 0 {
		return 0, fmt.Errorf("negative position")
	}

	h.readPos = newPos
	return h.readPos, nil
}

// Sync executes the buffered SQL and commits the transaction
func (h *SQLFileHandle) Sync() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return fmt.Errorf("handle closed")
	}

	if h.committed {
		return nil // Already committed
	}

	if h.tx == nil {
		return nil // No transaction to commit
	}

	// Execute any buffered SQL statements
	if h.writeBuffer.Len() > 0 {
		if err := h.executeBufferedSQL(); err != nil {
			return err
		}
	}

	// Commit the transaction
	if err := h.tx.Commit(); err != nil {
		return fmt.Errorf("transaction commit failed: %w", err)
	}

	h.committed = true
	log.Debugf("[sqlfs2] Transaction committed for handle %d", h.id)
	return nil
}

// executeBufferedSQL executes the SQL statements in the write buffer
func (h *SQLFileHandle) executeBufferedSQL() error {
	sqlStmt := strings.TrimSpace(h.writeBuffer.String())
	if sqlStmt == "" {
		return nil
	}

	switch h.operation {
	case "query":
		// Execute SELECT query in transaction
		rows, err := h.tx.Query(sqlStmt)
		if err != nil {
			return fmt.Errorf("query error: %w", err)
		}
		defer rows.Close()

		// Get column names
		columns, err := rows.Columns()
		if err != nil {
			return fmt.Errorf("failed to get columns: %w", err)
		}

		// Read all results
		var results []map[string]interface{}
		for rows.Next() {
			values := make([]interface{}, len(columns))
			valuePtrs := make([]interface{}, len(columns))
			for i := range values {
				valuePtrs[i] = &values[i]
			}

			if err := rows.Scan(valuePtrs...); err != nil {
				return fmt.Errorf("scan error: %w", err)
			}

			row := make(map[string]interface{})
			for i, col := range columns {
				val := values[i]
				if b, ok := val.([]byte); ok {
					row[col] = string(b)
				} else {
					row[col] = val
				}
			}
			results = append(results, row)
		}

		if err := rows.Err(); err != nil {
			return fmt.Errorf("rows error: %w", err)
		}

		// Store results in read buffer for subsequent reads
		jsonData, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			return fmt.Errorf("json marshal error: %w", err)
		}
		h.readBuffer.Reset()
		h.readBuffer.Write(jsonData)
		h.readBuffer.WriteString("\n")
		h.readPos = 0

	case "execute":
		// Execute DML statement in transaction
		_, err := h.tx.Exec(sqlStmt)
		if err != nil {
			return fmt.Errorf("execution error: %w", err)
		}

	case "insert_json":
		// Execute JSON insert in transaction
		if err := h.executeInsertJSON(sqlStmt); err != nil {
			return err
		}

	default:
		return fmt.Errorf("unknown operation: %s", h.operation)
	}

	// Clear write buffer after execution
	h.writeBuffer.Reset()
	return nil
}

// executeInsertJSON handles JSON insert operations within the transaction
func (h *SQLFileHandle) executeInsertJSON(data string) error {
	if h.dbName == "" || h.tableName == "" {
		return fmt.Errorf("invalid path for insert_json")
	}

	// Get table columns
	columns, err := h.fs.plugin.backend.GetTableColumns(h.fs.plugin.db, h.dbName, h.tableName)
	if err != nil {
		return fmt.Errorf("failed to get table columns: %w", err)
	}

	if len(columns) == 0 {
		return fmt.Errorf("no columns found for table %s", h.tableName)
	}

	columnNames := make([]string, len(columns))
	for i, col := range columns {
		columnNames[i] = col.Name
	}

	// Parse JSON
	var records []map[string]interface{}
	lines := strings.Split(data, "\n")

	// Check for NDJSON mode
	nonEmptyLines := 0
	firstNonEmptyIdx := -1
	for i, line := range lines {
		if strings.TrimSpace(line) != "" {
			nonEmptyLines++
			if firstNonEmptyIdx == -1 {
				firstNonEmptyIdx = i
			}
		}
	}

	isStreamMode := false
	if nonEmptyLines > 1 && firstNonEmptyIdx >= 0 {
		var testObj map[string]interface{}
		firstLine := strings.TrimSpace(lines[firstNonEmptyIdx])
		if err := json.Unmarshal([]byte(firstLine), &testObj); err == nil {
			isStreamMode = true
		}
	}

	if isStreamMode {
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var record map[string]interface{}
			if err := json.Unmarshal([]byte(line), &record); err != nil {
				continue
			}
			records = append(records, record)
		}
	} else {
		var jsonData interface{}
		if err := json.Unmarshal([]byte(data), &jsonData); err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}

		switch v := jsonData.(type) {
		case map[string]interface{}:
			records = append(records, v)
		case []interface{}:
			for i, item := range v {
				if record, ok := item.(map[string]interface{}); ok {
					records = append(records, record)
				} else {
					return fmt.Errorf("element at index %d is not a JSON object", i)
				}
			}
		default:
			return fmt.Errorf("JSON must be an object or array of objects")
		}
	}

	// Execute inserts in transaction
	for idx, record := range records {
		values := make([]interface{}, len(columnNames))
		for i, colName := range columnNames {
			if val, ok := record[colName]; ok {
				values[i] = val
			} else {
				values[i] = nil
			}
		}

		placeholders := make([]string, len(columnNames))
		for i := range placeholders {
			placeholders[i] = "?"
		}

		insertSQL := fmt.Sprintf("INSERT INTO %s.%s (%s) VALUES (%s)",
			h.dbName, h.tableName,
			strings.Join(columnNames, ", "),
			strings.Join(placeholders, ", "))

		if _, err := h.tx.Exec(insertSQL, values...); err != nil {
			return fmt.Errorf("insert error at record %d: %w", idx+1, err)
		}
	}

	return nil
}

// Close closes the handle and rolls back if not committed
func (h *SQLFileHandle) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return nil
	}

	h.closed = true

	// Rollback if not committed
	if h.tx != nil && !h.committed {
		if err := h.tx.Rollback(); err != nil && err != sql.ErrTxDone {
			log.Warnf("[sqlfs2] Transaction rollback failed for handle %d: %v", h.id, err)
		} else {
			log.Debugf("[sqlfs2] Transaction rolled back for handle %d", h.id)
		}
	}

	// Remove from handles map
	h.fs.handlesMu.Lock()
	delete(h.fs.handles, h.id)
	h.fs.handlesMu.Unlock()

	return nil
}

// Stat returns file information
func (h *SQLFileHandle) Stat() (*filesystem.FileInfo, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return nil, fmt.Errorf("handle closed")
	}

	return h.fs.Stat(h.path)
}

// OpenHandle opens a file and returns a handle with a new transaction
func (fs *sqlfs2FS) OpenHandle(path string, flags filesystem.OpenFlag, mode uint32) (filesystem.FileHandle, error) {
	dbName, tableName, sid, operation, err := fs.parsePath(path)
	if err != nil {
		return nil, err
	}

	// Only support handle operations on operation files
	if operation == "" {
		return nil, fmt.Errorf("cannot open handle on directory: %s", path)
	}

	// Session-related paths do not support HandleFS mode.
	// The session model requires immediate SQL execution on write and reading results
	// from the session state, which is incompatible with HandleFS's buffered I/O model.
	// Return ErrNotSupported so FUSE falls back to using Read/Write methods directly.
	if sid != "" {
		log.Debugf("[sqlfs2] HandleFS not supported for session path: %s (use Read/Write instead)", path)
		return nil, filesystem.ErrNotSupported
	}

	// Check if table exists for table-level operations
	if tableName != "" {
		exists, err := fs.tableExists(dbName, tableName)
		if err != nil {
			return nil, fmt.Errorf("failed to check table existence: %w", err)
		}
		if !exists {
			return nil, fmt.Errorf("table '%s.%s' does not exist", dbName, tableName)
		}
	}

	// Switch to database if needed
	if err := fs.plugin.backend.SwitchDatabase(fs.plugin.db, dbName); err != nil {
		return nil, err
	}

	// Start a new transaction
	tx, err := fs.plugin.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}

	// Create handle with auto-incremented ID
	fs.handlesMu.Lock()
	handleID := fs.nextHandleID
	fs.nextHandleID++

	handle := &SQLFileHandle{
		id:        handleID,
		path:      path,
		flags:     flags,
		fs:        fs,
		tx:        tx,
		dbName:    dbName,
		tableName: tableName,
		sid:       sid,
		operation: operation,
	}

	fs.handles[handleID] = handle
	fs.handlesMu.Unlock()

	log.Debugf("[sqlfs2] Opened handle %d for %s (transaction started)", handleID, path)
	return handle, nil
}

// GetHandle retrieves an existing handle by its ID
func (fs *sqlfs2FS) GetHandle(id int64) (filesystem.FileHandle, error) {
	fs.handlesMu.RLock()
	defer fs.handlesMu.RUnlock()

	handle, exists := fs.handles[id]
	if !exists {
		return nil, filesystem.ErrNotFound
	}

	return handle, nil
}

// CloseHandle closes a handle by its ID
func (fs *sqlfs2FS) CloseHandle(id int64) error {
	fs.handlesMu.RLock()
	handle, exists := fs.handles[id]
	fs.handlesMu.RUnlock()

	if !exists {
		return filesystem.ErrNotFound
	}

	return handle.Close()
}
