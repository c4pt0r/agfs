package queuefs

import (
	"crypto/tls"
	"database/sql"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/c4pt0r/agfs/agfs-server/pkg/plugin/config"
	"github.com/go-sql-driver/mysql"
	_ "github.com/go-sql-driver/mysql" // MySQL/TiDB driver
	_ "github.com/jackc/pgx/v5/stdlib" // PostgreSQL driver
	_ "github.com/mattn/go-sqlite3"    // SQLite driver
	log "github.com/sirupsen/logrus"
)

// DBBackend defines the interface for database operations
type DBBackend interface {
	// Open opens a connection to the database
	Open(cfg map[string]interface{}) (*sql.DB, error)

	// GetInitSQL returns the SQL statements to initialize the schema
	GetInitSQL() []string

	// SupportsSkipLocked reports whether the backend supports FOR UPDATE SKIP LOCKED.
	SupportsSkipLocked() bool

	// QueueTableDDL returns the SQL to create a queue table.
	QueueTableDDL(tableName string) string

	// EnsureQueueIndexes creates any backend-specific queue indexes.
	EnsureQueueIndexes(db *sql.DB, tableName string) error

	// RegistryInsertSQL returns the SQL used to register a queue.
	RegistryInsertSQL() string

	// Rebind rewrites generic placeholders to the backend-specific format.
	Rebind(query string) string

	// BoolLiteral returns the backend-specific SQL literal for a boolean value.
	BoolLiteral(value bool) string
}

// SQLiteDBBackend implements DBBackend for SQLite
type SQLiteDBBackend struct{}

func NewSQLiteDBBackend() *SQLiteDBBackend {
	return &SQLiteDBBackend{}
}

func (b *SQLiteDBBackend) Open(cfg map[string]interface{}) (*sql.DB, error) {
	dbPath := config.GetStringConfig(cfg, "db_path", "queue.db")

	db, err := sql.Open("sqlite3", dbPath)
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

func (b *SQLiteDBBackend) GetInitSQL() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS queuefs_registry (
			queue_name TEXT PRIMARY KEY,
			table_name TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
	}
}

func (b *SQLiteDBBackend) SupportsSkipLocked() bool {
	return false
}

func (b *SQLiteDBBackend) QueueTableDDL(tableName string) string {
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		message_id TEXT NOT NULL,
		data BLOB NOT NULL,
		timestamp INTEGER NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		deleted INTEGER DEFAULT 0,
		deleted_at DATETIME NULL
	)`, tableName)
}

func (b *SQLiteDBBackend) EnsureQueueIndexes(db *sql.DB, tableName string) error {
	indexSQL := fmt.Sprintf(
		"CREATE INDEX IF NOT EXISTS idx_%s_deleted_id ON %s(deleted, id)",
		strings.TrimPrefix(tableName, "queuefs_queue_"),
		tableName,
	)
	_, err := db.Exec(indexSQL)
	if err != nil {
		return fmt.Errorf("failed to create queue index: %w", err)
	}
	return nil
}

func (b *SQLiteDBBackend) RegistryInsertSQL() string {
	return "INSERT OR IGNORE INTO queuefs_registry (queue_name, table_name) VALUES (?, ?)"
}

func (b *SQLiteDBBackend) Rebind(query string) string {
	return query
}

func (b *SQLiteDBBackend) BoolLiteral(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

// TiDBDBBackend implements DBBackend for TiDB
type TiDBDBBackend struct{}

func NewTiDBDBBackend() *TiDBDBBackend {
	return &TiDBDBBackend{}
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

func (b *TiDBDBBackend) SupportsSkipLocked() bool {
	return true
}

func (b *TiDBDBBackend) QueueTableDDL(tableName string) string {
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		id BIGINT AUTO_INCREMENT PRIMARY KEY,
		message_id VARCHAR(64) NOT NULL,
		data LONGBLOB NOT NULL,
		timestamp BIGINT NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		deleted TINYINT(1) DEFAULT 0,
		deleted_at TIMESTAMP NULL,
		INDEX idx_deleted_id (deleted, id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`, tableName)
}

func (b *TiDBDBBackend) EnsureQueueIndexes(db *sql.DB, tableName string) error {
	return nil
}

func (b *TiDBDBBackend) RegistryInsertSQL() string {
	return "INSERT IGNORE INTO queuefs_registry (queue_name, table_name) VALUES (?, ?)"
}

func (b *TiDBDBBackend) Rebind(query string) string {
	return query
}

