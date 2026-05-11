package sqlfs2

import (
	"crypto/tls"
	"database/sql"
	"fmt"
	"regexp"
	"strings"

	"github.com/c4pt0r/agfs/agfs-server/pkg/plugin/config"
	"github.com/go-sql-driver/mysql"
	_ "github.com/go-sql-driver/mysql"
	log "github.com/sirupsen/logrus"
)

// TiDBBackend implements the Backend interface for TiDB
type TiDBBackend struct{}

func (b *TiDBBackend) Name() string {
	return "tidb"
}

func (b *TiDBBackend) Initialize(cfg map[string]interface{}) (*sql.DB, error) {
	// Check if DSN contains tls parameter
	dsnStr := config.GetStringConfig(cfg, "dsn", "")
	dsnHasTLS := strings.Contains(dsnStr, "tls=")

	// Extract TLS config name from DSN if present
	tlsConfigName := "tidb-sqlfs2"
	if dsnHasTLS {
		// Extract tls parameter value from DSN: tls=value
		re := regexp.MustCompile(`tls=([^&]+)`)
		if matches := re.FindStringSubmatch(dsnStr); len(matches) > 1 {
			tlsConfigName = matches[1]
		}
	}

	// Register TLS configuration if needed
	enableTLS := config.GetBoolConfig(cfg, "enable_tls", false) || dsnHasTLS

	if enableTLS {
		// Get TLS configuration
		serverName := config.GetStringConfig(cfg, "tls_server_name", "")

		// If no explicit server name, try to extract from DSN or host
		if serverName == "" {
			if dsnStr != "" {
				// Extract host from DSN: user:pass@tcp(host:port)/db
				re := regexp.MustCompile(`@tcp\(([^:]+):\d+\)`)
				if matches := re.FindStringSubmatch(dsnStr); len(matches) > 1 {
					serverName = matches[1]
				}
			} else {
				// Use host config
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
			log.Warn("[sqlfs2] TLS certificate verification is disabled (insecure)")
		}

		// Register TLS config with MySQL driver
		if err := mysql.RegisterTLSConfig(tlsConfigName, tlsConfig); err != nil {
			log.Warnf("[sqlfs2] Failed to register TLS config (may already exist): %v", err)
		}
	}

	// Parse TiDB connection string
	var dsn string

	if dsnStr != "" {
		dsn = dsnStr
	} else {
		// Build DSN from individual components
		user := config.GetStringConfig(cfg, "user", "root")
		password := config.GetStringConfig(cfg, "password", "")
		host := config.GetStringConfig(cfg, "host", "127.0.0.1")
		port := config.GetStringConfig(cfg, "port", "4000")
		database := config.GetStringConfig(cfg, "database", "test")

		// Build base DSN
		if password != "" {
			dsn = fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True",
				user, password, host, port, database)
		} else {
			dsn = fmt.Sprintf("%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True",
				user, host, port, database)
		}

		// Add TLS parameter if enabled
		if enableTLS {
			dsn += fmt.Sprintf("&tls=%s", tlsConfigName)
		}
	}

	log.Infof("[sqlfs2] Connecting to TiDB (TLS: %v)", enableTLS)

	// Extract database name to create it if needed
	dbName := extractDatabaseName(dsn, config.GetStringConfig(cfg, "database", ""))

	// First, try to connect without database to create it if needed
	if dbName != "" {
		dsnWithoutDB := removeDatabaseFromDSN(dsn)
		if dsnWithoutDB != dsn {
			tempDB, err := sql.Open("mysql", dsnWithoutDB)
			if err == nil {
				defer tempDB.Close()
				// Try to create database if it doesn't exist
				quotedDB, quoteErr := quoteSQLIdentifier("database", dbName)
				if quoteErr != nil {
					err = quoteErr
				} else {
					_, err = tempDB.Exec(fmt.Sprintf("CREATE DATABASE IF NOT EXISTS %s", quotedDB))
				}
				if err != nil {
					log.Warnf("[sqlfs2] Failed to create database '%s': %v", dbName, err)
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

func (b *TiDBBackend) GetTableSchema(db *sql.DB, dbName, tableName string) (string, error) {
	// Switch to database first if needed
	if dbName != "" {
		if err := b.SwitchDatabase(db, dbName); err != nil {
			return "", err
		}
	}

	var tblName, createTableStmt string
	quotedTable, err := quoteSQLIdentifier("table", tableName)
	if err != nil {
		return "", err
	}
	query := fmt.Sprintf("SHOW CREATE TABLE %s", quotedTable)
	err = db.QueryRow(query).Scan(&tblName, &createTableStmt)
	if err != nil {
		return "", fmt.Errorf("failed to get table schema: %w", err)
	}
	return createTableStmt, nil
}

func (b *TiDBBackend) ListDatabases(db *sql.DB) ([]string, error) {
	rows, err := db.Query("SHOW DATABASES")
	if err != nil {
		return nil, fmt.Errorf("failed to list databases: %w", err)
	}
	defer rows.Close()

	var databases []string
	for rows.Next() {
		var dbName string
		if err := rows.Scan(&dbName); err != nil {
			return nil, err
		}
		databases = append(databases, dbName)
	}
	return databases, nil
}

func (b *TiDBBackend) ListTables(db *sql.DB, dbName string) ([]string, error) {
	// Switch to database first
	if err := b.SwitchDatabase(db, dbName); err != nil {
		return nil, err
	}

	rows, err := db.Query("SHOW TABLES")
	if err != nil {
		return nil, fmt.Errorf("failed to list tables: %w", err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var tableName string
		if err := rows.Scan(&tableName); err != nil {
			return nil, err
		}
		tables = append(tables, tableName)
	}
	return tables, nil
}

func (b *TiDBBackend) SwitchDatabase(db *sql.DB, dbName string) error {
	if dbName == "" {
		return nil
	}
	quotedDB, err := quoteSQLIdentifier("database", dbName)
	if err != nil {
		return err
	}
	_, err = db.Exec(fmt.Sprintf("USE %s", quotedDB))
	if err != nil {
		return fmt.Errorf("failed to switch to database %s: %w", dbName, err)
	}
	return nil
}

func (b *TiDBBackend) GetTableColumns(db *sql.DB, dbName, tableName string) ([]ColumnInfo, error) {
	// Switch to database first if needed
	if dbName != "" {
		if err := b.SwitchDatabase(db, dbName); err != nil {
			return nil, err
		}
	}

	quotedTable, err := quoteSQLIdentifier("table", tableName)
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf("SHOW COLUMNS FROM %s", quotedTable)
	rows, err := db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to get table columns: %w", err)
	}
	defer rows.Close()

	var columns []ColumnInfo
	for rows.Next() {
		var field, colType string
		var null, key, extra interface{}
		var dflt interface{}

		if err := rows.Scan(&field, &colType, &null, &key, &dflt, &extra); err != nil {
			return nil, err
		}
		columns = append(columns, ColumnInfo{Name: field, Type: colType})
	}
	return columns, nil
}

// extractDatabaseName extracts database name from DSN or config
func extractDatabaseName(dsn string, configDB string) string {
	if dsn != "" {
		// Extract from DSN: ...)/database?...
		re := regexp.MustCompile(`\)/([^?]+)`)
		if matches := re.FindStringSubmatch(dsn); len(matches) > 1 {
			return matches[1]
		}
	}
	return configDB
}

// removeDatabaseFromDSN removes database name from DSN
func removeDatabaseFromDSN(dsn string) string {
	// Replace )/database? with )/?
	re := regexp.MustCompile(`\)/[^?]+(\?|$)`)
	return re.ReplaceAllString(dsn, ")/$1")
}
