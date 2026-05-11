package sqlfs2

import (
	"database/sql"
	"fmt"

	"github.com/c4pt0r/agfs/agfs-server/pkg/plugin/config"
	_ "github.com/go-sql-driver/mysql"
)

// MySQLBackend implements the Backend interface for MySQL
type MySQLBackend struct{}

func (b *MySQLBackend) Name() string {
	return "mysql"
}

func (b *MySQLBackend) Initialize(cfg map[string]interface{}) (*sql.DB, error) {
	var dsn string
	if dsnStr := config.GetStringConfig(cfg, "dsn", ""); dsnStr != "" {
		dsn = dsnStr
	} else {
		user := config.GetStringConfig(cfg, "user", "root")
		password := config.GetStringConfig(cfg, "password", "")
		host := config.GetStringConfig(cfg, "host", "127.0.0.1")
		port := config.GetStringConfig(cfg, "port", "3306")
		database := config.GetStringConfig(cfg, "database", "")

		if password != "" {
			dsn = fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True",
				user, password, host, port, database)
		} else {
			dsn = fmt.Sprintf("%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True",
				user, host, port, database)
		}
	}

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open MySQL database: %w", err)
	}
	return db, nil
}

func (b *MySQLBackend) GetTableSchema(db *sql.DB, dbName, tableName string) (string, error) {
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

func (b *MySQLBackend) ListDatabases(db *sql.DB) ([]string, error) {
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

func (b *MySQLBackend) ListTables(db *sql.DB, dbName string) ([]string, error) {
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

func (b *MySQLBackend) SwitchDatabase(db *sql.DB, dbName string) error {
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

func (b *MySQLBackend) GetTableColumns(db *sql.DB, dbName, tableName string) ([]ColumnInfo, error) {
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
