# cnc-cklog

Daemon that watches configured CSV log files and ingests new lines into monthly-rotated SQLite databases, with optional async HTTP POST to a remote API. Each log file has its own schema and its own set of databases.

`cnc1.log` → `cnc1_2026_03.db`, `cnc1_2026_04.db`, …

## Build

```bash
go build -o cklogd ./cmd/cklogd

# focas-logger requires CGo and the Fanuc FOCAS2 SDK (see Fanuc section below)
CGO_ENABLED=1 go build -o focas-logger ./cmd/focas-logger
```

## Configuration

All configuration lives in `cklogd.ini` (default path; override with `-config`).

```ini
[cklogd]
dbdir         = .      ; directory where .db files are written
retain_months = 24     ; how many monthly DBs to keep per log (oldest deleted)
debug         = false  ; verbose logging

; One section per log file. Section name becomes the DB filename prefix.
[cnc1]
file       = cnclogs/cnc1.log
max_fields = 4          ; CSV fields to store per line (default: 10)

[cnc1.columns]       ; optional: name each column (default: Column1, Column2…)
1 = event
2 = program
3 = ip
4 = timestamp

; Second log with a different schema
[spindle]
file       = cnclogs/spindle.log
max_fields = 3

[spindle.columns]
1 = event
2 = axis
3 = value
```

### FOCAS2 polling (optional, per log)

For Fanuc controllers (e.g. Robocut C800iB with 31i-WB), `focas-logger` polls the machine via the FOCAS2 ethernet API and writes `START`/`END` events directly to the log file. `cklogd` then ingests those lines normally — no difference on the ingestion side.

Add these keys to any log section to enable FOCAS polling:

```ini
[cnc1]
file          = cnclogs/cnc1.log
focas_host    = 10.16.30.100   ; controller IP — enables FOCAS for this section
focas_port    = 8193           ; FOCAS2 port (default: 8193)
machine_ip    = 10.16.30.100   ; IP written into CSV lines (default: focas_host)
machine_name  = CNC1           ; identifier written into CSV lines (default: uppercase section name)
poll_interval = 2s             ; how often to query the controller (default: 2s)
```

`focas-logger` detects state transitions on the controller:

- Machine goes idle → running: writes `START, CNC1, 10.16.30.100, 2026-03-12 19:07`
- Machine goes running → idle: writes `END, CNC1, 10.16.30.100, 2026-03-12 19:08`

If the connection drops, `focas-logger` reconnects automatically after 10 seconds. On reconnect, the first poll establishes the current state silently (no spurious event if the machine was already running).

### API posting (optional, per log)

Add these keys to any log section to POST each ingested batch to an HTTP/HTTPS endpoint:

```ini
[cnc1]
file    = cnclogs/cnc1.log
api_url = https://api.example.com/logs
```

**No authentication:**
```ini
api_url = https://api.example.com/logs
```

**Bearer token:**
```ini
api_url        = https://api.example.com/logs
api_auth_type  = bearer
api_auth_token = mysecrettoken
```

**Basic auth:**
```ini
api_url        = https://api.example.com/logs
api_auth_type  = basic
api_auth_user  = myuser
api_auth_token = mypassword
```

POST body is a JSON array of objects with your named column keys:
```json
[{"event":"START","program":"PROGRAM1","ip":"10.16.30.100","timestamp":"2026-03-12 19:07"}]
```

HTTPS works out of the box using the system certificate store.

### Configuration rules

- `[cklogd]` is reserved for global settings.
- Every other `[name]` section defines a log to watch. `file` is required.
- `[name.columns]` names columns by 1-based index. Unspecified positions default to `Column1`, `Column2`, etc.
- Column names must match `[a-zA-Z_][a-zA-Z0-9_]*`.
- Adding a log or changing `max_fields` takes effect on restart. Changing column names adds the new column to existing DBs (old columns are not removed).

## Run

```bash
./cklogd                          # uses cklogd.ini in current directory
./cklogd -config /etc/cklogd.ini

./focas-logger                    # same config file as cklogd
./focas-logger -config /etc/cklogd/cklogd.ini
```

`focas-logger` and `cklogd` run independently and can be started in any order. They share only the log files on disk.

## Install as a systemd service

**1. Create a dedicated user and directories:**

