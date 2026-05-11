package queuefs

import (
	"crypto/tls"
	"database/sql"
	"fmt"
	"regexp"
	"strings"

	"github.com/c4pt0r/agfs/agfs-server/pkg/plugin/config"
	"github.com/go-sql-driver/mysql"
	_ "github.com/go-sql-driver/mysql" // MySQL/TiDB driver
	_ "github.com/mattn/go-sqlite3"    // SQLite driver
	log "github.com/sirupsen/logrus"
)

// DBBackend defines the interface for database operations
type DBBackend interface {
	// Open opens a connection to the database
	Open(cfg map[string]interface{}) (*sql.DB, error)

	// GetInitSQL returns the SQL statements to initialize the schema
	GetInitSQL() []string

	// GetCreateQueueSQL returns the SQL statements to create a queue table.
	GetCreateQueueSQL(tableName string) []string

	// GetRegisterQueueSQL returns the SQL used to register a queue/table pair.
	GetRegisterQueueSQL() string

	// GetDequeueQuery returns the SQL used to claim the next queue message.
	GetDequeueQuery(tableName string) string

	// GetAtomicDequeueQuery returns SQL that atomically marks the next message
	// deleted and returns its data. Backends that cannot express this in one
	// statement return ok=false and use GetDequeueQuery inside a transaction.
	GetAtomicDequeueQuery(tableName string) (query string, ok bool)

	// GetDriverName returns the driver name
	GetDriverName() string
}

// SQLiteDBBackend implements DBBackend for SQLite
type SQLiteDBBackend struct{}

func NewSQLiteDBBackend() *SQLiteDBBackend {
	return &SQLiteDBBackend{}
}

func (b *SQLiteDBBackend) GetDriverName() string {
	return "sqlite3"
}

func (b *SQLiteDBBackend) Open(cfg map[string]interface{}) (*sql.DB, error) {
	dbPath := config.GetStringConfig(cfg, "db_path", "queue.db")

	db, err := sql.Open("sqlite3", sqliteDSNWithDefaultBusyTimeout(dbPath))
	if err != nil {
		return nil, fmt.Errorf("failed to open SQLite database: %w", err)
	}

	// Enable WAL mode for better concurrency
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to enable WAL mode: %w", err)
	}
	return db, nil
}

func sqliteDSNWithDefaultBusyTimeout(dsn string) string {
	if strings.Contains(dsn, "_busy_timeout=") || strings.Contains(dsn, "busy_timeout=") {
		return dsn
	}

	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	return dsn + separator + "_busy_timeout=5000"
}

func (b *SQLiteDBBackend) GetInitSQL() []string {
	return []string{
		// Queue registry table to track all queues and their backing tables.
		`CREATE TABLE IF NOT EXISTS queuefs_registry (
			queue_name TEXT PRIMARY KEY,
			table_name TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
	}
}

func (b *SQLiteDBBackend) GetCreateQueueSQL(tableName string) []string {
	return []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			message_id TEXT NOT NULL,
			data TEXT NOT NULL,
			timestamp INTEGER NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			deleted INTEGER DEFAULT 0,
			deleted_at DATETIME
		)`, tableName),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_%s_deleted_id ON %s (deleted, id)`, tableName, tableName),
	}
}

func (b *SQLiteDBBackend) GetRegisterQueueSQL() string {
	return "INSERT OR IGNORE INTO queuefs_registry (queue_name, table_name) VALUES (?, ?)"
}

func (b *SQLiteDBBackend) GetDequeueQuery(tableName string) string {
	return fmt.Sprintf("SELECT id, data FROM %s WHERE deleted = 0 ORDER BY id LIMIT 1", tableName)
}

func (b *SQLiteDBBackend) GetAtomicDequeueQuery(tableName string) (string, bool) {
	return fmt.Sprintf(`UPDATE %s
		SET deleted = 1, deleted_at = CURRENT_TIMESTAMP
		WHERE id = (
			SELECT id FROM %s
			WHERE deleted = 0
			ORDER BY id
			LIMIT 1
		)
		RETURNING data`, tableName, tableName), true
}

// TiDBDBBackend implements DBBackend for TiDB
type TiDBDBBackend struct{}

func NewTiDBDBBackend() *TiDBDBBackend {
	return &TiDBDBBackend{}
}

func (b *TiDBDBBackend) GetDriverName() string {
	return "mysql"
}

func (b *TiDBDBBackend) Open(cfg map[string]interface{}) (*sql.DB, error) {
	// Check if DSN contains tls parameter
	dsnStr := config.GetStringConfig(cfg, "dsn", "")
	dsnHasTLS := strings.Contains(dsnStr, "tls=")

	// Register TLS configuration if needed
	enableTLS := config.GetBoolConfig(cfg, "enable_tls", false) || dsnHasTLS
	tlsConfigName := "tidb-queuefs"

	if enableTLS {
		// Get TLS configuration
		serverName := config.GetStringConfig(cfg, "tls_server_name", "")

		// If no explicit server name, try to extract from DSN or host
		if serverName == "" {
			if dsnStr != "" {
				// Extract host from DSN
				re := regexp.MustCompile(`@tcp\(([^:]+):\d+\)`)
				if matches := re.FindStringSubmatch(dsnStr); len(matches) > 1 {
					serverName = matches[1]
				}
			} else {
				serverName = config.GetStringConfig(cfg, "host", "")
			}
		}

		skipVerify := config.GetBoolConfig(cfg, "tls_skip_verify", false)

		tlsConfig := &tls.Config{
			MinVersion: tls.VersionTLS12,
		}

		if serverName != "" {
			tlsConfig.ServerName = serverName
		}

		if skipVerify {
			tlsConfig.InsecureSkipVerify = true
			log.Warn("[queuefs] TLS certificate verification is disabled (insecure)")
		}

		// Register TLS config
		if err := mysql.RegisterTLSConfig(tlsConfigName, tlsConfig); err != nil {
			log.Warnf("[queuefs] Failed to register TLS config (may already exist): %v", err)
		}
	}

	// Build DSN
	var dsn string

	if dsnStr != "" {
		dsn = dsnStr
	} else {
		user := config.GetStringConfig(cfg, "user", "root")
		password := config.GetStringConfig(cfg, "password", "")
		host := config.GetStringConfig(cfg, "host", "127.0.0.1")
		port := config.GetStringConfig(cfg, "port", "4000")
		database := config.GetStringConfig(cfg, "database", "queuedb")

		if password != "" {
			dsn = fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True",
				user, password, host, port, database)
		} else {
			dsn = fmt.Sprintf("%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True",
				user, host, port, database)
		}

		if enableTLS {
			dsn += fmt.Sprintf("&tls=%s", tlsConfigName)
		}
	}

	log.Infof("[queuefs] Connecting to TiDB (TLS: %v)", enableTLS)

	// Extract database name
	dbName := extractDatabaseName(dsn, config.GetStringConfig(cfg, "database", ""))

	// Create database if needed
	if dbName != "" {
		dsnWithoutDB := removeDatabaseFromDSN(dsn)
		if dsnWithoutDB != dsn {
			tempDB, err := sql.Open("mysql", dsnWithoutDB)
			if err == nil {
				defer tempDB.Close()
				_, err = tempDB.Exec(fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s`", dbName))
				if err != nil {
					log.Warnf("[queuefs] Failed to create database '%s': %v", dbName, err)
				} else {
					log.Infof("[queuefs] Database '%s' created or already exists", dbName)
				}
			}
		}
	}

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open TiDB database: %w", err)
	}

	// Set connection pool parameters
	db.SetMaxOpenConns(100)
	db.SetMaxIdleConns(10)

	// Test connection
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping TiDB database: %w", err)
	}

	return db, nil
}

