package sqlfs2

import (
	"database/sql"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/c4pt0r/agfs/agfs-server/pkg/filesystem"
)

func newSQLiteSQLFS2ForTest(t *testing.T) (*SQLFS2Plugin, *sqlfs2FS) {
	t.Helper()

	plugin := NewSQLFS2Plugin()
	err := plugin.Initialize(map[string]interface{}{
		"backend": "sqlite",
		"db_path": filepath.Join(t.TempDir(), "sqlfs2.db"),
	})
	if err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	t.Cleanup(func() {
		if err := plugin.Shutdown(); err != nil {
			t.Fatalf("Shutdown() error = %v", err)
		}
	})

	fs, ok := plugin.GetFileSystem().(*sqlfs2FS)
	if !ok {
		t.Fatalf("GetFileSystem() returned %T, want *sqlfs2FS", plugin.GetFileSystem())
	}
	return plugin, fs
}

func mustExecSQL(t *testing.T, db *sql.DB, stmt string, args ...interface{}) {
	t.Helper()
	if _, err := db.Exec(stmt, args...); err != nil {
		t.Fatalf("Exec(%q) error = %v", stmt, err)
	}
}

func tableExistsInSQLite(t *testing.T, db *sql.DB, tableName string) bool {
	t.Helper()
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", tableName).Scan(&count)
	if err != nil {
		t.Fatalf("table existence query error = %v", err)
	}
	return count > 0
}

func requireInvalidIdentifierError(t *testing.T, err error, wantKind string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected invalid identifier error, got nil")
	}
	if !errors.Is(err, filesystem.ErrInvalidArgument) {
		t.Fatalf("expected filesystem.ErrInvalidArgument, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "invalid SQL "+wantKind+" identifier") {
		t.Fatalf("expected invalid SQL %s identifier error, got %v", wantKind, err)
	}
}

func TestSQLFS2IdentifierSQLBuilders(t *testing.T) {
	dropSQL, err := dropTableSQL("main", "users")
	if err != nil {
		t.Fatalf("dropTableSQL() error = %v", err)
	}
	if want := "DROP TABLE IF EXISTS `main`.`users`"; dropSQL != want {
		t.Fatalf("dropTableSQL() = %q, want %q", dropSQL, want)
	}

	insertSQL, err := insertRowsSQL("main", "users", []string{"id", "user_name"})
	if err != nil {
		t.Fatalf("insertRowsSQL() error = %v", err)
	}
	if want := "INSERT INTO `main`.`users` (`id`, `user_name`) VALUES (?, ?)"; insertSQL != want {
		t.Fatalf("insertRowsSQL() = %q, want %q", insertSQL, want)
	}

	if _, err := dropTableSQL("main", "users; DROP TABLE safe"); err == nil {
		t.Fatal("dropTableSQL() accepted hostile table identifier")
	}
	if _, err := insertRowsSQL("main", "users", []string{"id", "name; DROP TABLE safe"}); err == nil {
		t.Fatal("insertRowsSQL() accepted hostile column identifier")
	}
}

func TestSQLFS2SQLiteCountAndDropTableUseValidatedQuotedIdentifiers(t *testing.T) {
	plugin, fs := newSQLiteSQLFS2ForTest(t)
	mustExecSQL(t, plugin.db, "CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)")
	mustExecSQL(t, plugin.db, "INSERT INTO users (name) VALUES (?), (?)", "Alice", "Bob")

	data, err := fs.Read("/main/users/count", 0, -1)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("Read(count) error = %v", err)
	}
	if string(data) != "2\n" {
		t.Fatalf("Read(count) = %q, want %q", data, "2\n")
	}

	if err := fs.RemoveAll("/main/users"); err != nil {
		t.Fatalf("RemoveAll(table) error = %v", err)
	}
	if tableExistsInSQLite(t, plugin.db, "users") {
		t.Fatal("users table still exists after RemoveAll")
	}
}

func TestSQLFS2RejectsHostilePathIdentifiersBeforeSQL(t *testing.T) {
	plugin, fs := newSQLiteSQLFS2ForTest(t)
	mustExecSQL(t, plugin.db, "CREATE TABLE safe (id INTEGER)")

	_, err := fs.Read("/main/safe; DROP TABLE safe/count", 0, -1)
	requireInvalidIdentifierError(t, err, "table")
	if !tableExistsInSQLite(t, plugin.db, "safe") {
		t.Fatal("safe table was dropped after hostile count path")
	}

	err = fs.RemoveAll("/main; DROP DATABASE main/safe")
	requireInvalidIdentifierError(t, err, "database")
	if !tableExistsInSQLite(t, plugin.db, "safe") {
		t.Fatal("safe table was dropped after hostile database path")
	}

	err = fs.RemoveAll("/main/safe; DROP TABLE safe")
	requireInvalidIdentifierError(t, err, "table")
	if !tableExistsInSQLite(t, plugin.db, "safe") {
		t.Fatal("safe table was dropped after hostile drop-table path")
	}
}

func TestSQLFS2SQLiteDropDatabaseIsExplicitlyUnsupported(t *testing.T) {
	plugin, fs := newSQLiteSQLFS2ForTest(t)
	mustExecSQL(t, plugin.db, "CREATE TABLE safe (id INTEGER)")

	err := fs.RemoveAll("/main")
	if err == nil {
		t.Fatal("RemoveAll(/main) unexpectedly succeeded on sqlite backend")
	}
	if !strings.Contains(err.Error(), "drop database is not supported for sqlite backend") {
		t.Fatalf("RemoveAll(/main) error = %v, want sqlite unsupported error", err)
	}
	if !tableExistsInSQLite(t, plugin.db, "safe") {
		t.Fatal("safe table was dropped after sqlite database remove")
	}
}
