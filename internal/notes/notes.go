// Package notes is LazyAI's durable per-repository state in SQLite: the
// agent's show_locations explanations, the worktrees LazyAI opened (and which
// of them are dormant), and small key/value repo state.
package notes

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	_ "modernc.org/sqlite"

	"lazyai/internal/show"
)

// DB is an open notes store.
type DB struct {
	Path string
	db   *sql.DB
}

// Set is a stored show set.
type Set struct {
	ID        int64
	CreatedAt time.Time
	Root      string
	Branch    string
	SessionID string
	Title     string
	Locations []show.Location
}

const schema = `
CREATE TABLE IF NOT EXISTS show_sets (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	created_at TEXT NOT NULL,
	root       TEXT NOT NULL,
	branch     TEXT NOT NULL,
	session_id TEXT NOT NULL,
	title      TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS show_sets_root ON show_sets(root, created_at);
CREATE TABLE IF NOT EXISTS show_locations (
	id     INTEGER PRIMARY KEY AUTOINCREMENT,
	set_id INTEGER NOT NULL REFERENCES show_sets(id) ON DELETE CASCADE,
	idx    INTEGER NOT NULL,
	path   TEXT NOT NULL,
	abs    TEXT NOT NULL,
	line   INTEGER NOT NULL,
	col    INTEGER NOT NULL,
	note   TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS show_locations_set ON show_locations(set_id, idx);
CREATE TABLE IF NOT EXISTS worktrees (
	repo        TEXT NOT NULL,
	branch      TEXT NOT NULL,
	path        TEXT NOT NULL,
	linked      INTEGER NOT NULL,
	created_at  TEXT NOT NULL,
	last_opened TEXT NOT NULL,
	dormant     INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (repo, branch)
);
CREATE TABLE IF NOT EXISTS repo_state (
	repo  TEXT NOT NULL,
	key   TEXT NOT NULL,
	value TEXT NOT NULL,
	PRIMARY KEY (repo, key)
);
`

// Worktree is a worktree LazyAI has run a workstream in.
type Worktree struct {
	Repo       string // main checkout top level
	Branch     string
	Path       string
	Linked     bool // false for the main checkout itself
	CreatedAt  time.Time
	LastOpened time.Time
	Dormant    bool
}

// DefaultPath is where the store lives unless LAZYAI_DB overrides it:
// $XDG_DATA_HOME/lazyai/lazyai.db (macOS: ~/Library/Application Support).
func DefaultPath() (string, error) {
	if p := os.Getenv("LAZYAI_DB"); p != "" {
		return p, nil
	}
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if runtime.GOOS == "darwin" {
			base = filepath.Join(home, "Library", "Application Support")
		} else {
			base = filepath.Join(home, ".local", "share")
		}
	}
	return filepath.Join(base, "lazyai", "lazyai.db"), nil
}

// Open creates the file and schema if needed.
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(2000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("notes schema: %w", err)
	}
	return &DB{Path: path, db: db}, nil
}

// Close releases the database.
func (d *DB) Close() error {
	if d == nil || d.db == nil {
		return nil
	}
	return d.db.Close()
}

