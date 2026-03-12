# Heidenhain TNC640 (HEROS)

The TNC640 can write directly to the log file over the network using `FN 16: F-PRINT` with `APPEND`. No companion process is needed.

## Log format

Each line must be CSV with fields matching the configured columns:

```
START, CNC1, 10.16.30.100, 2026-03-12 19:07
END,   CNC1, 10.16.30.100, 2026-03-12 19:08
```

## DIN/ISO program

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

> `APPEND` requires HEROS 4.x or later. Check **MOD → Software version**. On older firmware use a per-day filename: `/NET/cnclogs/cnc1_%date%.log`.

**Format variables:**

| Variable | Value |
|----------|-------|
| `%time%` | current time `HH:MM:SS` |
| `%date%` | current date `YYYY-MM-DD` |
| `%S1`…`%S9` | string Q-parameters `QS1`…`QS9` |
| `%Q1`… | numeric Q-parameters |

## Reusable subprogram

Create `LOGWRITE.I` once and call it from every program:

```gcode
%BEGIN PGM LOGWRITE MM
; QS1 = machine name, QS2 = event (START or END)
% FN 16: F-PRINT /NET/cnclogs/cnc1.log / APPEND / "%S2, %S1, 10.16.30.100, %time%"
%END PGM LOGWRITE MM
```

```gcode
% QS1 = "CNC1"
% QS2 = "START"
% CALL PGM /TNC/nc_prog/LOGWRITE.I

; ... machining code ...

% QS2 = "END"
% CALL PGM /TNC/nc_prog/LOGWRITE.I
```

## Configuring the network drive on HEROS

The `/NET/cnclogs/` path corresponds to a Samba share mounted on the controller.

1. Open **File Manager** → **Network** softkey → **Manage network drives** → **Add**.
2. Enter:

   | Field | Value |
   |-------|-------|
   | Server | `\\192.168.x.x` (use IP — avoid hostnames if DNS is not configured) |
   | Share | `cnclogs` |
   | Username | `cncuser` |
   | Password | as set with `smbpasswd` |
   | Mount point | `NET` |

3. Confirm. The share is now accessible as `/NET/cnclogs/` in all NC programs.

Network drives survive reboots. If the mount does not persist, check **MOD → Machine settings → Network** or contact your MTB.

> **Tip:** Before running programs, test write access from the TNC file manager by creating a file manually in `/NET/cnclogs/`.
