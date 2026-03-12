# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
go build -o cklogd ./cmd/cklogd        # compile binary
go run ./cmd/cklogd -config cklogd.ini # run
go build ./...                          # verify all packages compile
```

## Architecture

`cklogd` is a daemon that tails configured CSV log files and ingests new lines into monthly-rotated per-file SQLite databases, with optional async HTTP posting.

**Data flow:** `watcher` → `ingester` → `reader` → `store`
                                                  ↘ `poster` (if api_url set)

- **`internal/config`** — loads `cklogd.ini`. Global settings in `[cklogd]` (`dbdir`, `retain_months`, `debug`). Each `[name]` section defines one log: `file`, `max_fields` (default 10), optional `api_url`, `api_auth_type` (`bearer`/`basic`), `api_auth_token`, `api_auth_user`. Optional `[name.columns]` maps 1-based index to column name. Produces `Config{Logs []*LogConfig}`. Validates auth config at load time.

- **`internal/watcher`** — wraps `fsnotify`. `NewMulti(dirs)` watches multiple directories (deduplicated from configured file paths). Emits normalized `Event{Path, Op}`.

- **`internal/ingester`** — owns the event loop. Tracks one `entry` per configured log file (reader + store + poster + active DB path). On startup calls `ScanExisting()` to catch up missed lines. On the 2s ticker: checks for month change → rotates DB (flush → close old store → open new store → `reader.SetStore()`) → deletes DBs older than `retain_months`. Only processes fsnotify events for configured file paths.

- **`internal/reader`** — per-file tail logic. Tracks byte offset and inode; detects rotation (inode change or file shrink). Parses each line as CSV (`TrimLeadingSpace=true`). Buffers 200 lines before flushing. `AfterFlush func([]store.LogLine)` is called with a copy of each flushed batch (used by the poster). `SetStore()` swaps the backing store during monthly rotation without losing in-memory offset.

- **`internal/poster`** — async HTTP POST worker, created per log entry when `api_url` is set. Takes `*config.LogConfig`. Receives `[]store.LogLine` via a buffered channel (512 slots); background goroutine serializes to JSON array of column-keyed objects and POSTs. Applies `Authorization` header based on `api_auth_type`. Drops with a warning if queue is full. `Close()` drains before exit.

- **`internal/store`** — SQLite via `modernc.org/sqlite` (pure Go, no CGo). WAL mode. Schema and INSERT statement are built dynamically from the column name slice. `SaveBatch` atomically inserts a line batch and updates `file_offsets` in one transaction. `migrate()` adds any missing named columns to existing DBs on open.

**SQLite schema per monthly DB:**
- `log_lines(id, filename, line, <col1>, <col2>, …, ingested_at)` — `line` is the raw text; named columns hold parsed CSV fields (NULL if line has fewer fields). `<col1>` has an index.
- `file_offsets(filename, offset, inode, updated_at)` — persists read position across restarts and DB rotations.

**DB naming:** `<section_name>_YYYY_MM.db` — e.g. `cnc1_2026_03.db`.
