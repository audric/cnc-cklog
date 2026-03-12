# Fanuc 31i-WB (Robocut C800iB and similar)

The Fanuc 31i-WB cannot write directly to a network share from an NC program. `focas-logger` polls the controller via the FOCAS2 ethernet API and writes `START`/`END` events to the log file instead.

## Requirements

- Fanuc FOCAS2 SDK (not publicly distributed — request from Fanuc): `fwlib32.h` + `libfwlib32.so`
- Place SDK files in `/usr/local/include/focas/` and `/usr/local/lib/`
- Controller embedded ethernet enabled and reachable from the server

## Build

```bash
CGO_ENABLED=1 go build -o focas-logger ./cmd/focas-logger
```

If you only have the 32-bit SDK:

```bash
CGO_ENABLED=1 GOARCH=386 go build -o focas-logger ./cmd/focas-logger
```

## Configure cklogd.ini

```ini
[cnc1]
file          = cnclogs/cnc1.log
max_fields    = 4
focas_host    = 10.16.30.100
focas_port    = 8193           ; default: 8193
machine_ip    = 10.16.30.100   ; default: focas_host
machine_name  = CNC1           ; default: uppercase section name
poll_interval = 2s             ; default: 2s

[cnc1.columns]
1 = event
2 = program
3 = ip
4 = timestamp
```

## How it works

`focas-logger` polls the controller every 2 seconds. On state transitions it appends a line:

```
START, CNC1, 10.16.30.100, 2026-03-12 19:07
END,   CNC1, 10.16.30.100, 2026-03-12 19:08
```

On connection loss it reconnects automatically after 10 seconds. The first poll after reconnect establishes state silently — no spurious event if the machine was already running.

## Configure the controller network

On the Fanuc 31i-WB:

1. **SYSTEM** → **Embedded Ethernet** (or **FOCAS2 Ethernet**)
2. Set IP address, subnet mask, and gateway
3. Confirm FOCAS port is `8193`
4. Ensure the server is on the same LAN or routed subnet

> **Tip:** Test with `ping <controller-ip>` from the server before starting `focas-logger`.

## Install as a systemd service

```bash
sudo cp focas-logger /usr/local/bin/focas-logger
sudo cp focas-logger.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable focas-logger
sudo systemctl start focas-logger
sudo journalctl -u focas-logger -f
```
