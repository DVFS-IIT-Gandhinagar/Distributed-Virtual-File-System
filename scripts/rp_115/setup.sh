#!/bin/bash
set -e

sudo apt update
sudo apt install -y git make wget lm-sensors

wget https://go.dev/dl/go1.27.1.linux-amd64.tar.gz

sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf go1.27.1.linux-amd64.tar.gz

echo 'export PATH=$PATH:/usr/local/go/bin' | sudo tee /etc/profile.d/go.sh > /dev/null
export PATH=$PATH:/usr/local/go/bin

go version

make build