#!/bin/bash

tmux new-session -d -s admin 'cd Distributed-Virtual-File-System/bin/; ./admin -port=8080 -state_file=./metaserver_state.json -ssh_user=$(whoami) -ssh_key=~/.ssh/id_ed25519 -repo_path=~/Distributed-Virtual-File-System'

