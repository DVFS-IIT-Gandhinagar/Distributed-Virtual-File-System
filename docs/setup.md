# Cluster Deployment & Operations Runbook

This document is the authoritative, all-in-one runbook for deploying the Distributed Virtual File System (DVFS) across cluster nodes (such as Raspberry Pi boards, workstations, or Ubuntu Linux servers).

Commands are presented first in sequential execution order, followed by conceptual explanations and operational details in Section 8.

---

## 1. Base Setup (Execute on Every Node)

Run these commands on **every** physical or virtual machine hosting any DVFS service:

```bash
./connect.sh # replace id and passwd
# Clone the repository
git clone https://github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System.git
cd Distributed-Virtual-File-System

# Run base environment preparation
chmod +x ./scripts/rp_115/setup.sh
./scripts/rp_115/setup.sh

# Mask hardware sleep/suspend targets to keep nodes online
chmod +x ./scripts/rp_115/persist.sh
./scripts/rp_115/persist.sh

# Install and start the network re-authentication timer
sudo cp scripts/rp_115/fortinet.service scripts/rp_115/fortinet.timer /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now fortinet.timer
```

---

## 2. Certificate Generation & Distribution (Developer Machine Only)

Execute these commands once on your development workstation:

```bash
# Step 1: Mint the air-gapped Root Certificate Authority (10-year validity)
go run scripts/gen-certs/cmd/gen_root_ca/main.go

# Step 2: Mint leaf certificates with DNS SANs for all cluster nodes (dvfs1 through dvfs9, localhost, fs1, mds)
go run scripts/gen-certs/cmd/gen_node_certs/main.go
```

For **each cluster node `{i}`** with IP `${ip}`, distribute the minted certificates:

```bash
# Create certs directory on remote node
ssh dvfs${i}@${ip} "mkdir -p ~/Distributed-Virtual-File-System/certs"

# Copy leaf cert, leaf private key, and public Root CA cert
scp deploy_certs/dvfs${i}/server.crt dvfs${i}@${ip}:~/Distributed-Virtual-File-System/certs/
scp deploy_certs/dvfs${i}/server.key dvfs${i}@${ip}:~/Distributed-Virtual-File-System/certs/
scp deploy_certs/ca.crt             dvfs${i}@${ip}:~/Distributed-Virtual-File-System/certs/

# Restrict private key permissions
ssh dvfs${i}@${ip} "chmod 600 ~/Distributed-Virtual-File-System/certs/server.key && chmod 644 ~/Distributed-Virtual-File-System/certs/*.crt"
```

Verify certificate chains:

```bash
openssl verify -CAfile deploy_certs/ca.crt deploy_certs/dvfs1/server.crt
```

---

## 3. MetaServer Setup (dvfs1 / Coordinator Node)

On the machine designated as the **MetaServer**:

```bash
# Install and enable the systemd service
sudo cp scripts/dvfs-metaserver.service /etc/systemd/system/
chmod +x scripts/start-metaserver.sh
sudo systemctl daemon-reload
sudo systemctl enable --now dvfs-metaserver

# Verify status
sudo systemctl status dvfs-metaserver --no-pager
```

---

## 4. Update Gist Discovery Server Setup

On the machine designated to run the **Gist IP updater**:

```bash
# Install system packages
sudo apt update && sudo apt install -y python3 python3-pip python3-requests python3-dotenv

# Prepare execution directory
sudo mkdir -p /opt/dvfs
sudo cp scripts/rp_115/update_gist.py /opt/dvfs/
sudo chmod +x /opt/dvfs/update_gist.py

# Create secret configuration file
sudo tee /opt/dvfs/.env > /dev/null << 'EOF'
TAILSCALE_CLIENT_ID=your_tailscale_oauth_client_id
TAILSCALE_CLIENT_SECRET=your_tailscale_oauth_client_secret
GIST_ID=your_github_gist_id
GITHUB_TOKEN=your_github_personal_access_token
EOF

# Lock down permissions and enable hourly timer
sudo chown -R $USER:$USER /opt/dvfs
sudo chmod 600 /opt/dvfs/.env
sudo cp scripts/rp_115/dvfs-gist.service scripts/rp_115/dvfs-gist.timer /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl start dvfs-gist.service
sudo systemctl enable --now dvfs-gist.timer

# Verify timer schedule
systemctl list-timers --all | grep dvfs-gist
```

