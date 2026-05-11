package sqlfs2

import (
	"database/sql"
	"fmt"

	"github.com/c4pt0r/agfs/agfs-server/pkg/plugin/config"
	_ "github.com/mattn/go-sqlite3"
)

// SQLiteBackend implements the Backend interface for SQLite
type SQLiteBackend struct{}

func (b *SQLiteBackend) Name() string {
	return "sqlite"
}

func (b *SQLiteBackend) Initialize(cfg map[string]interface{}) (*sql.DB, error) {
	dbPath := config.GetStringConfig(cfg, "db_path", "sqlfs2.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open SQLite database: %w", err)
	}
	return db, nil
}

func (b *SQLiteBackend) GetTableSchema(db *sql.DB, dbName, tableName string) (string, error) {
	if err := validateSQLIdentifier("table", tableName); err != nil {
		return "", err
	}
	var createTableStmt string
	query := "SELECT sql FROM sqlite_master WHERE type='table' AND name=?"
	err := db.QueryRow(query, tableName).Scan(&createTableStmt)
	if err != nil {
		return "", fmt.Errorf("failed to get table schema: %w", err)
	}
	return createTableStmt, nil
}

func (b *SQLiteBackend) ListDatabases(db *sql.DB) ([]string, error) {
	// SQLite only has one main database
	return []string{"main"}, nil
}

func (b *SQLiteBackend) ListTables(db *sql.DB, dbName string) ([]string, error) {
	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type='table' ORDER BY name")
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

func (b *SQLiteBackend) SwitchDatabase(db *sql.DB, dbName string) error {
	// SQLite doesn't need to switch databases
	return nil
}

func (b *SQLiteBackend) GetTableColumns(db *sql.DB, dbName, tableName string) ([]ColumnInfo, error) {
	quotedTable, err := quoteSQLIdentifier("table", tableName)
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf("PRAGMA table_info(%s)", quotedTable)
	rows, err := db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to get table columns: %w", err)
	}
	defer rows.Close()

	var columns []ColumnInfo
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dfltValue interface{}

		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			return nil, err
		}
		columns = append(columns, ColumnInfo{Name: name, Type: colType})
	}
	return columns, nil
}
