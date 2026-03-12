# Mazak Matrix Nexus 200 (Windows 2000)

The Mazak controller runs Windows 2000 with SMB file sharing. DPRNT output is written to a local directory on the controller, shared over the network, and read by `mazak-logger` via a CIFS mount.

> **Note:** inotify does not work on CIFS mounts — `mazak-logger` uses timed polling only.

## 1. Configure DPRNT on the Mazak

Set these parameters to direct DPRNT output to a local directory:

| Parameter | Value | Meaning |
|---|---|---|
| `DPR14` | `3` or `4` | Output port (COM3/COM4) |
| `DPR16` | `1` | Enable DPRNT file output |
| I/O channel (param 20) | `4` | Write to memory card / local disk |

DPRNT creates sequential files: `PRNT001.DAT`, `PRNT002.DAT`, … in the configured output directory (e.g. `C:\DPRNT\`).

## 2. NC program

Write DPRNT lines that produce the CSV format `cklogd` expects:

```gcode
; At program start:
POPEN
DPRNT[START,*CNC1,*10.16.30.100,*#3011[4]-#3012[2]-#3013[2]*#3014[2]:#3015[2]]
PCLOS

; ... machining ...

; At program end:
POPEN
DPRNT[END,*CNC1,*10.16.30.100,*#3011[4]-#3012[2]-#3013[2]*#3014[2]:#3015[2]]
PCLOS
```

System variables: `#3011`=year, `#3012`=month, `#3013`=day, `#3014`=hour, `#3015`=minute.

Output line: `START, CNC1, 10.16.30.100, 2026-03-13 19:07`

## 3. Share the DPRNT directory on Windows 2000

On the Mazak:
1. Right-click `C:\DPRNT` → **Sharing** → **Share this folder**
2. Share name: `dprnt` — grant read access to the server account

## 4. Mount the share on the Linux server

```bash
sudo apt install cifs-utils
sudo mkdir -p /mnt/mazak/dprnt
sudo mount -t cifs //192.168.1.20/dprnt /mnt/mazak/dprnt \
    -o username=mazakuser,password=secret,vers=1.0,uid=cklogd,gid=cklogd,ro
```

> `vers=1.0` is required for Windows 2000 SMB1.

Add to `/etc/fstab` for persistence:

```
//192.168.1.20/dprnt /mnt/mazak/dprnt cifs username=mazakuser,password=secret,vers=1.0,uid=cklogd,gid=cklogd,ro 0 0
```

## 5. Configure cklogd.ini

```ini
[cnc1]
file          = cnclogs/cnc1.log
max_fields    = 4
dprnt_path    = /mnt/mazak/dprnt
dprnt_glob    = PRNT*.DAT

[cnc1.columns]
1 = event
2 = program
3 = ip
4 = timestamp
```

## 6. Build and run

```bash
go build -o mazak-logger ./cmd/mazak-logger
./mazak-logger -config cklogd.ini
```

`mazak-logger` polls the mounted directory every 2 seconds. Files present at startup are skipped. When a new `PRNT*.DAT` file appears its lines are appended to the log file and picked up by `cklogd`.

## 7. Install as a systemd service

```bash
sudo cp mazak-logger /usr/local/bin/mazak-logger
sudo cp mazak-logger.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable mazak-logger
sudo systemctl start mazak-logger
sudo journalctl -u mazak-logger -f
```
