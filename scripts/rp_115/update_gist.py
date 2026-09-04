#!/usr/bin/python3

import json
import re
import subprocess
import requests
from dotenv import dotenv_values

# Read directly into a dictionary, bypassing the OS environment completely
secrets = dotenv_values("/opt/dvfs/.env")

TS_CLIENT_ID = secrets["TAILSCALE_CLIENT_ID"]
TS_CLIENT_SECRET = secrets["TAILSCALE_CLIENT_SECRET"]

GIST_ID = secrets["GIST_ID"]
GITHUB_TOKEN = secrets["GITHUB_TOKEN"]

TAG = "tag:dvfsmachines"

def get_tailscale_access_token():
    response = requests.post(
        "https://api.tailscale.com/api/v2/oauth/token",
        data={
            "client_id": TS_CLIENT_ID,
            "client_secret": TS_CLIENT_SECRET,
        },
        timeout=15,
    )
    response.raise_for_status()
    return response.json()["access_token"]


def get_tailscale_devices():
    token = get_tailscale_access_token()

    response = requests.get(
        "https://api.tailscale.com/api/v2/tailnet/-/devices",
        headers={
            "Authorization": f"Bearer {token}",
        },
        timeout=15,
    )
    response.raise_for_status()
    return response.json()["devices"]


def get_dvfs_machines():
    devices = get_tailscale_devices()
    machines = []

    for device in devices:
        if TAG not in device.get("tags", []):
            continue

        hostname = device.get("hostname", "").strip()
        last_seen = device.get('lastSeen', "").strip()
        match = re.fullmatch(r"dvfs(\d+)", hostname)
        if not match:
            continue

        tailscale_ip = next(
            (
                address
                for address in device.get("addresses", [])
                if address.startswith("100.")
            ),
            None,
        )

        if not tailscale_ip:
            continue

        machines.append({
            "username": hostname,
            "tailscale_ip": tailscale_ip,
            "number": int(match.group(1)),
            "last_seen":last_seen
        })

    machines.sort(key=lambda x: x["number"])
    return machines


def get_lan_info(machine):
    username = machine["username"]
    tailscale_ip = machine["tailscale_ip"]

    remote_command = r"""
for iface in /sys/class/net/*; do
    iface="${iface##*/}"
    case "$iface" in
        lo|tailscale*) continue ;;
    esac
    case "$iface" in
        eno*|enp*|eth*) ;;
        *) continue ;;
    esac

    mac=$(cat "/sys/class/net/$iface/address" 2>/dev/null)
    ip=$(ip -4 -o addr show dev "$iface" scope global 2>/dev/null |
         awk 'NR==1 {print $4}' |
         cut -d/ -f1)

    if [ -n "$mac" ] && [ -n "$ip" ]; then
        printf '%s %s\n' "$mac" "$ip"
        exit 0
    fi
done
exit 1
"""

    try:
        result = subprocess.run(
            [
                "ssh",
                "-o", "BatchMode=yes",
                "-o", "ConnectTimeout=5",
                "-o", "StrictHostKeyChecking=accept-new",
                f"{username}@{tailscale_ip}",
                remote_command,
            ],
            capture_output=True,
            text=True,
            timeout=10,
            check=False,
        )
    except subprocess.TimeoutExpired:
        print(f"{username}: SSH timed out")
        return None

    if result.returncode != 0:
        print(f"{username}: SSH failed")
        return None

    parts = result.stdout.strip().split()
    if len(parts) != 2:
        print(f"{username}: unexpected SSH output")
        return None

    mac, ip = parts
    return {
        "username": username,
        "mac": mac,
        "ip": ip,
        "last_seen": machine['last_seen']
    }

def get_gist():
    response = requests.get(
        f"https://api.github.com/gists/{GIST_ID}",
        headers={
            "Accept": "application/vnd.github+json",
            "Authorization": f"Bearer {GITHUB_TOKEN}",
            "X-GitHub-Api-Version": "2022-11-28",
        },
        timeout=20,
    )
    response.raise_for_status()

    gist = response.json()
    content = gist["files"]["machines.json"]["content"]

    return json.loads(content)

def update_gist(data):
    content = json.dumps(data, indent=4) + "\n"
    response = requests.patch(
        f"https://api.github.com/gists/{GIST_ID}",
        headers={
            "Accept": "application/vnd.github+json",
            "Authorization": f"Bearer {GITHUB_TOKEN}",
            "X-GitHub-Api-Version": "2022-11-28",
        },
        json={
            "files": {
                "machines.json": {
                    "content": content,
                }
            }
        },
        timeout=20,
    )
    response.raise_for_status()


def main():
    machines = get_dvfs_machines()
    print(f"Found {len(machines)} DVFS machines")
    
    output = []
    for machine in machines:
        info = get_lan_info(machine)
        if info is not None:
            output.append(info)

    print(json.dumps(output, indent=4))
    update_gist(output)
    print("dvfs.json updated successfully.")


if __name__ == "__main__":
    main()