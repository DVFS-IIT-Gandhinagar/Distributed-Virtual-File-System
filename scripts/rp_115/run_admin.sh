#!/bin/bash

tmux new-session -d -s admin 'cd Distributed-Virtual-File-System/; ./bin/admin -port=8080 -mongo_uri=${MONGO_URI:-mongodb://127.0.0.1:27017/dvfs} -ssh_user=$(whoami) -ssh_key=~/.ssh/id_ed25519 -repo_path=~/Distributed-Virtual-File-System'

