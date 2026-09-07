# Telemetry & Performance Monitoring

This document details the telemetry collection, performance instrumentation, and time-series infrastructure of DVFS, divided into its monitoring principles (**In Essence**) and its backend implementation (**Implementation Quirks**).

---

## 1. In Essence: Lightweight, Pull-Based Observability

Operating a distributed storage cluster requires continuous visibility into storage utilization, network throughput, disk IOPS, and hardware health across all nodes.

DVFS adopts a **pull-based telemetry architecture**:

```
[FileServer fs1] --- (Port 9052 /metrics) ---+
                                             |  5s Scrapes
[FileServer fs2] --- (Port 9053 /metrics) ---+--> [Admin Server Poller]
                                             |          |
[FileServer fs3] --- (Port 9054 /metrics) ---+          v
                                                [In-Memory Ring Buffer]
                                                (720 Snapshots / 60 Min)
                                                        |
                                                        v
                                                [React SPA Dashboard]
```

### Design Invariants
1. **Low Overhead on Storage Nodes**: FileServers perform minimal computation for metrics. They maintain atomic byte and operation counters in memory. Heavy rate derivations, IOPS calculations, and time-series windowing are performed by the Admin Server.
2. **Zero External TSDB Dependencies**: DVFS does not require external telemetry databases like Prometheus, InfluxDB, or Grafana. The Admin Server maintains a rolling in-memory ring buffer flushed periodically to disk, providing an out-of-the-box observability experience.
3. **Decoupled Telemetry Surface**: FileServers expose metrics over a dedicated, lightweight HTTP sidecar listener, leaving the primary gRPC port dedicated strictly to filesystem RPCs and streaming transfers.

---

## 2. Implementation Quirks & Practical Realities

### 2.1 The Fileserver HTTP Sidecar (`internal/fileserver/metrics_http.go`)
Every FileServer process automatically launches an HTTP sidecar:
- **Port Derivation**: The metrics port is computed as:
  `metricsPort = gRPC_port - 41000`
  *(Example: gRPC port `50052` launches metrics HTTP on `9052`)*.
- The Admin Server discovers the FileServer's gRPC address from `metaserver_state.json` and automatically derives the metrics URL.
- **Endpoints**:
  - `GET /metrics`: Returns a JSON document containing the complete metrics catalog.
  - `GET /health`: Lightweight endpoint returning `{"status": "ok"}` for systemd and load balancer probes.

### 2.2 Real-Time Chunked Streaming Throughput
During large multi-gigabyte file transfers, waiting until an operation completes to accumulate bytes causes the dashboard to display `0.00 MiB/s` for minutes, followed by an artificial spike.

DVFS decouples real-time byte counters from operation completion:
- In `UploadFile`: After each 4 MB chunk is written to disk via `WriteFile`, `h.fileServer.AddBytesWritten(uint64(len(chunk)))` is called immediately.
- In `DownloadFile`: After each 4 MB chunk is transmitted over the gRPC stream, `h.fileServer.AddBytesRead(uint64(n))` is called immediately.
- Operation counters and latency durations are committed when the stream closes via `RecordWriteOp` and `RecordReadOp`.
- **Result**: The Admin poller observes active byte movement every 5 seconds, rendering smooth, accurate throughput curves during active multi-gigabyte streams.

### 2.3 Multi-Tier CPU Temperature Engine (`internal/fileserver/cputemp.go`)
Standard Linux temperature readers rely on `/sys/class/thermal/thermal_zone0/temp`. On modern hardware, this often fails:
- On **x86 platforms (Intel & AMD)**, `thermal_zone0` frequently reports static ACPI dummy values or does not exist. Temperature data is exposed via `/sys/class/hwmon/` using `coretemp` or `k10temp` drivers.
- On **Raspberry Pi 5 (Debian Bookworm)**, `thermal_zone0` belongs to the PMIC (power management IC), while the Cortex-A76 CPU is on `thermal_zone1` or `thermal_zone2` with type `bcm2712`.

DVFS implements a multi-tier detection engine:
1. **Tier 1 (Kernel hwmon)**: Scans `/sys/class/hwmon/hwmon*` for drivers (`coretemp`, `k10temp`, `zenpower`, `cpu_thermal`) and preferred labels (`Package id 0`, `Tctl`, `Tdie`, `CPU`).
2. **Tier 2 (Thermal Zones)**: Scans `/sys/class/thermal/thermal_zone*` matching type strings `bcm2712`, `cpu-thermal`, or `x86_pkg_temp`.
3. **Tier 3 (CLI Fallback)**: Executes `sensors -j` and parses JSON output if kernel sysfs entries are unavailable.

### 2.4 Sliding-Window Latency Histograms
FileServers track operation latencies using sliding-window histograms in `internal/fileserver/opmetrics.go`:
- Tracks median (p50), 95th percentile (p95), and 99th percentile (p99) latencies separately for read and write operations.
- Metrics are exposed directly in the `/metrics` payload:
  `op_latency_write_ms_p50`, `op_latency_write_ms_p95`, `op_latency_write_ms_p99`,
  `op_latency_read_ms_p50`, `op_latency_read_ms_p95`, `op_latency_read_ms_p99`.

### 2.5 In-Memory Ring Buffer & Disk Snapshotting
The Admin Server (`internal/admin/ringbuffer.go`) stores historical telemetry per node:
- **Capacity**: 720 snapshots at 5-second resolution (60 minutes of continuous history).
- **FIFO Eviction**: When the buffer reaches 720 entries, older data points are dropped.
- **Snapshot Persistence**: Every 60 seconds and during clean shutdown, `SaveMetricsSnapshot` serializes the buffer to `admin_metrics_snapshot.json`. On reboot, historical data is restored immediately.

### 2.6 Health & Status State Machine
Every 5 seconds, the Admin poller evaluates each node and computes a status grade:
- **`online`**: Responding to HTTP probes and all metrics nominal.
- **`warning`**: Responding, but disk usage > 80% or CPU temperature > 65°C.
- **`degraded`**: Disk usage > 90% or CPU temperature > 75°C.
- **`critical`**: Disk usage > 95% or CPU temperature > 85°C.
- **`offline`**: `/metrics` HTTP endpoint unresponsive for more than 30 seconds.

### 2.7 Performance Aggregation & CSV Export Endpoints
The Admin Console aggregates live and historical performance across the cluster:
- **`GET /api/performance`**: Returns cluster-wide and per-node performance metrics in JSON format, including:
  - Aggregate cluster write/read throughput (`cluster_write_mbps`, `cluster_read_mbps`).
  - Total write/read IOPS (`cluster_write_iops`, `cluster_read_iops`).
  - Overall cluster error percentage (`cluster_error_rate_pct`).
  - Node breakdowns with p50/p95/p99 latency percentiles and active client connections.
- **`GET /api/performance/export`**: Streams performance data as an RFC-4180 compliant CSV file (`dvfs_performance_<timestamp>.csv`). Supports optional query parameter `?node_id=<id>` to export data for a specific node or all online nodes. Useful for research analysis, benchmarking reports, and offline capacity planning.
