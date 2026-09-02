#!/bin/bash

while [[ $# -gt 0 ]]; do
    case "$1" in
        --meta_addr)
            meta_addr="$2"
            shift 2
            ;;
        --own_ip)
            own_ip="$2"
            shift 2
            ;;
        *)
            echo "Unknown argument: $1"
            echo "Usage: $0 --meta_addr <IP:PORT> --own_ip <IP>"
            exit 1
            ;;
    esac
done

if [[ -z "$meta_addr" || -z "$own_ip" ]]; then
    echo "Usage: $0 --meta_addr <IP:PORT> --own_ip <IP>"
    exit 1
fi

tmux new-session -d -s fileserver \
    "cd Distributed-Virtual-File-System/bin/ && ./fileserver --meta_addr '$meta_addr' --own_ip='$own_ip'"