```bash
sudo useradd -r -s /usr/sbin/nologin cklogd
sudo mkdir -p /var/log/cnclogs /var/lib/cklogd /etc/cklogd
sudo chown cklogd:cklogd /var/log/cnclogs /var/lib/cklogd
```

**2. Install the binary and config:**

```bash
sudo cp cklogd /usr/local/bin/cklogd
sudo cp cklogd.ini /etc/cklogd/cklogd.ini
# Edit /etc/cklogd/cklogd.ini: set dbdir = /var/lib/cklogd and file paths
```

**3. Install and enable the services:**

```bash
sudo cp cklogd /usr/local/bin/cklogd
sudo cp focas-logger /usr/local/bin/focas-logger   # if using FOCAS

sudo cp cklogd.service /etc/systemd/system/
sudo cp focas-logger.service /etc/systemd/system/  # if using FOCAS
sudo systemctl daemon-reload

sudo systemctl enable cklogd
sudo systemctl start cklogd

sudo systemctl enable focas-logger    # if using FOCAS
sudo systemctl start focas-logger
```

**4. Manage the services:**

```bash
sudo systemctl status cklogd
sudo systemctl stop cklogd
sudo systemctl restart cklogd
sudo journalctl -u cklogd -f          # follow live logs
sudo journalctl -u cklogd --since today

sudo systemctl status focas-logger
sudo journalctl -u focas-logger -f
```

## Expose `cnclogs` via Samba (for CNC machines)

CNC machines can write logs directly over SMB. Install Samba, create a dedicated user, and add this share:

```ini
# /etc/samba/smb.conf

[global]
   workgroup = WORKGROUP
   server string = CNC Log Server
   log file = /var/log/samba/%m.log
   max log size = 50

[cnclogs]
   path = /var/log/cnclogs
   comment = CNC Log Files
   read only = no
   browseable = yes
   create mask = 0644
   directory mask = 0755
   valid users = cncuser
```

```bash
sudo apt install samba

# Create the Samba write account
sudo useradd -M -s /usr/sbin/nologin cncuser
sudo smbpasswd -a cncuser

# Shared ownership: cncuser writes, cklogd group reads
sudo chown cncuser:cklogd /var/log/cnclogs
sudo chmod 2770 /var/log/cnclogs   # setgid: new files inherit cklogd group

sudo systemctl enable --now smbd
sudo systemctl restart smbd
```

CNC machines connect to `\\<server>\cnclogs` with the `cncuser` credentials. `cklogd` reads the files via group membership.

## Writing to the log from a CNC program

### Heidenhain TNC640 (HEROS)

### Log format

Each line written to `cnc1.log` should be CSV with four fields matching the configured columns:

```
START, CNC1, 10.16.30.100, 2026-03-12 19:07
END,   CNC1, 10.16.30.100, 2026-03-12 19:08
```

### DIN/ISO program

Use `FN 16: F-PRINT` (prefixed with `%` in DIN/ISO context) to write to a file. Call it at the top and bottom of the program:

```gcode
%BEGIN PGM CNC1 MM

; Log program start
% QS1 = "CNC1"
% FN 16: F-PRINT /NET/cnclogs/cnc1.log / APPEND / "START, %S1, 10.16.30.100, %time%"

G00 ...
; ... machining code ...

; Log program end
% FN 16: F-PRINT /NET/cnclogs/cnc1.log / APPEND / "END, %S1, 10.16.30.100, %time%"

%END PGM CNC1 MM
```

> The `APPEND` keyword requires HEROS 4.x or later. Check **MOD → Software version**. On older firmware, use a per-day filename as a workaround: `/NET/cnclogs/cnc1_%date%.log`.

**Useful format variables:**

| Variable | Value |
|----------|-------|
| `%time%` | current time `HH:MM:SS` |
| `%date%` | current date `YYYY-MM-DD` |
| `%S1`…`%S9` | string Q-parameters `QS1`…`QS9` |
| `%Q1`… | numeric Q-parameters |

### Reusable subprogram

To avoid repeating the FN 16 call in every program, create a subprogram `LOGWRITE.I`:

```gcode
%BEGIN PGM LOGWRITE MM
; QS1 = machine/program name, QS2 = event (START or END)
% FN 16: F-PRINT /NET/cnclogs/cnc1.log / APPEND / "%S2, %S1, 10.16.30.100, %time%"
%END PGM LOGWRITE MM
```

