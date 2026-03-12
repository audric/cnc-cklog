package store

import (
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

func buildSchema(columns []string) string {
	cols := ""
	for _, name := range columns {
		cols += fmt.Sprintf("\t%-20s TEXT,\n", name)
	}
	return fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS log_lines (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	filename    TEXT    NOT NULL,
	line        TEXT    NOT NULL,
%s	ingested_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_log_lines_filename    ON log_lines (filename);
CREATE INDEX IF NOT EXISTS idx_log_lines_ingested_at ON log_lines (ingested_at);
CREATE INDEX IF NOT EXISTS idx_log_lines_%s ON log_lines (%s);

CREATE TABLE IF NOT EXISTS file_offsets (
	filename   TEXT    PRIMARY KEY,
	offset     INTEGER NOT NULL DEFAULT 0,
	inode      INTEGER NOT NULL DEFAULT 0,
	updated_at DATETIME NOT NULL
);
`, cols, columns[0], columns[0])
}

func buildInsertSQL(columns []string) string {
	names := "filename, line, " + strings.Join(columns, ", ") + ", ingested_at"
	placeholders := strings.Repeat("?, ", len(columns)+2) + "?"
	return fmt.Sprintf("INSERT INTO log_lines (%s) VALUES (%s)", names, placeholders)
}

type LogLine struct {
	Filename   string
	Line       string
	Fields     []string // parsed CSV fields; nil if line couldn't be parsed
	IngestedAt time.Time
}

type FileOffset struct {
	Offset uint64
	Inode  uint64
}

type Store struct {
	db        *sql.DB
	mu        sync.Mutex
	columns   []string
	insertSQL string
}

func Open(path string, columns []string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_synchronous=NORMAL")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(buildSchema(columns)); err != nil {
		return nil, fmt.Errorf("init schema: %w", err)
	}
	if err := migrate(db, columns); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{
		db:        db,
		columns:   columns,
		insertSQL: buildInsertSQL(columns),
	}, nil
}

// migrate adds any missing columns to an existing database.
func migrate(db *sql.DB, columns []string) error {
	rows, err := db.Query(`PRAGMA table_info(log_lines)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	existing := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return err
		}
		existing[name] = true
	}

	for _, col := range columns {
		if !existing[col] {
			if _, err := db.Exec(fmt.Sprintf("ALTER TABLE log_lines ADD COLUMN %s TEXT", col)); err != nil {
				if !strings.Contains(err.Error(), "duplicate column") {
					return err
				}
			}
		}
	}
	return nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

// SaveBatch inserts lines and updates the file offset atomically.
func (s *Store) SaveBatch(lines []LogLine, filename string, offset, inode uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(s.insertSQL)
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := time.Now().UTC()
	for _, l := range lines {
		ts := l.IngestedAt
		if ts.IsZero() {
			ts = now
		}
		args := make([]any, 0, 2+len(s.columns)+1)
		args = append(args, l.Filename, l.Line)
		for i := range s.columns {
			if i < len(l.Fields) {
				args = append(args, l.Fields[i])
			} else {
				args = append(args, nil)
			}
		}
		args = append(args, ts.Format(time.RFC3339Nano))
		if _, err := stmt.Exec(args...); err != nil {
			return err
		}
	}

	_, err = tx.Exec(
		`INSERT INTO file_offsets (filename, offset, inode, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(filename) DO UPDATE SET offset=excluded.offset, inode=excluded.inode, updated_at=excluded.updated_at`,
		filename, offset, inode, now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// GetOffset returns the stored offset and inode for a file, or zeros if unknown.
func (s *Store) GetOffset(filename string) (FileOffset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var fo FileOffset
	err := s.db.QueryRow(
		`SELECT offset, inode FROM file_offsets WHERE filename = ?`, filename,
	).Scan(&fo.Offset, &fo.Inode)
	if err == sql.ErrNoRows {
		return FileOffset{}, nil
	}
	return fo, err
}
