## Plan: MDS Crash Recovery and Auto Reconnect

Implement durable MDS metadata recovery plus active liveness refresh from file servers so MDS can restart without losing routing state. Keep startup behavior simple by loading last persisted state immediately, then continuously correct it with heartbeat and periodic re-registration from file servers.

**Steps**
1. Phase 1: Stabilize current routing assumptions
2. Add guardrails in MDS navigation flow so empty or invalid fileserver maps do not panic and instead return explicit errors. This avoids crash loops during early restart windows while registration is still converging.
3. Update the register flow in MDS to support idempotent updates by address (existing FS updates metadata instead of always allocating a new ID). This is required before robust retry-based convergence.
4. Phase 2: Add durable MDS state persistence
5. Extend MDS state model with durable snapshot schema for fileservers, users, nextFsID, and recovery metadata (last seen, status). Store and load from a local snapshot file at MDS startup/shutdown and after state mutations. Depends on step 3.
6. Integrate persistence hooks at all mutation points: RegisterFileServer and user assignment in Navigate. Use atomic write pattern (temp file + rename) to avoid torn snapshots. Depends on step 5.
7. Add startup loading in MDS initialization path and validate integrity/fallback behavior (missing/corrupt snapshot handling). Parallel with step 6 once schema is defined.
8. Phase 3: Add heartbeat protocol and retry convergence
9. Extend MetaServer gRPC API with Heartbeat RPC and optional metadata fields needed for liveness tracking. Regenerate protobuf Go bindings. Depends on step 5.
10. Implement MDS heartbeat handler and liveness tracker (last seen timestamps, stale markers) and a background monitor goroutine to mark unresponsive FS entries. Depends on step 9.
11. Add fileserver background loop with short-interval heartbeat. On failure, retry registration and continue serving local requests. This is the auto-reconnect path when MDS restarts. Depends on step 9; parallelizable with step 10 after proto is ready.
12. Keep startup one-shot registration, but reuse shared retry logic so startup and background convergence behavior are consistent. Depends on step 11.
13. Phase 4: Convergence and routing policy
14. Apply your chosen policy: trust persisted FS entries immediately after MDS restart, but downgrade/evict entries if heartbeat timeout is exceeded. Update Navigate to prefer healthy entries and avoid stale-only routing unless explicitly allowed. Depends on steps 10-11.
15. Ensure user-to-FS mapping consistency during registration refresh (existing users preserved, conflicts handled deterministically, duplicate ownership prevented). Depends on steps 3 and 14.
16. Phase 5: Testing and operational validation
17. Add unit tests for snapshot save/load, corrupt snapshot fallback, idempotent register updates, heartbeat timeout transitions, and Navigate behavior during partial recovery. Depends on steps 5-15.
18. Add an integration test workflow: start MDS + FS, register users, kill/restart MDS, assert routing continuity and automatic FS convergence without manual restart of FS. Depends on step 11.
19. Document runtime knobs (snapshot path, heartbeat interval/timeout, retry interval) and add recommended defaults for local and docker runs. Parallel with step 18.

**Relevant files**
- /home/shardul/sem6/Distributed-Virtual-File-System/internal/metaserver/metaserver.go — state model, startup load path, monitor goroutines
- /home/shardul/sem6/Distributed-Virtual-File-System/internal/metaserver/handler.go — RegisterFileServer, Navigate, Heartbeat handlers, mutation+persistence hooks
- /home/shardul/sem6/Distributed-Virtual-File-System/cmd/metaserver/main.go — MDS runtime flags and wiring for snapshot/liveness config
- /home/shardul/sem6/Distributed-Virtual-File-System/api/metaserver/metaserver.proto — Heartbeat RPC and message schema updates
- /home/shardul/sem6/Distributed-Virtual-File-System/internal/fileserver/msclient.go — heartbeat client loop and retry behavior
- /home/shardul/sem6/Distributed-Virtual-File-System/internal/fileserver/fileserver.go — lifecycle hooks for background registration tasks
- /home/shardul/sem6/Distributed-Virtual-File-System/cmd/fileserver/main.go — startup configuration and auto-reconnect loop initialization
- /home/shardul/sem6/Distributed-Virtual-File-System/internal/domain/types.go — optional expansion of FileServerInfo recovery fields
- /home/shardul/sem6/Distributed-Virtual-File-System/TODO.md — track completion of crash recovery + heartbeat tasks

**Verification**
1. Unit: snapshot round-trip test confirms fileservers/users/nextFsID persist and restore correctly.
2. Unit: corrupted snapshot test confirms MDS starts with safe empty state and clear log signal.
3. Unit: idempotent registration test confirms same FS address updates existing entry rather than allocating new IDs repeatedly.
4. Unit: heartbeat timeout test confirms status transitions healthy -> stale -> removed according to configured thresholds.
5. Unit: Navigate tests for no-fileserver, only-stale-fileserver, and mixed healthy/stale sets.
6. Integration: start FS + MDS, register, stop MDS, restart MDS, verify existing mapping restored immediately and then refreshed by heartbeat.
7. Integration: keep FS running while MDS restarts; verify FS auto-reconnects via heartbeat plus registration retry without restarting FS process.
8. Manual: run with TLS on/off and ensure reconnect path works in both modes.

**Decisions**
- Chosen from alignment: no strict storage preference, trust persisted entries immediately after restart, and use heartbeat with registration retry on failure.
- Included scope: MDS durable membership recovery + automatic convergence from running FS processes.
- Excluded scope: full distributed consensus/replicated MDS, cross-node quorum, and authentication redesign.

**Further Considerations**
1. Address identity key: use advertised address as primary identity now; if future deployments use dynamic ports, add explicit stable fileserver ID in proto.
2. Snapshot format: JSON is easiest now; if write frequency or consistency requirements grow, migrate to embedded DB with transactional writes.
3. Eviction policy: choose conservative timeout defaults to avoid flapping during brief network blips.