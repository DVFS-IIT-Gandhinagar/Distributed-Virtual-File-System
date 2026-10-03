#!/bin/bash

# The metaserver exits immediately without a MongoDB URI, and inside tmux
# that error would vanish with the session. Export MONGO_URI first, or accept
# the local default.
tmux new-session -d -s metaserver 'cd Distributed-Virtual-File-System/bin/; ./metaserver -mongo_uri="${MONGO_URI:-mongodb://127.0.0.1:27017/dvfs}"'