Call it from any main program:

```gcode
% QS1 = "CNC1"
% QS2 = "START"
% CALL PGM /TNC/nc_prog/LOGWRITE.I

; ... machining code ...

% QS2 = "END"
% CALL PGM /TNC/nc_prog/LOGWRITE.I
```

### Configuring the remote path on HEROS TNC640

The `/NET/cnclogs/` path above corresponds to a mounted Samba network drive. To configure it:

1. Open the **File Manager** on the TNC640.
2. Press the **Network** softkey (globe icon) → **Manage network drives** → **Add**.
3. Enter:

   | Field | Value |
   |-------|-------|
   | Server | `\\192.168.x.x` (server IP — avoid hostnames if DNS is not configured) |
   | Share | `cnclogs` |
   | Username | `cncuser` |
   | Password | as set with `smbpasswd` |
   | Mount point | `NET` |

4. Confirm. The share is now accessible as `/NET/cnclogs/` in all NC programs.

Network drives configured this way survive reboots. If the mount does not persist, contact your MTB — some machine configurations restrict network settings to the **MOD → Machine settings → Network** menu.

> **Tip:** Before running programs, test write access from the TNC file manager by creating a file manually in `/NET/cnclogs/`.

### Fanuc 31i-WB (Robocut C800iB and similar)

The Fanuc 31i-WB does not support direct file writes to a network share from within an NC program. Instead, use `focas-logger` as a companion process — it polls the controller over ethernet via the FOCAS2 API and writes the same CSV lines that `cklogd` expects.

#### Requirements

- Fanuc FOCAS2 SDK from Fanuc (not publicly distributed): `fwlib32.h` + `libfwlib32.so`
- Place the SDK files in `/usr/local/include/focas/` and `/usr/local/lib/`
- The controller's embedded ethernet must be enabled and reachable from the server

#### Build

```bash
CGO_ENABLED=1 go build -o focas-logger ./cmd/focas-logger
```

If you only have the 32-bit SDK (older installations):

```bash
CGO_ENABLED=1 GOARCH=386 go build -o focas-logger ./cmd/focas-logger
```

#### Configuration

Enable FOCAS polling for a log section by adding `focas_host`:

```ini
[cnc1]
file          = cnclogs/cnc1.log
max_fields    = 4
focas_host    = 10.16.30.100
machine_name  = CNC1

[cnc1.columns]
1 = event
2 = program
3 = ip
4 = timestamp
```

#### How it works

`focas-logger` polls the controller every 2 seconds (configurable). When it detects a state change it appends a line to the log file:

```
START, CNC1, 10.16.30.100, 2026-03-12 19:07
END,   CNC1, 10.16.30.100, 2026-03-12 19:08
```

`cklogd` picks up these lines via `fsnotify` exactly as it would for a Heidenhain-written log. No changes to `cklogd` configuration are required.

#### Configuring the controller network

On the Fanuc 31i-WB:

1. Go to **SYSTEM** → **Embedded Ethernet** (or **FOCAS2 Ethernet**).
2. Set the controller IP address, subnet mask, and gateway.
3. Confirm the FOCAS port is `8193` (default).
4. Ensure the server running `focas-logger` is on the same LAN segment or routed subnet.

> **Tip:** Test reachability with `ping <controller-ip>` from the server before starting `focas-logger`.

## Querying

```sql
-- All START events
SELECT event, program, ip, timestamp FROM log_lines WHERE event = 'START';

-- Count by event type
SELECT event, COUNT(*) FROM log_lines GROUP BY event;

-- Events for a specific machine
SELECT * FROM log_lines WHERE program = 'PROGRAM1';
```

## Behavior

- **Explicit file list**: only files listed in the ini are tracked; no directory scanning.
- **Monthly rotation**: at the start of each month a new `<name>_YYYY_MM.db` is opened automatically. The log file offset is preserved — no lines are re-read or skipped.
- **Retention**: after rotation, DBs older than `retain_months` are deleted (including `-wal`/`-shm` sidecars).
- **API posting**: each flushed batch is POSTed asynchronously; failed POSTs are logged and dropped (queue holds up to 512 batches).
- **Crash recovery**: byte offsets and inodes are persisted in each DB. On restart the daemon resumes from the last saved position.
- **Log rotation**: detected via inode change or file shrink; offset resets to 0 and the new file is read from the start.
