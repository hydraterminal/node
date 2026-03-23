# [@hydraterminal/node](https://github.com/hydraterminal/node)

A standalone intelligence collection node for the [Hydra Network](https://hydra.fast). Pull data from 10+ real-time sources, store it locally, and serve it directly to clients over WebSocket and REST — no middleman.

<img width="1676" height="1018" alt="chrome_yKC5BAXXx9" src="https://github.com/user-attachments/assets/55f1ff8c-aed0-405c-8771-4b5f956e1ee6" />

*fully functional web UI to manage node. selfhosted or managed, browser connects directly to your node.*


Run in **public mode** to submit signals to the network and earn (soon). Run in **private mode** for personal use with no network dependencies.

```
 Sources          Node              Clients
─────────      ──────────────      ──────────
 ADS-B   ──→  │              │──→  WebSocket
 AIS     ──→  │  normalize   │──→  REST API
 Oref    ──→  │  store       │
 OTX     ──→  │  broadcast   │──→  (optional)
 USGS    ──→  │              │──→  Hydra Network
 ...     ──→  │              │     (public mode)
              └──────────────┘
```

---

> **Don't want to run your own hardware?**
> [Host a managed node on Hydra — it's free to get started →](https://hydra.fast/platform)

---

## Install

**Pre-built binary (recommended)**

```bash
# Linux amd64
curl -sSLO https://github.com/hydraterminal/node/releases/latest/download/hydra-node-linux-amd64
chmod +x hydra-node-linux-amd64 && sudo mv hydra-node-linux-amd64 /usr/local/bin/hydra-node
```

Other platforms: [github.com/hydraterminal/node/releases](https://github.com/hydraterminal/node/releases)

**Docker**

```bash
docker pull ghcr.io/hydraterminal/node:latest
docker run -v ~/.hydra:/data ghcr.io/hydraterminal/node:latest start --config /data/node.yaml
```

**From source**

```bash
git clone https://github.com/hydra/hydra-node
cd hydra-node
make build
./bin/hydra-node --version
```

Requirements: Go 1.21+, SQLite (bundled via CGO-free driver — no system install needed)

---

## Quick Start

```bash
# Generate a config file
hydra-node init

# Start the node
hydra-node start

# Check status
hydra-node status
```

The node binds to `0.0.0.0:4001` (API) and `127.0.0.1:4002` (control plane) by default. Your data directory is `~/.hydra/data`.

---

## Configuration

Config lives at `~/.hydra/node.yaml` (or pass `--config /path/to/node.yaml`). See [`configs/node.example.yaml`](configs/node.example.yaml) for the full reference.

**Minimal config:**

```yaml
mode: private  # or "public" to earn rewards

server:
  port: 4001

storage:
  dataDir: ~/.hydra/data
  retentionHours: 24

sources:
  adsb:
    enabled: true
    interval: 60s
    credentials:
      apiKey: YOUR_RAPIDAPI_KEY

  usgs:
    enabled: true
    interval: 5m
```

**Public mode** additionally requires registering your node on the [Hydra Platform](https://hydra.fast/platform) to get a node ID and shared secret:

```yaml
mode: public

node:
  id: node_xxxxxxxxxxxx

network:
  backendUrl: https://api.hydra.fast
  submissionInterval: 1m
  heartbeatInterval: 30s
  batchSize: 100
```

Set your node secret via env var: `HYDRA_NODE_SECRET=your_secret`

---

## Data Sources

10 sources are available. Enable the ones you have credentials for:

| Source | What it collects | Auth needed |
|--------|-----------------|-------------|
| `adsb` | Military aircraft positions | RapidAPI key |
| `ais` | Global vessel tracking | AIS Stream key |
| `oref` | Israel missile/rocket alerts | None |
| `cyber` | OTX cyber threat pulses | OTX API key (free) |
| `usgs` | Earthquakes M2.5+ | None |
| `airspace` | SIGMETs, NOTAMs, restricted zones | None (OpenAIP optional) |
| `telegram` | Public channel OSINT | None |
| `xtracker` | OSINT account monitoring | Cookies optional |
| `polymarket` | Geopolitical prediction markets | None |
| `maritime` | MARAD security advisories | None |

For full source configuration options, see the [docs →](https://hydra.fast/docs/node/sources)

---

## Building

```bash
make build          # current platform
make build-all      # all platforms (linux, darwin, windows)
make docker         # Docker image
make test           # run tests with race detector
make lint           # golangci-lint
```

Binaries output to `./bin/`.

Cross-compile targets: `linux-amd64`, `linux-arm64`, `darwin-amd64`, `darwin-arm64`, `windows-amd64`

---

## API & WebSocket

The node exposes a public and authenticated API on port 4001. Clients connect directly — no Hydra backend involved.

Full API reference in the [docs →](https://hydra.fast/docs/node/api)

API keys are issued from your [Hydra Platform dashboard](https://hydra.fast/platform) and validated by the node on startup (cached 5 min). (keys are stored on your node, you can optionally query the node api to create api keys directly.)

---

## Control Plane

The node runs a separate control server on `127.0.0.1:4002` (localhost only):

```bash
# Check live status
curl http://localhost:4002/control/status

# Stream logs
curl http://localhost:4002/control/logs

# Restart a specific source without stopping the node
curl -X POST http://localhost:4002/control/restart-source/adsb

# Prometheus metrics
curl http://localhost:4002/control/metrics
```

---

## Docker Compose

```yaml
services:
  hydra-node:
    image: ghcr.io/hydraterminal/node:latest
    restart: unless-stopped
    ports:
      - "4001:4001"
    volumes:
      - ./node.yaml:/etc/hydra/node.yaml
      - hydra-data:/data
    environment:
      - HYDRA_NODE_SECRET=${HYDRA_NODE_SECRET}
    command: start --config /etc/hydra/node.yaml

volumes:
  hydra-data:
```

---

## Docs

- [Full configuration reference](https://hydra.fast/docs/node/config)
- [Source setup & credentials](https://hydra.fast/docs/node/sources)
- [API reference](https://hydra.fast/docs/node/api)
- [Registering with the network](https://hydra.fast/docs/node/registration)
- [Rewards & public mode](https://hydra.fast/docs/node/rewards)
- [Troubleshooting](https://hydra.fast/docs/node/troubleshooting)

---

## Don't want to manage your own node?

[**Get a managed Hydra node — free to start →**](https://hydra.fast/platform)

We handle the infrastructure. You get a fully operational node in under a minute, with the same API surface, same data sources, and the same reward eligibility as self-hosted. Upgrade when you need more capacity.
