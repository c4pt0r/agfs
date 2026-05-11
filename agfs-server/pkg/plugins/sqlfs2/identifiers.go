package sqlfs2

import (
	"fmt"
	"regexp"
	"strings"
)

var sqlIdentifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

const sqlIdentifierRule = "must match [A-Za-z_][A-Za-z0-9_]*"

func validateSQLIdentifier(kind, name string) error {
	if name == "" {
		return fmt.Errorf("invalid SQL %s identifier %q: must not be empty", kind, name)
	}
	if !sqlIdentifierPattern.MatchString(name) {
		return fmt.Errorf("invalid SQL %s identifier %q: %s", kind, name, sqlIdentifierRule)
	}
	return nil
}

func quoteSQLIdentifier(kind, name string) (string, error) {
	if err := validateSQLIdentifier(kind, name); err != nil {
		return "", err
	}
	return "`" + strings.ReplaceAll(name, "`", "``") + "`", nil
}

func qualifiedTableName(dbName, tableName string) (string, error) {
	quotedDB, err := quoteSQLIdentifier("database", dbName)
	if err != nil {
		return "", err
	}
	quotedTable, err := quoteSQLIdentifier("table", tableName)
	if err != nil {
		return "", err
	}
	return quotedDB + "." + quotedTable, nil
}

func countTableSQL(dbName, tableName string) (string, error) {
	table, err := qualifiedTableName(dbName, tableName)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("SELECT COUNT(*) FROM %s", table), nil
}

func dropTableSQL(dbName, tableName string) (string, error) {
	table, err := qualifiedTableName(dbName, tableName)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("DROP TABLE IF EXISTS %s", table), nil
}

func dropDatabaseSQL(backend Backend, dbName string) (string, error) {
	if backend != nil && backend.Name() == "sqlite" {
		return "", fmt.Errorf("drop database is not supported for sqlite backend")
	}
	quotedDB, err := quoteSQLIdentifier("database", dbName)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("DROP DATABASE IF EXISTS %s", quotedDB), nil
}

func quotedColumnNames(columnNames []string) ([]string, error) {
	quotedColumns := make([]string, len(columnNames))
	for i, colName := range columnNames {
		quoted, err := quoteSQLIdentifier("column", colName)
		if err != nil {
			return nil, err
		}
		quotedColumns[i] = quoted
	}
	return quotedColumns, nil
}

func insertRowsSQL(dbName, tableName string, columnNames []string) (string, error) {
	if len(columnNames) == 0 {
		return "", fmt.Errorf("columnNames must not be empty")
	}
	table, err := qualifiedTableName(dbName, tableName)
	if err != nil {
		return "", err
	}
	quotedColumns, err := quotedColumnNames(columnNames)
	if err != nil {
		return "", err
	}
	placeholders := make([]string, len(columnNames))
	for i := range placeholders {
		placeholders[i] = "?"
	}
	return fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		table,
		strings.Join(quotedColumns, ", "),
		strings.Join(placeholders, ", ")), nil
}
