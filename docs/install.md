# Installation

## 1. Create a dedicated user and directories

```bash
sudo useradd -r -s /usr/sbin/nologin cklogd
sudo mkdir -p /var/log/cnclogs /var/lib/cklogd /etc/cklogd
sudo chown cklogd:cklogd /var/log/cnclogs /var/lib/cklogd
```

## 2. Install binaries and config

```bash
sudo cp cklogd /usr/local/bin/cklogd
sudo cp mazak-logger /usr/local/bin/mazak-logger   # if using Mazak
sudo cp focas-logger /usr/local/bin/focas-logger   # if using Fanuc FOCAS2

sudo cp cklogd.ini /etc/cklogd/cklogd.ini
# Edit /etc/cklogd/cklogd.ini: set dbdir = /var/lib/cklogd and file paths
```

## 3. Install and enable services

```bash
sudo cp cklogd.service /etc/systemd/system/
sudo cp mazak-logger.service /etc/systemd/system/  # if using Mazak
sudo cp focas-logger.service /etc/systemd/system/  # if using Fanuc FOCAS2
sudo systemctl daemon-reload

sudo systemctl enable cklogd && sudo systemctl start cklogd
sudo systemctl enable mazak-logger && sudo systemctl start mazak-logger   # if using Mazak
sudo systemctl enable focas-logger && sudo systemctl start focas-logger   # if using Fanuc FOCAS2
```

## 4. Manage services

```bash
sudo systemctl status cklogd
sudo systemctl restart cklogd
sudo journalctl -u cklogd -f
sudo journalctl -u cklogd --since today

sudo systemctl status mazak-logger
sudo journalctl -u mazak-logger -f

sudo systemctl status focas-logger
sudo journalctl -u focas-logger -f
```

## Expose `cnclogs` via Samba (for CNC machines)

CNC machines that write logs directly over SMB (e.g. Heidenhain TNC640) need a writable share on the server.

### Install Samba

```bash
sudo apt install samba

sudo useradd -M -s /usr/sbin/nologin cncuser
sudo smbpasswd -a cncuser

sudo chown cncuser:cklogd /var/log/cnclogs
sudo chmod 2770 /var/log/cnclogs   # setgid: new files inherit cklogd group

sudo systemctl enable --now smbd
```

### `/etc/samba/smb.conf`

```ini
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

CNC machines connect to `\\<server>\cnclogs` with the `cncuser` credentials. `cklogd` reads the files via group membership.
