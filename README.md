# pluster

Pluster is a proxy for redis cluster written in Go. Exposes a single Redis endpoint to clients and transparently routes requests to the correct cluster node — handling slot-based routing, cross-slot fan-out, MOVED/ASK redirections, and topology changes.

## Features

- Slot-based routing via CRC16 hash
- Automatic MOVED/ASK redirection handling
- Live topology refresh (CLUSTER NODES)
- Cross-slot fan-out for multi-key commands (MGET, MSET, DEL, …)
- Read scaling: master-only / master-slave balanced / slave-only with master fallback
- MULTI/EXEC transactions via dedicated backend connection
- Blocking commands (BLPOP, BRPOP, XREAD BLOCK, …)
- Pub/Sub support
- Per-node pipelined connection pool

## Installation

```bash
go install github.com/pluster/pluster/cmd/pluster@latest
```

Or build from source:

```bash
make build   # outputs bin/pluster
```

## Quick Start

```bash
# Connect to a single cluster entry point
pluster 127.0.0.1:7000

# Multiple entry points, custom port
pluster --port 7778 127.0.0.1:7000 127.0.0.1:7001 127.0.0.1:7002

# Use a config file
pluster -c config/simple.conf
```

## CLI Options

| Flag | Default | Description |
|------|---------|-------------|
| `--port` | `7777` | Proxy listen port |
| `--bind` | `0.0.0.0` | Bind address |
| `--auth` | | Backend cluster password |
| `--auth-user` | | Backend cluster username (Redis 6+ ACL) |
| `--pool-size` | `10` | Connection pool size per node |
| `-c` | | Config file path |
| `--log-level` | `info` | Log level: debug / info / warn / error |
| `--pprof` | | Enable pprof HTTP server (e.g. `127.0.0.1:6060`) |
| `--version` | | Print version and exit |

## Config File

```
bind 0.0.0.0
port 7777

# One or more cluster entry points
cluster 127.0.0.1:7000
cluster 127.0.0.1:7001
cluster 127.0.0.1:7002

# Backend auth (Redis 6+ ACL)
username default
password yourpassword

# Require clients to authenticate with the proxy
# client-password yourproxypassword

pool-size 20
idle-timeout 30s
refresh-interval 5s
log-level info
max-clients 1000

# Read mode: master-only | master-slave | slave-only
read-mode master-only
```

### Read Modes

| Value | Behaviour |
|-------|-----------|
| `master-only` | All reads go to master (default, strongest consistency) |
| `master-slave` | Round-robin across master and all slaves |
| `slave-only` | Prefer slave; fall back to master if no slave is available |

## Running Tests

```bash
# Unit tests
make test-unit

# Integration tests (requires a local Redis Cluster on ports 18000+)
make test-integration
```

## Project Layout

```
cmd/pluster/        # Binary entrypoint
pkg/
  config/           # Config loading
  proto/            # RESP parser & encoder, CRC16, command table
  cluster/          # Topology manager (CLUSTER NODES)
  pool/             # Pipelined connection pool
  router/           # Slot routing, read-mode logic
  engine/           # Request handler, backend mux, pub/sub, transactions
  server/           # TCP server (gnet)
tests/              # Integration tests
config/             # Example config files
```

## License

[MIT](LICENSE)