// Record stores one accepted show set.
func (d *DB) Record(root, branch, sessionID string, set show.Set) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	res, err := tx.Exec(`INSERT INTO show_sets(created_at, root, branch, session_id, title) VALUES (?,?,?,?,?)`,
		time.Now().UTC().Format(time.RFC3339Nano), root, branch, sessionID, set.Title)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	for i, l := range set.Locations {
		if _, err := tx.Exec(`INSERT INTO show_locations(set_id, idx, path, abs, line, col, note) VALUES (?,?,?,?,?,?,?)`,
			id, i, l.Path, l.Abs, l.Line, l.Column, l.Note); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Recent returns the newest sets recorded for a workspace root.
func (d *DB) Recent(root string, limit int) ([]Set, error) {
	rows, err := d.db.Query(`SELECT id, created_at, root, branch, session_id, title FROM show_sets WHERE root = ? ORDER BY id DESC LIMIT ?`, root, limit)
	if err != nil {
		return nil, err
	}
	var sets []Set
	for rows.Next() {
		var s Set
		var ts string
		if err := rows.Scan(&s.ID, &ts, &s.Root, &s.Branch, &s.SessionID, &s.Title); err != nil {
			rows.Close()
			return nil, err
		}
		s.CreatedAt, _ = time.Parse(time.RFC3339Nano, ts)
		sets = append(sets, s)
	}
	rows.Close()
	for i := range sets {
		lr, err := d.db.Query(`SELECT path, abs, line, col, note FROM show_locations WHERE set_id = ? ORDER BY idx`, sets[i].ID)
		if err != nil {
			return nil, err
		}
		for lr.Next() {
			var l show.Location
			if err := lr.Scan(&l.Path, &l.Abs, &l.Line, &l.Column, &l.Note); err != nil {
				lr.Close()
				return nil, err
			}
			sets[i].Locations = append(sets[i].Locations, l)
		}
		lr.Close()
	}
	return sets, nil
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// UpsertWorktree records that a workstream is (again) running in a worktree:
// first sight sets created_at; every call bumps last_opened and clears dormant.
func (d *DB) UpsertWorktree(repo, branch, path string, linked bool) error {
	l := 0
	if linked {
		l = 1
	}
	ts := now()
	_, err := d.db.Exec(`INSERT INTO worktrees(repo, branch, path, linked, created_at, last_opened, dormant)
		VALUES (?,?,?,?,?,?,0)
		ON CONFLICT(repo, branch) DO UPDATE SET path = excluded.path, linked = excluded.linked, last_opened = excluded.last_opened, dormant = 0`,
		repo, branch, path, l, ts, ts)
	return err
}

// SetDormant marks a worktree as archived (or wakes it).
func (d *DB) SetDormant(repo, branch string, dormant bool) error {
	v := 0
	if dormant {
		v = 1
	}
	_, err := d.db.Exec(`UPDATE worktrees SET dormant = ? WHERE repo = ? AND branch = ?`, v, repo, branch)
	return err
}

// Worktrees lists every worktree recorded for a repo, most recently opened first.
func (d *DB) Worktrees(repo string) ([]Worktree, error) {
	return d.queryWorktrees(`SELECT repo, branch, path, linked, created_at, last_opened, dormant FROM worktrees WHERE repo = ? ORDER BY last_opened DESC`, repo)
}

// Dormant lists the archived worktrees of a repo.
func (d *DB) Dormant(repo string) ([]Worktree, error) {
	return d.queryWorktrees(`SELECT repo, branch, path, linked, created_at, last_opened, dormant FROM worktrees WHERE repo = ? AND dormant = 1 ORDER BY last_opened DESC`, repo)
}

func (d *DB) queryWorktrees(q string, args ...any) ([]Worktree, error) {
	rows, err := d.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Worktree
	for rows.Next() {
		var w Worktree
		var linked, dormant int
		var created, opened string
		if err := rows.Scan(&w.Repo, &w.Branch, &w.Path, &linked, &created, &opened, &dormant); err != nil {
			return nil, err
		}
		w.Linked, w.Dormant = linked == 1, dormant == 1
		w.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		w.LastOpened, _ = time.Parse(time.RFC3339Nano, opened)
		out = append(out, w)
	}
	return out, rows.Err()
}

// SetState stores a small per-repo value (e.g. the last active branch).
func (d *DB) SetState(repo, key, value string) error {
	_, err := d.db.Exec(`INSERT INTO repo_state(repo, key, value) VALUES (?,?,?)
		ON CONFLICT(repo, key) DO UPDATE SET value = excluded.value`, repo, key, value)
	return err
}

// State reads a per-repo value; missing keys yield "".
func (d *DB) State(repo, key string) (string, error) {
	var v string
	err := d.db.QueryRow(`SELECT value FROM repo_state WHERE repo = ? AND key = ?`, repo, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}