---

## 5. Admin UI & Orchestration Server Setup

On the coordinator or management server:

```bash
# Step 1: Generate an SSH keypair for cluster management
ssh-keygen -t ed25519 -f ~/.ssh/id_ed25519 -N ""

# Step 2: Copy SSH public key to each fileserver node
for ip in <node_ips>; do
    ssh-copy-id -i ~/.ssh/id_ed25519.pub user@${ip}
done

# Step 3: Configure administrative password hash (SHA-256)
# On Linux / macOS / WSL:
echo -n "YourSecretPassword" | sha256sum | awk '{print "ADMIN_PASSWORD_HASH="$1}' > .env
chmod 600 .env

# On Windows PowerShell:
# $hash = [System.BitConverter]::ToString([System.Security.Cryptography.SHA256]::Create().ComputeHash([System.Text.Encoding]::UTF8.GetBytes("YourSecretPassword"))).Replace("-","").ToLower()
# "ADMIN_PASSWORD_HASH=$hash" | Out-File -Encoding ascii .env

# Step 4: Build the React SPA frontend
cd cmd/admin/ui
npm install -D @vitejs/plugin-react@^6.1.1 vite@^8.2.2
npm install
npm run build
cd ../../..

# Step 5: Option A - Run as a systemd service
sudo cp scripts/dvfs-admin.service /etc/systemd/system/
chmod +x scripts/start-admin.sh
sudo systemctl daemon-reload
sudo systemctl enable --now dvfs-admin

# Verify status
sudo systemctl status dvfs-admin --no-pager

# Step 5: Option B - Run binary directly (Development / Testing)
# Note: Password authentication uses ADMIN_PASSWORD_HASH loaded from .env
./bin/admin \
  -port=8080 \
  -state_file=./metaserver_state.json \
  -static=./cmd/admin/static \
  -ssh_user=dvfs \
  -ssh_key=~/.ssh/id_ed25519 \
  -repo_path=~/Distributed-Virtual-File-System
```

Open `http://<admin_ip>:8080` (or `https://` if TLS certs are supplied) in your web browser.

---

## 6. FileServer Node Setup (Each Storage Node)

On **every fileserver node** (`dvfs1` through `dvfs9`):

```bash
# Step 1: Configure scoped passwordless sudoers rules for remote orchestration
sudo bash -c 'cat <<EOF > /etc/sudoers.d/dvfs
# Scoped dvfs administration privileges without password prompt
$SUDO_USER ALL=(ALL) NOPASSWD: /usr/bin/systemctl restart dvfs-*, /usr/bin/systemctl status dvfs-*, /usr/bin/journalctl, /sbin/reboot, /usr/sbin/reboot, /usr/bin/systemctl reboot, /sbin/shutdown, /usr/bin/apt, /usr/bin/apt-get
EOF'
sudo chmod 0440 /etc/sudoers.d/dvfs

# Step 2: Install and start the FileServer service
sudo cp scripts/dvfs-fileserver.service /etc/systemd/system/
chmod +x scripts/start-fileserver.sh
sudo systemctl daemon-reload
sudo systemctl enable --now dvfs-fileserver

# Step 3: Verify 
sudo systemctl status dvfs-fileserver --no-pager
```

---

## 7. Quick Local Development (Single-Machine)

To test the complete DVFS ecosystem locally on a single machine without systemd:

```bash
# 1. Generate dev certificates (if not already created)
make certs

# 2. Terminal 1: Start MetaServer
go run ./cmd/metaserver/main.go -port=50051

# 3. Terminal 2: Start FileServer
go run ./cmd/fileserver/main.go \
  -id=fs1 \
  -port=50052 \
  -data=./fileserver_data \
  -meta_addr=127.0.0.1:50051 \
  -own_ip=127.0.0.1

# 4. Terminal 3: Start Admin Console
go run ./cmd/admin/main.go \
  -port=8080 \
  -state_file=./metaserver_state.json \
  -static=./cmd/admin/static

# 5. Terminal 4: Launch Client
go run ./cmd/client/main.go -username=alice -ip_addr=127.0.0.1 -port=50051 -meta=true
```

---

## 8. Client Usage

### Running from Source
```bash
# Connect via MetaServer with dynamic Gist discovery
go run ./cmd/client/main.go -username alice

# Connect directly to a specific FileServer or MetaServer IP
go run ./cmd/client/main.go -username alice -ip_addr 10.7.52.85 -port 50051
```

### Running Pre-Compiled Standalone Binaries
Download the matching binary from the repository release artifacts (e.g. `dvfs-client-windows-amd64.exe` or `dvfs-client-linux-amd64`):
```bash
./dvfs-client-linux-amd64 -username alice
```

---

## 9. Architectural & Operational Explanations

### Why Hardware Sleep is Masked (`persist.sh`)
Linux power management daemons routinely place idle cluster machines into sleep or hybrid-sleep states. The `persist.sh` script executes `systemctl mask sleep.target suspend.target hibernate.target hybrid-sleep.target` to guarantee uninterrupted fileserver availability.

### Why the Network Timer Exists (`fortinet.service` / `fortinet.timer`)
Campus networks (such as IIT Gandhinagar's Fortinet gateway) frequently terminate outbound internet sessions after 24 hours, requiring web captive portal authentication. The `fortinet.timer` runs on boot and hourly thereafter, invoking `connect.sh` to extract CSRF tokens and submit credentials headless to `https://fwg.iitgn.ac.in`, preventing network dropouts.

### Why Certificate Authority Keys Remain Air-Gapped
Traditional TLS setups generate the CA key directly on the server. DVFS decouples this completely: `scripts/gen-certs/cmd/gen_root_ca/` writes `ca.key` strictly to the developer's administrative machine. Only signed end-entity leaf certificates (`server.crt`) and the public Root certificate (`ca.crt`) are deployed via `scp`. If any storage node is physically stolen or compromised, the root certificate authority remains secure.

### How Dynamic Discovery Bridges Tailscale and Ethernet IPs
Cluster machines run on DHCP where local IP addresses can change upon router reboot. The `update_gist.py` script uses a dual-network topology:
1. It queries the Tailscale API to find active devices tagged with `tag:dvfsmachines`.
2. It uses Tailscale's overlay IPs (`100.x.y.z`) to SSH into each node and read its physical campus Ethernet interface (`eno*|enp*|eth*`).
3. It posts the mapping of hostname to campus LAN IP directly to a public GitHub Gist (`machines.json`).
4. DVFS clients read this Gist on startup, dial the active campus LAN IP directly for high-speed local transfer, and supply the node's hostname (`dvfs1`..`dvfs9`) in the TLS SNI header.

### Why Sudoers is Scoped
The Admin Console features remote cluster orchestration (restarting services, streaming logs, updating packages, and rebooting nodes). To enable automated execution over SSH without prompting for interactive passwords or granting unrestricted root privileges, `/etc/sudoers.d/dvfs` restricts passwordless execution specifically to `/usr/bin/systemctl`, `/usr/bin/journalctl`, `/sbin/reboot`, `/sbin/shutdown`, and `/usr/bin/apt`.

### How Admin Authentication Operates
The Admin Console reads `ADMIN_PASSWORD_HASH` from `.env`. When an administrator logs in, the backend computes the SHA-256 hash of the submitted password and compares it in constant time via `crypto/subtle.ConstantTimeCompare`. A cryptographically secure 32-byte session token is generated and stored with a 12-hour expiration, set via an `HttpOnly` browser cookie (`dvfs_admin_token`).
