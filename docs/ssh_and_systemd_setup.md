# Passwordless SSH & systemd Setup Guide for DVFS Cluster Nodes

This guide documents the setup required on Ubuntu Live Server / Linux machines hosting the DVFS Metaserver, Admin Console, and Fileserver nodes to enable Phase 3 remote orchestration.

---

## 1. Passwordless SSH Setup

The Admin Console runs on the machine hosting the Metaserver (or any management host) and connects to each Fileserver node over SSH to execute commands (`git pull`, `make`, `systemctl restart`, `journalctl`, etc.).

### Step 1: Generate an SSH Key Pair on the Admin / Metaserver Machine
On the machine running `./bin/admin`:
```bash
# Generate an ed25519 key (recommended) without a passphrase:
ssh-keygen -t ed25519 -f ~/.ssh/id_ed25519 -N ""
```
*(Or if using RSA: `ssh-keygen -t rsa -b 4096 -f ~/.ssh/id_rsa -N ""`)*

### Step 2: Distribute the Public Key to Each Fileserver Node
Run this command for each fileserver node IP address:
```bash
ssh-copy-id -i ~/.ssh/id_ed25519.pub <ssh_user>@<node_ip>
```
*Example:*
```bash
ssh-copy-id -i ~/.ssh/id_ed25519.pub ubuntu@10.7.52.85
ssh-copy-id -i ~/.ssh/id_ed25519.pub ubuntu@10.7.52.86
```

### Step 3: Test Passwordless Login
Verify that you can log in without entering a password:
```bash
ssh -i ~/.ssh/id_ed25519 <ssh_user>@<node_ip> "echo 'SSH connection successful!'"
```

---

## 2. Passwordless `systemctl`, `journalctl` & `reboot` for Sudo

The Admin Console executes `sudo systemctl restart dvfs-fileserver`, `journalctl -u dvfs-fileserver`, and `sudo reboot` (or `shutdown`) to restart services, read logs, and reboot physical cluster machines. To allow the SSH user to execute these commands without a password prompt:

On **each Fileserver node**, create or update `/etc/sudoers.d/dvfs`:
```bash
sudo bash -c 'cat <<EOF > /etc/sudoers.d/dvfs
# Allow dvfs service management and machine reboot without password prompt
<ssh_user> ALL=(ALL) NOPASSWD: /usr/bin/systemctl restart dvfs-fileserver, /usr/bin/systemctl status dvfs-fileserver, /usr/bin/journalctl, /sbin/reboot, /usr/sbin/reboot, /usr/bin/systemctl reboot, /sbin/shutdown
EOF'

# Secure permissions
sudo chmod 0440 /etc/sudoers.d/dvfs
```
*(Replace `<ssh_user>` with your actual username, e.g., `dvfs2`, `ubuntu`, or `jsm`)*

---

## 3. Installing the systemd Fileserver Service

To have fileservers launch automatically on node boot and be manageable via `systemctl`:

### Step 1: Copy Service File
On each Fileserver node:
```bash
sudo cp scripts/dvfs-fileserver.service /etc/systemd/system/dvfs-fileserver.service
```

### Step 2: Adjust Environment Variables (Optional)
The service automatically detects the non-root primary user (UID 1000, e.g. `ubuntu`, `rpi`, `jsm`) and their `~/Distributed-Virtual-File-System` path dynamically!

If your setup uses non-standard ports or paths, edit `/etc/systemd/system/dvfs-fileserver.service`:
- (Optional) Set `Environment=DVFS_USER=<user>` if not using the primary UID 1000 user.
- Update `META_ADDR=` to the Metaserver's `<IP>:50051`.
- Set `FS_ID=` and `FS_PORT=` (e.g. `fs1` / `50052`).

Ensure the startup script is executable:
```bash
chmod +x scripts/start-fileserver.sh
```

### Step 3: Enable & Start the Service
```bash
sudo systemctl daemon-reload
sudo systemctl enable dvfs-fileserver
sudo systemctl start dvfs-fileserver
```

### Step 4: Verify Status
```bash
sudo systemctl status dvfs-fileserver
journalctl -u dvfs-fileserver -n 50 --no-pager
```

---

## 4. Installing the systemd Metaserver & Admin Console Services

To run the Metaserver and Admin Console automatically on startup as persistent background services on the coordinator machine:

### Step 1: Copy Service Files
```bash
sudo cp scripts/dvfs-metaserver.service /etc/systemd/system/
sudo cp scripts/dvfs-admin.service /etc/systemd/system/
chmod +x scripts/start-metaserver.sh scripts/start-admin.sh
```

### Step 2: Enable & Start the Services
```bash
sudo systemctl daemon-reload
sudo systemctl enable --now dvfs-metaserver
sudo systemctl enable --now dvfs-admin
```

### Step 3: Verify Status & View Logs
```bash
# Check status
sudo systemctl status dvfs-metaserver
sudo systemctl status dvfs-admin

# Stream live service logs
journalctl -u dvfs-metaserver -f
journalctl -u dvfs-admin -f
```

---

## 5. Running the Admin Server Manually (Optional)

When starting the Admin Console binary manually without systemd:
```bash
./bin/admin \
  -port=8080 \
  -state_file=./bin/metaserver_state.json \
  -ssh_user=$(whoami) \
  -ssh_key=~/.ssh/id_ed25519 \
  -repo_path=~/Distributed-Virtual-File-System
```

In the web console (default `http://<host-ip>:8080`), navigate to the **Actions** tab. All nodes registered in `metaserver_state.json` will be available for remote orchestration.