func (b *TiDBDBBackend) BoolLiteral(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

// PostgreSQLDBBackend implements DBBackend for PostgreSQL.
type PostgreSQLDBBackend struct{}

func NewPostgreSQLDBBackend() *PostgreSQLDBBackend {
	return &PostgreSQLDBBackend{}
}

func (b *PostgreSQLDBBackend) Open(cfg map[string]interface{}) (*sql.DB, error) {
	dsn := config.GetStringConfig(cfg, "dsn", "")
	database := config.GetStringConfig(cfg, "database", "queuedb")
	if dsn == "" {
		host := config.GetStringConfig(cfg, "host", "127.0.0.1")
		port := config.GetStringConfig(cfg, "port", "5432")
		user := config.GetStringConfig(cfg, "user", "postgres")
		password := config.GetStringConfig(cfg, "password", "")
		sslMode := "disable"
		if config.GetBoolConfig(cfg, "enable_tls", false) {
			sslMode = "require"
			if config.GetBoolConfig(cfg, "tls_skip_verify", false) {
				sslMode = "prefer"
			}
		}

		dsn = fmt.Sprintf("host=%s port=%s user=%s dbname=%s sslmode=%s",
			host, port, user, database, sslMode)
		if password != "" {
			dsn += fmt.Sprintf(" password=%s", password)
		}
		if serverName := config.GetStringConfig(cfg, "tls_server_name", ""); serverName != "" {
			dsn += fmt.Sprintf(" host=%s", serverName)
		}
	}

	adminDSN := config.GetStringConfig(cfg, "admin_dsn", "")
	if adminDSN == "" {
		adminDSN = config.GetStringConfig(cfg, "dsn", "")
	}
	if adminDSN == "" && database != "" {
		host := config.GetStringConfig(cfg, "host", "127.0.0.1")
		port := config.GetStringConfig(cfg, "port", "5432")
		user := config.GetStringConfig(cfg, "user", "postgres")
		password := config.GetStringConfig(cfg, "password", "")
		sslMode := "disable"
		if config.GetBoolConfig(cfg, "enable_tls", false) {
			sslMode = "require"
			if config.GetBoolConfig(cfg, "tls_skip_verify", false) {
				sslMode = "prefer"
			}
		}
		adminDSN = fmt.Sprintf("host=%s port=%s user=%s dbname=postgres sslmode=%s",
			host, port, user, sslMode)
		if password != "" {
			adminDSN += fmt.Sprintf(" password=%s", password)
		}
	}

	if adminDSN != "" && database != "" {
		tempDB, err := sql.Open("pgx", adminDSN)
		if err == nil {
			defer tempDB.Close()
			_, err = tempDB.Exec(fmt.Sprintf("CREATE DATABASE %s", quotePostgresIdentifier(database)))
			if err != nil && !strings.Contains(err.Error(), "already exists") {
				log.Warnf("[queuefs] Failed to create PostgreSQL database %q: %v", database, err)
			} else {
				log.Infof("[queuefs] PostgreSQL database %q created or already exists", database)
			}
		}
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open PostgreSQL database: %w", err)
	}

	db.SetMaxOpenConns(100)
	db.SetMaxIdleConns(10)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping PostgreSQL database: %w", err)
	}

	return db, nil
}

func (b *PostgreSQLDBBackend) GetInitSQL() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS queuefs_registry (
			queue_name TEXT PRIMARY KEY,
			table_name TEXT NOT NULL,
			created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
		)`,
	}
}

func (b *PostgreSQLDBBackend) SupportsSkipLocked() bool {
	return true
}

func (b *PostgreSQLDBBackend) QueueTableDDL(tableName string) string {
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		id BIGSERIAL PRIMARY KEY,
		message_id TEXT NOT NULL,
		data BYTEA NOT NULL,
		timestamp BIGINT NOT NULL,
		created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
		deleted BOOLEAN DEFAULT FALSE,
		deleted_at TIMESTAMPTZ NULL
	)`, tableName)
}

func (b *PostgreSQLDBBackend) EnsureQueueIndexes(db *sql.DB, tableName string) error {
	indexName := fmt.Sprintf("idx_%s_deleted_id", strings.TrimPrefix(tableName, "queuefs_queue_"))
	indexSQL := fmt.Sprintf(
		"CREATE INDEX IF NOT EXISTS %s ON %s(deleted, id)",
		quotePostgresIdentifier(indexName),
		tableName,
	)
	_, err := db.Exec(indexSQL)
	if err != nil {
		return fmt.Errorf("failed to create queue index: %w", err)
	}
	return nil
}

func (b *PostgreSQLDBBackend) RegistryInsertSQL() string {
	return `INSERT INTO queuefs_registry (queue_name, table_name) VALUES (?, ?) ON CONFLICT (queue_name) DO NOTHING`
}

func (b *PostgreSQLDBBackend) Rebind(query string) string {
	var builder strings.Builder
	builder.Grow(len(query) + 8)
	argIndex := 1
	for _, ch := range query {
		if ch == '?' {
			builder.WriteByte('$')
			builder.WriteString(strconv.Itoa(argIndex))
			argIndex++
			continue
		}
		builder.WriteRune(ch)
	}
	return builder.String()
}

func (b *PostgreSQLDBBackend) BoolLiteral(value bool) string {
	if value {
		return "TRUE"
	}
	return "FALSE"
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

func quotePostgresIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

// CreateBackend creates the appropriate database backend
func CreateBackend(cfg map[string]interface{}) (DBBackend, error) {
	backendType := config.GetStringConfig(cfg, "backend", "memory")

	switch backendType {
	case "sqlite", "sqlite3":
		return NewSQLiteDBBackend(), nil
	case "tidb", "mysql":
		return NewTiDBDBBackend(), nil
	case "pgsql", "postgres", "postgresql":
		return NewPostgreSQLDBBackend(), nil
	default:
		return nil, fmt.Errorf("unsupported database backend: %s", backendType)
	}
}