func (b *TiDBDBBackend) GetInitSQL() []string {
	return []string{
		// Queue registry table to track all queue tables
		`CREATE TABLE IF NOT EXISTS queuefs_registry (
			queue_name VARCHAR(255) PRIMARY KEY,
			table_name VARCHAR(255) NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	}
}

func (b *TiDBDBBackend) GetCreateQueueSQL(tableName string) []string {
	return []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			message_id VARCHAR(64) NOT NULL,
			data LONGBLOB NOT NULL,
			timestamp BIGINT NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			deleted TINYINT(1) DEFAULT 0,
			deleted_at TIMESTAMP NULL,
			INDEX idx_deleted_id (deleted, id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`, tableName),
	}
}

func (b *TiDBDBBackend) GetRegisterQueueSQL() string {
	return "INSERT IGNORE INTO queuefs_registry (queue_name, table_name) VALUES (?, ?)"
}

func (b *TiDBDBBackend) GetDequeueQuery(tableName string) string {
	return fmt.Sprintf("SELECT id, data FROM %s WHERE deleted = 0 ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED", tableName)
}

func (b *TiDBDBBackend) GetAtomicDequeueQuery(tableName string) (string, bool) {
	return "", false
}

// Helper functions

func extractDatabaseName(dsn string, configDB string) string {
	if dsn != "" {
		re := regexp.MustCompile(`\)/([^?]+)`)
		if matches := re.FindStringSubmatch(dsn); len(matches) > 1 {
			return matches[1]
		}
	}
	return configDB
}

func removeDatabaseFromDSN(dsn string) string {
	re := regexp.MustCompile(`\)/[^?]+(\?|$)`)
	return re.ReplaceAllString(dsn, ")/$1")
}

// sanitizeTableName converts a queue name to a safe table name
// Replaces / with _ and ensures the name is safe for SQL
func sanitizeTableName(queueName string) string {
	// Replace forward slashes with underscores
	tableName := strings.ReplaceAll(queueName, "/", "_")

	// Replace any other potentially problematic characters
	tableName = strings.ReplaceAll(tableName, "-", "_")
	tableName = strings.ReplaceAll(tableName, ".", "_")

	// Prefix with queuefs_queue_ to avoid conflicts with system tables
	return "queuefs_queue_" + tableName
}

// CreateBackend creates the appropriate database backend
func CreateBackend(cfg map[string]interface{}) (DBBackend, error) {
	backendType := config.GetStringConfig(cfg, "backend", "memory")

	switch backendType {
	case "sqlite", "sqlite3":
		return NewSQLiteDBBackend(), nil
	case "tidb", "mysql":
		return NewTiDBDBBackend(), nil
	default:
		return nil, fmt.Errorf("unsupported database backend: %s", backendType)
	}
}
