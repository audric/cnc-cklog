# cnc-cklog

Daemon that watches configured CSV log files and ingests new lines into monthly-rotated SQLite databases, with optional async HTTP POST to a remote API. Each log file has its own schema and its own set of databases.

`cnc1.log` → `cnc1_2026_03.db`, `cnc1_2026_04.db`, …

## Build

```bash
go build -o cklogd ./cmd/cklogd
go build -o mazak-logger ./cmd/mazak-logger

# focas-logger requires CGo and the Fanuc FOCAS2 SDK
CGO_ENABLED=1 go build -o focas-logger ./cmd/focas-logger
```

## Configuration

All configuration lives in `cklogd.ini` (override with `-config`).

```ini
[cklogd]
dbdir         = .      ; directory where .db files are written
retain_months = 24     ; how many monthly DBs to keep per log
debug         = false

[cnc1]
file       = cnclogs/cnc1.log
max_fields = 4

[cnc1.columns]
1 = event
2 = program
3 = ip
4 = timestamp
```

**Optional per-log keys:**

| Key | Used by | Purpose |
|-----|---------|---------|
| `api_url`, `api_auth_type`, `api_auth_token`, `api_auth_user` | `cklogd` | POST each batch to an HTTP endpoint |
| `focas_host`, `focas_port`, `machine_ip`, `machine_name`, `poll_interval` | `focas-logger` | Poll a Fanuc controller via FOCAS2 |
| `dprnt_path`, `dprnt_glob` | `mazak-logger` | Read DPRNT output from a mounted Mazak share |

**Rules:**
- `[cklogd]` is reserved for global settings.
- Every `[name]` section requires `file`.
- `max_fields` controls how many CSV fields are stored; defaults to 10. Set it to match your column count to avoid extra empty columns.
- `[name.columns]` names columns by 1-based index; unspecified positions default to `Column1`, `Column2`, etc.
- Column names must match `[a-zA-Z_][a-zA-Z0-9_]*`.

## Run

```bash
./cklogd -config cklogd.ini
./mazak-logger -config cklogd.ini   # Mazak Matrix Nexus (Windows 2000)
./focas-logger -config cklogd.ini   # Fanuc 31i-WB
```

`mazak-logger` and `focas-logger` accept a `-debug` flag for verbose logging.

All three processes are independent and can be started in any order.

## Querying

Every `log_lines` row contains: `id`, `filename`, `line` (raw CSV), the configured columns, and `ingested_at`. A `file_offsets` table tracks byte offsets for crash recovery.

```sql
SELECT event, program, ip, timestamp FROM log_lines WHERE event = 'START';
SELECT event, COUNT(*) FROM log_lines GROUP BY event;
SELECT * FROM log_lines WHERE ingested_at > '2026-03-01';
```

## Behavior

- **Explicit file list** — only files listed in the ini are tracked.
- **Monthly rotation** — new `<name>_YYYY_MM.db` opened automatically; offset preserved.
- **Retention** — DBs older than `retain_months` deleted after rotation (including `-wal`/`-shm`).
- **API posting** — async; failed POSTs are dropped (queue: 512 batches).
- **Crash recovery** — byte offset and inode persisted per DB; resumes on restart.
- **Log rotation** — detected via inode change or file shrink; reads from offset 0.

## Further reading

- [Installation & Samba setup](docs/install.md)
- [Heidenhain TNC640](docs/machines/heidenhain-tnc640.md)
- [Mazak Matrix Nexus 200](docs/machines/mazak-matrix-nexus.md)
- [Fanuc 31i-WB (Robocut C800iB)](docs/machines/fanuc-31i-wb.md)
