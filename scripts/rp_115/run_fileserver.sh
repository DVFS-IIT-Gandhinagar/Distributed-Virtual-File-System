#!/bin/bash

while [[ $# -gt 0 ]]; do
    case "$1" in
        --id)
            fs_id="$2"
            shift 2
            ;;
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
            echo "Usage: $0 --id <fs_id> --meta_addr <IP:PORT> --own_ip <IP>"
            exit 1
            ;;
    esac
done

# The id is the node's identity in the cluster. The metaserver refuses a
# second live fileserver claiming the same one, so it has to be unique.
if [[ -z "$fs_id" || -z "$meta_addr" || -z "$own_ip" ]]; then
    echo "Usage: $0 --id <fs_id> --meta_addr <IP:PORT> --own_ip <IP>"
    exit 1
fi

tmux new-session -d -s fileserver \
    "cd Distributed-Virtual-File-System/bin/ && ./fileserver --id='$fs_id' --meta_addr '$meta_addr' --own_ip='$own_ip'"