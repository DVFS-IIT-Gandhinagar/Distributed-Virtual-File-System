# Distributed Virtual File System (DVFS) - Setup Guide

This document outlines the setup and configuration process for the Distributed Virtual File System (DVFS).

The system components include:
1. **Dev Machine**
2. **Update Gist Server**
3. **Metaserver**
4. **Admin UI Server**
5. **File Server(s)**

> **Note:** You can run all services on the same machine (an all-in-one setup) or distribute them across multiple servers. Depending on your architecture, follow the **Base Setup** first, and then proceed to the relevant component sections for that specific machine.

---

## 1. Base Setup (Required Everywhere)

Run the following commands on **every** machine that will host any of the DVFS services.

```bash
./connect.sh
git clone "https://github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System.git"
cd "Distributed-Virtual-File-System"
chmod +x ./scripts/rp_115/setup.sh
./scripts/rp_115/setup.sh
chmod +x ./scripts/rp_115/persist.sh 
./scripts/rp_115/persist.sh
sudo cp scripts/rp_115/fortinet.service scripts/rp_115/fortinet.timer /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now fortinet.timer
```

---

## 2. Dev Machine Setup

Run the following commands on your development machine to generate certificates:

```bash
go run scripts/gen-certs/cmd/gen_root_ca/main.go
go run scripts/gen-certs/cmd/gen_node_certs/main.go
```

For **each server `{i}`** with IP `${ip}`, run the following to deploy the generated certificates:

```bash
ssh dvfs${i}@${ip} "mkdir -p ~/Distributed-Virtual-File-System/certs"

scp deploy_certs/dvfs${i}/server.crt \
    dvfs${i}@${ip}:~/Distributed-Virtual-File-System/certs/

scp deploy_certs/dvfs${i}/server.key \
    dvfs${i}@${ip}:~/Distributed-Virtual-File-System/certs/

scp deploy_certs/ca.crt \
    dvfs${i}@${ip}:~/Distributed-Virtual-File-System/certs/

ssh dvfs${i}@${ip} \
    "chmod 600 ~/Distributed-Virtual-File-System/certs/server.key && chmod 644 ~/Distributed-Virtual-File-System/certs/*.crt"
```

---

## 3. Metaserver Setup

If the current machine is acting as the **Metaserver**, run the following:

```bash
sudo cp scripts/dvfs-metaserver.service /etc/systemd/system/
chmod +x scripts/start-metaserver.sh
sudo systemctl daemon-reload
sudo systemctl enable --now dvfs-metaserver
```

---

## 4. Update Gist Server Setup

If the current machine is acting as the **Update Gist Server**, follow these steps.

### Install Dependencies & Prepare Environment
```bash
sudo apt update && sudo apt install -y python3 python3-pip python3-requests python3-dotenv
sudo mkdir -p /opt/dvfs
sudo cp scripts/rp_115/update_gist.py /opt/dvfs/
sudo chmod +x /opt/dvfs/update_gist.py
sudo nano /opt/dvfs/.env
```

### Configure Credentials
Add your credentials into `/opt/dvfs/.env`:
```env
TAILSCALE_CLIENT_ID=your_tailscale_oauth_client_id
TAILSCALE_CLIENT_SECRET=your_tailscale_oauth_client_secret
GIST_ID=your_github_gist_id
GITHUB_TOKEN=your_github_personal_access_token
```

### Set Permissions & Start Services
```bash
sudo chown -R $USER:$USER /opt/dvfs
sudo chmod 600 /opt/dvfs/.env
sudo cp scripts/rp_115/dvfs-gist.service /etc/systemd/system/
sudo cp scripts/rp_115/dvfs-gist.timer /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl start dvfs-gist.service
sudo systemctl enable --now dvfs-gist.timer
systemctl list-timers --all | grep dvfs-gist
```

---

## 5. Admin UI Server Setup

If the current machine is acting as the **Admin UI Server**, follow these steps.

### SSH Access
Generate a key and copy it to all servers:
```bash
ssh-keygen
ssh-copy-id user@<fileserver_ip>  # Repeat for all servers
```

### Setup Environment & Credentials
```bash
echo -n "YourSecretPassword" | sha256sum | awk '{print $1}'
cat << 'EOF' > .env
ADMIN_PASSWORD_HASH=8c6976e5b5410415bde908bd4dee15dfb167a9c873fc4bb8a81f6f2ab448a918
EOF
chown $(id -un):$(id -gn) .env
chmod 600 .env
```

### Install Node.js & Build UI
```bash
curl -fsSL https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.3/install.sh | bash
export NVM_DIR="$HOME/.nvm"
[ -s "$NVM_DIR/nvm.sh" ] && . "$NVM_DIR/nvm.sh"
nvm install --lts
nvm alias default 'lts/*'
node --version
npm --version

cd cmd/admin/ui
npm install -D @vitejs/plugin-react@^6.1.1 vite@^8.2.2
npm install
npm run build
cd ../../..
```

### Start Admin Service
```bash
sudo cp scripts/dvfs-admin.service /etc/systemd/system/
chmod +x scripts/start-admin.sh
sudo systemctl daemon-reload
sudo systemctl enable --now dvfs-admin
```

---

## 6. File Server Setup

For **each fileserver**, run the following commands:

```bash
sudo bash -c 'cat <<EOF> /etc/sudoers.d/dvfs
# Allow dvfs service management and machine reboot without password prompt
$SUDO_USER ALL=(ALL) NOPASSWD: /usr/bin/systemctl restart dvfs-fileserver, /usr/bin/systemctl status dvfs-fileserver, /usr/bin/journalctl, /sbin/reboot, /usr/sbin/reboot, /usr/bin/systemctl reboot, /sbin/shutdown
EOF'
sudo chmod 0440 /etc/sudoers.d/dvfs

sudo cp scripts/dvfs-fileserver.service /etc/systemd/system/dvfs-fileserver.service
chmod +x scripts/start-fileserver.sh
sudo systemctl daemon-reload
sudo systemctl enable dvfs-fileserver
sudo systemctl start dvfs-fileserver
```
