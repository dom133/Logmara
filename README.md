# Syslytics

Web-based syslog monitoring, parsing, and visualization platform with Docker Compose deployment.

## Architecture

```
┌──────────┐     ┌──────────┐     ┌──────────┐
│  Client   │────▶│ Nginx    │────▶│ Go API   │
│  (Browser)│     │  (:80)   │     │  (:8080) │
└──────────┘     └──────────┘     └────┬─────┘
                                       │
                    ┌──────────┐       │       ┌──────────────┐
                    │ rsyslog  │       │       │   PostgreSQL   │
                    │  (:514)  │───────┘       │    (:5432)     │
                    └──────────┘               └──────────────┘
```

| Service | Port | Description |
|---------|------|-------------|
| Frontend | 80 | React SPA served via Nginx |
| API | 8080 | Gin REST API with JWT auth |
| rsyslog | 514 (TCP/UDP) | Syslog ingestion daemon |
| PostgreSQL | 5432 | Persistent log storage |

## Quick Start (Single Server)

This is the default, fully-supported deployment path: **one server, one command, everything included** — frontend, API, rsyslog ingestion, and PostgreSQL all start together from `docker-compose.yml` (see the Architecture diagram above), nothing to set up separately. It is unaffected by the optional High Availability path below; skip that section entirely unless you specifically need to survive losing an entire server.

### Requirements

- One Linux server (any distro with a current kernel), reachable from wherever your browser and syslog senders are.
- Nothing else needs to be pre-installed — the steps below install Docker for you.

### 1. Install Docker

```bash
curl -fsSL https://get.docker.com | sh
sudo systemctl enable --now docker
sudo usermod -aG docker "$USER"   # log out/in for this to take effect
```

### 2. Open firewall ports

Only two things need to be reachable from outside the server:
- `80/tcp` (and `443/tcp` if you enable HTTPS later from the admin Settings UI) — the web UI/API, reverse-proxied through nginx.
- `514/tcp` and `514/udp` — syslog ingestion, from whatever devices will send logs here.

```bash
# Example using ufw - adjust to your actual firewall/security group
sudo ufw allow 80,443/tcp
sudo ufw allow 514/tcp
sudo ufw allow 514/udp
```

`8080/tcp` (the API) is also published by `docker-compose.yml`, but only needed if you want to hit the API directly instead of through nginx on 80/443 — leave it firewalled off unless you have a specific reason to open it.

### 3. Clone the repo and configure

```bash
git clone https://gitlab.dom133.xyz/dominik.kruszewski/syslog_gui.git
cd syslog_gui
cp .env.example .env
```

Edit `.env` and set at least these before going anywhere near production — the defaults in `.env.example` are placeholders, not secrets:
- `POSTGRES_PASSWORD`
- `JWT_SECRET` (e.g. `openssl rand -base64 48`)
- `ADMIN_PASSWORD` — not actually used to create the account (see step 5), but still worth setting since it's passed to the container regardless
- `TZ` if `Europe/Warsaw` isn't your timezone

### 4. Start it

```bash
docker compose up -d --build
```

`-d` runs it in the background so it survives you logging out. Every service already has `restart: unless-stopped` in `docker-compose.yml`, and `systemctl enable docker` from step 1 means Docker itself starts on boot — so the stack comes back up automatically after a server reboot, no extra systemd unit needed.

Check it actually came up:
```bash
docker compose ps          # all services should show "healthy" or "running"
docker compose logs -f     # follow logs if something looks wrong
```

### 5. First login

Open `http://<server-ip>` in a browser and complete the Setup Wizard (creates the admin account and finalizes database/security settings). On subsequent launches, log in with the account you created there.

### Updating later

```bash
git pull
docker compose up -d --build
```

## Configuration

Copy `.env.example` and adjust values:

| Variable | Default | Description |
|----------|---------|-------------|
| `DATABASE_URL` | *(none)* | PostgreSQL connection string. In `docker-compose.yml` this is always set. If unset, the API serves only the setup wizard until it's submitted with database settings, then connects and continues booting on its own |
| `JWT_SECRET` | *(auto-generated)* | JWT signing key — set explicitly in production |
| `ENCRYPTION_KEY` | *(auto-generated)* | AES-256 key for encrypting sensitive settings |
| `PORT` | `8080` | API server port |
| `LOG_FILE_PATH` | `/data/logs.jsonl` | Path to rsyslog JSON output |
| `CORS_ORIGINS` | *(none)* | Comma-separated allowed origins for CORS. Only seeds the initial value in the database on first initialization — afterwards it's managed from the admin Settings UI |
| `LDAP_SERVER` | *(none)* | LDAP/AD server hostname |
| `LDAP_PORT` | `636` | LDAP server port |
| `LDAP_USE_TLS` | `true` | Enable TLS for LDAP |
| `LDAP_VERIFY_CERT` | `true` | Verify LDAP server certificate |
| `LDAP_CA_CERT` | *(none)* | Path to CA certificate for LDAP |
| `LDAP_BASE_DN` | *(none)* | Base DN for user search |
| `LDAP_BIND_DN` | *(none)* | Bind DN for LDAP queries |
| `LDAP_BIND_PASSWORD` | *(none)* | Bind password for LDAP queries |
| `DOCKER_PROXY_URL` | `http://docker-proxy:2375` | Base URL of the `docker-proxy` sidecar backing Admin > Health — see [Health Monitoring](#health-monitoring) |

## High Availability Deployment (Multi-Node, Optional)

An additional, opt-in deployment path for surviving the loss of an entire server, not just a container. It does not replace or modify the single-server `docker-compose.yml` path above — that keeps working exactly as documented whether or not you ever use this section.

**Scope**: this gives *true* horizontal scale-out for `api`/`frontend` (multiple replicas serving traffic concurrently, not just failover) and *true* HA for PostgreSQL (automatic leader election/failover via Patroni) and Redis (automatic leader election/failover via Sentinel). `rsyslog` remains single-active-writer by design (see below), which is a correctness requirement, not a current limitation.

Getting here required backend code changes (not just Docker config) — see "How multi-replica safety works" below for what changed and why.

### Requirements

- **4+ Linux servers**, Docker installed, all mutually reachable. Recommended minimum split: 3 dedicated to Postgres, 3 for Redis Sentinel (can overlap with the app tier on small clusters), 2+ for the app tier (`api`/`frontend`/`rsyslog`/`haproxy`) so that tier also survives a single node failure.
- A container image registry every node can pull from (e.g. a private registry, GHCR, ECR).
- An NFS export (or equivalent shared filesystem — Ceph, GlusterFS, etc. if you already run one) reachable from every app/edge node, for the shared `/data` (raw logs, TLS certs, nginx conf snippets).
- Two or more externally-routable IPs on the edge nodes for the keepalived VIP.

### Files

| File | Purpose |
|------|---------|
| [`docker-stack.postgres.yml`](docker-stack.postgres.yml) | etcd (3-node DCS) + Patroni-managed Postgres (3 nodes) + HAProxy routing writes to the current leader |
| [`Dockerfile.patroni`](Dockerfile.patroni), [`patroni/`](patroni/) | Patroni on top of the same `postgres:16-alpine` base as `docker-compose.yml` |
| [`haproxy/haproxy.cfg`](haproxy/haproxy.cfg) | Routes `:5000` to whichever Postgres node's Patroni REST API reports itself as primary |
| [`docker-stack.redis.yml`](docker-stack.redis.yml) | 3-node Redis + 3-node Sentinel, backing `backend/sharedstate` (rate limiting, cache invalidation, tailer leader election, ingestion control, slow-query log) |
| [`redis/sentinel.conf.tpl`](redis/sentinel.conf.tpl) | Sentinel config template loaded as a Swarm config at deploy time |
| [`docker-stack.app.yml`](docker-stack.app.yml) | `api`, `rsyslog`, `frontend` as Swarm services with real `deploy:` (placement, restart, update policy, `api`/`frontend` at `${API_REPLICAS:-2}`/`${FRONTEND_REPLICAS:-2}`) |
| [`keepalived/`](keepalived/) | VRRP config template + health-check script for the floating syslog ingress VIP |
| [`scripts/swarm-bootstrap.sh`](scripts/swarm-bootstrap.sh) | Guided commands for swarm init/join, node labeling, network/secret/config creation |

### How multi-replica safety works

Running more than one `api`/`frontend` replica used to be unsafe: an in-memory rate limiter and stat caches would silently diverge per replica, the log-file tailer would double-ingest if two instances tailed the same file, and nginx config pushes only ever reached one frontend replica. `backend/sharedstate` (backed by the Redis/Sentinel stack above) fixes all of it:

- **Rate limiting** (`backend/main.go`) — a Lua script does an atomic sliding-window check against Redis instead of an in-process map, so limits are shared across every replica.
- **Tailer leader election** (`backend/tailer/tailer.go`) — a Redis lock (`SET NX PX` + periodic renew, the standard Redis distributed-lock pattern) ensures exactly one `api` replica is ever actively tailing/flushing/compacting `logs.jsonl` at a time; losing the lock (crash, node loss) lets another replica take over within a few seconds.
- **Cache invalidation** (`backend/handler/stats.go`, `logs.go`) — a purge or alias update now publishes over Redis pub/sub so every replica's local cache clears, not just the one that handled the request.
- **Ingestion pause/resume** (`backend/control/ingestion.go`) — the pause flag lives in Redis with a local cached copy kept current via pub/sub, so `GET /admin/ingestion/status` is consistent no matter which replica answers.
- **Slow-query log** (`backend/handler/slow_query_logger.go`) — moved from a per-process in-memory ring buffer to a Redis list, so the admin view is consistent across replicas.
- **Migration safety** (`backend/db/db.go`'s `MigrateWithLock`) — a Postgres advisory lock serializes concurrent replicas' schema migrations on startup/rolling deploy, independent of Redis.
- **Nginx reload fan-out** (`backend/handler/admin.go`) — resolves `NGINX_RELOAD_TARGETS_HOST` (Swarm's `tasks.frontend`, which returns every replica's task IP) and reloads each one in parallel instead of hitting a single fixed URL.

**All of this is opt-in and additive**: none of it activates unless `REDIS_SENTINEL_ADDRS` (or `REDIS_ADDR`) is set. Without it — the single-server `docker-compose.yml` path, and any deployment that simply doesn't set those vars — every one of the above falls back to exactly its original single-process, in-memory behavior. See `backend/sharedstate/client.go`'s `Connect()` doc comment for details.

`rsyslog` is deliberately **not** part of this scale-out: it stays `mode: global` on `edge=true` nodes behind the keepalived VIP (only the node currently holding the VIP receives traffic, so there's still only one active writer to `logs.jsonl` at any time) — this is what keeps the real sender IP intact for `fromhost_ip` parser matching, which a load-balanced/scaled ingestion tier would lose.

### Deployment steps (from scratch)

This walks through everything from bare servers to a working cluster, using a concrete 6-machine example. Substitute your own hostnames/IPs throughout.

#### 0. Example topology

| Node | Example IP | Swarm role | Labels | Runs |
|---|---|---|---|---|
| `pg1` | 10.0.0.11 | manager | `pg_id=1`, `cache_id=1` | Postgres (Patroni), etcd, Redis, Sentinel |
| `pg2` | 10.0.0.12 | manager | `pg_id=2`, `cache_id=2` | Postgres (Patroni), etcd, Redis, Sentinel |
| `pg3` | 10.0.0.13 | manager | `pg_id=3`, `cache_id=3` | Postgres (Patroni), etcd, Redis, Sentinel |
| `app1` | 10.0.0.21 | worker | `app=true`, `edge=true` | api, frontend, haproxy, rsyslog, keepalived |
| `app2` | 10.0.0.22 | worker | `app=true`, `edge=true` | api, frontend, haproxy, rsyslog, keepalived |
| `nfs1` | 10.0.0.30 | *(not in the swarm)* | — | NFS server for `/data` |

Managers double as the 3 Postgres + 3 Redis nodes (odd number, needed for etcd/Sentinel quorum either way) — this keeps the example to 6 machines total. Workers run everything that needs `app=true`/`edge=true`; with 2 of them, both `api`/`frontend` replicas and rsyslog/keepalived survive losing either one. Scale any tier up by adding more nodes with the matching label instead of changing this topology.

`nfs1` is a plain Linux box outside the swarm — it only needs to be network-reachable from `app1`/`app2`. You can reuse an existing NFS/Ceph/GlusterFS server instead of standing up a new one; adjust step 2 accordingly.

#### 1. Install Docker on every swarm node (`pg1`-`pg3`, `app1`-`app2`)

```bash
curl -fsSL https://get.docker.com | sh
sudo systemctl enable --now docker
sudo usermod -aG docker "$USER"   # log out/in for this to take effect
```

#### 2. Open firewall ports between nodes

Swarm itself needs, between every swarm node (`pg1`-`pg3`, `app1`-`app2`):
- `2377/tcp` — cluster management (managers only need to accept this)
- `7946/tcp` and `7946/udp` — node gossip
- `4789/udp` — overlay network data (VXLAN)

Everything else (Postgres 5432, Patroni's REST API 8008, etcd 2379/2380, Redis 6379, Sentinel 26379) travels over the `syslog_net` overlay network via that same VXLAN tunnel — you do **not** need to open those ports individually between nodes.

What *does* need opening beyond the swarm nodes themselves:
- `app1`/`app2`: `80/tcp`, `443/tcp` (frontend, published with `mode: host`) and `514/tcp`+`514/udp` (rsyslog, also `mode: host`) to whatever network your users/log senders are on.
- `nfs1`: `2049/tcp` (NFS) and `111/tcp`+`111/udp` (portmapper), open to `app1`/`app2` specifically.

```bash
# Example using ufw on app1/app2 - adjust to your actual firewall/security groups
sudo ufw allow from 10.0.0.0/24 to any port 2377,7946 proto tcp
sudo ufw allow from 10.0.0.0/24 to any port 7946,4789 proto udp
sudo ufw allow 80,443,514/tcp
sudo ufw allow 514/udp
```

#### 3. Set up the NFS export (`nfs1`)

```bash
sudo apt-get install -y nfs-kernel-server
sudo mkdir -p /srv/syslog-ha/nfs/log_data /srv/syslog-ha/nfs/log_spool
sudo chown -R nobody:nogroup /srv/syslog-ha/nfs
echo '/srv/syslog-ha/nfs/log_data  10.0.0.21(rw,sync,no_subtree_check) 10.0.0.22(rw,sync,no_subtree_check)' | sudo tee -a /etc/exports
echo '/srv/syslog-ha/nfs/log_spool 10.0.0.21(rw,sync,no_subtree_check) 10.0.0.22(rw,sync,no_subtree_check)' | sudo tee -a /etc/exports
sudo exportfs -ra
sudo systemctl enable --now nfs-kernel-server
```

On `app1`/`app2`, install the NFS client (Docker's `local` volume driver shells out to it for `type: nfs` volumes — see `docker-stack.app.yml`):

```bash
sudo apt-get install -y nfs-common
```

#### 4. Clone the repo and build/push images

Pick one manager (`pg1` here) as your "control" node — everywhere below that says "run on a manager", run it there, over SSH, against `pg1`'s local Docker socket. You'll also need a container registry every node can pull from (private registry, GHCR, ECR, etc.).

```bash
ssh pg1
git clone https://gitlab.dom133.xyz/dominik.kruszewski/syslog_gui.git
cd syslog_gui

export REGISTRY=registry.example.com/syslytics TAG=v1
docker build -f Dockerfile.backend  -t $REGISTRY/syslytics-api:$TAG .
docker build -f Dockerfile.rsyslog  -t $REGISTRY/syslytics-rsyslog:$TAG .
docker build -f Dockerfile.frontend -t $REGISTRY/syslytics-frontend:$TAG .
docker build -f Dockerfile.patroni  -t $REGISTRY/syslytics-patroni:$TAG .
docker push $REGISTRY/syslytics-api:$TAG
docker push $REGISTRY/syslytics-rsyslog:$TAG
docker push $REGISTRY/syslytics-frontend:$TAG
docker push $REGISTRY/syslytics-patroni:$TAG
```

#### 5. Initialize the swarm and join the other nodes

On `pg1`:
```bash
./scripts/swarm-bootstrap.sh init-manager 10.0.0.11
# prints a manager join token and a worker join token - copy both
```

On `pg2` and `pg3` (as additional managers, for etcd/Sentinel quorum):
```bash
docker swarm join --token <manager-token> 10.0.0.11:2377
```

On `app1` and `app2` (as workers):
```bash
docker swarm join --token <worker-token> 10.0.0.11:2377
```

Verify from `pg1`: `docker node ls` should list all 5 nodes as `Ready`.

#### 6. Label the nodes

Back on `pg1`:
```bash
./scripts/swarm-bootstrap.sh label-pg pg1 1
./scripts/swarm-bootstrap.sh label-pg pg2 2
./scripts/swarm-bootstrap.sh label-pg pg3 3
./scripts/swarm-bootstrap.sh label-cache pg1 1
./scripts/swarm-bootstrap.sh label-cache pg2 2
./scripts/swarm-bootstrap.sh label-cache pg3 3
./scripts/swarm-bootstrap.sh label-app app1
./scripts/swarm-bootstrap.sh label-app app2
./scripts/swarm-bootstrap.sh label-edge app1
./scripts/swarm-bootstrap.sh label-edge app2
```

#### 7. Create the shared network, secrets, and configs

Still on `pg1`:
```bash
./scripts/swarm-bootstrap.sh network

PG_SUPERUSER_PASS=$(openssl rand -base64 32)
PG_REPLICATION_PASS=$(openssl rand -base64 32)
PG_APP_PASS=$(openssl rand -base64 32)
REDIS_PASS=$(openssl rand -base64 32)
# Save these somewhere safe (e.g. a password manager) - you'll need
# PG_APP_PASS and REDIS_PASS again in step 10.

./scripts/swarm-bootstrap.sh secrets "$PG_SUPERUSER_PASS" "$PG_REPLICATION_PASS" "$PG_APP_PASS"
./scripts/swarm-bootstrap.sh redis-secret "$REDIS_PASS"
./scripts/swarm-bootstrap.sh haproxy-config
./scripts/swarm-bootstrap.sh redis-sentinel-config
```

#### 8. Create local data directories on the Postgres nodes

On each of `pg1`, `pg2`, `pg3`:
```bash
sudo mkdir -p /srv/syslog-ha/pg /srv/syslog-ha/etcd
```
These are node-local bind mounts, deliberately *not* on the shared NFS — Patroni replicates via WAL streaming, not a shared filesystem, and two Postgres instances sharing one data directory would corrupt it. Ownership is fixed up automatically by the Patroni container's entrypoint on first start. Redis needs no data directories at all (deliberately non-persistent — see `docker-stack.redis.yml`'s comments).

#### 9. Deploy Postgres, then Redis

On `pg1`:
```bash
docker stack deploy -c docker-stack.postgres.yml syslog-pg
watch docker service ls   # wait for postgres1/2/3 and etcd1/2/3 at 1/1, haproxy at 2/2
```

Once that's healthy:
```bash
docker stack deploy -c docker-stack.redis.yml syslog-redis
watch docker service ls   # wait for redis1/2/3 and sentinel1/2/3 at 1/1
```

Sanity-check leader election worked: `docker exec -it $(docker ps -qf name=syslog-pg_postgres1) curl -s localhost:8008/` should show `"role": "master"` on exactly one of `postgres1/2/3`.

#### 10. Deploy the app tier

Still on `pg1` (or wherever you're driving `docker stack deploy` from), export the values from step 7:

```bash
export REGISTRY=registry.example.com/syslytics TAG=v1
export NFS_SERVER=10.0.0.30
export JWT_SECRET=$(openssl rand -base64 48)
export ADMIN_PASSWORD=$(openssl rand -base64 16)
export POSTGRES_PASSWORD="$PG_APP_PASS"      # from step 7
export REDIS_PASSWORD="$REDIS_PASS"          # from step 7

docker stack deploy -c docker-stack.app.yml syslog-app
watch docker service ls   # wait for api and frontend at 2/2, rsyslog global at 2/2
```

#### 11. Set up keepalived on the edge nodes

On each of `app1` and `app2` (outside Swarm — VRRP needs direct L2 access the overlay network doesn't provide):

```bash
sudo apt-get install -y keepalived
```

Render `keepalived/keepalived.conf.tpl` for this node (see the template's own comments for each placeholder) and write it to `/etc/keepalived/keepalived.conf`. On `app1` (`STATE=MASTER`, higher `PRIORITY`) and `app2` (`STATE=BACKUP`, lower `PRIORITY`), pointing `unicast_peer` at each other's real IP and both at the same floating `VIP` (e.g. `10.0.0.100`). Also copy `keepalived/check_rsyslog.sh` to `/etc/keepalived/check_rsyslog.sh` and `chmod +x` it.

```bash
sudo systemctl enable --now keepalived
```

Point syslog senders at the VIP (`10.0.0.100:514`, tcp or udp) and, if you're routing browser/API traffic through it too, point clients/DNS at the same VIP for `80`/`443`.

#### 12. First login

Open `http://<vip-or-any-app-node-ip>` in a browser and complete the Setup Wizard (creates the admin account) — same first-run flow as the single-server Quick Start. From then on, log in with the account you just created.

### Testing failover

- Kill the Patroni leader's node → `docker service logs syslog-pg_haproxy` and the Patroni REST API (`curl http://<any-pg-node>:8008/`) should show a new leader within a few seconds, with no manual steps.
- Kill the Redis node currently acting as Sentinel's master → the other two Sentinels should promote a replica within a few seconds (`docker service logs syslog-redis_sentinel1` shows the failover); `api` replicas using `go-redis`'s Sentinel-aware client should reconnect to the new master automatically, and exactly one of them should log `"tailer: acquired leader lock"` shortly after.
- Kill the node running one `api`/`frontend` replica → the other replica(s) keep serving without interruption; `docker service ps syslog-app_api` should show the lost one rescheduled onto the other `app=true` node.
- Kill the edge node currently holding the VIP → keepalived should fail over in 1-3s; confirm with `ip addr` on the new holder and by sending a test syslog message during the cutover.
- Send syslog messages throughout each test and confirm no duplicates or gaps in the logs table, and that `/admin/slow-queries` and dashboard stats look the same regardless of which `api` replica answers the request.

### Updating images (rolling update)

When you change code, config, or dependencies, rebuild your images, push them under a **new tag**, then re-deploy each stack so Swarm performs a rolling update (no downtime if done in the right order).

#### 1. Build and push new images

```bash
ssh pg1
cd syslog_gui
git pull   # or switch to the branch/commit you want

export REGISTRY=registry.example.com/syslytics TAG=v2   # <-- bump the tag

docker build -f Dockerfile.backend   -t $REGISTRY/syslytics-api:$TAG .
docker build -f Dockerfile.rsyslog   -t $REGISTRY/syslytics-rsyslog:$TAG .
docker build -f Dockerfile.frontend  -t $REGISTRY/syslytics-frontend:$TAG .
docker build -f Dockerfile.patroni   -t $REGISTRY/syslytics-patroni:$TAG .
docker push $REGISTRY/syslytics-api:$TAG
docker push $REGISTRY/syslytics-rsyslog:$TAG
docker push $REGISTRY/syslytics-frontend:$TAG
docker push $REGISTRY/syslytics-patroni:$TAG
```

#### 2. Re-deploy stacks (rolling update)

Deploy in this order so that data-tier services are current before the app tier connects to them:

```bash
# Postgres + etcd (uses stop-first: old task stops before new one starts)
docker stack deploy \
  --resolve-image always \
  --with-registry-auth \
  -c docker-stack.postgres.yml syslog-pg

# Redis + Sentinel (stop-first default)
docker stack deploy \
  --resolve-image always \
  --with-registry-auth \
  -c docker-stack.redis.yml syslog-redis

# App tier: api/frontend (start-first) + rsyslog (global)
docker stack deploy \
  --resolve-image always \
  --with-registry-auth \
  -c docker-stack.app.yml syslog-app
```

`--resolve-image always` forces every node to pull the latest image from the registry before starting the new task. Without it, Swarm reuses the locally cached image and your update silently does nothing.

#### 3. Force an update (config/secret changes)

If you only changed a Swarm secret, config, or environment variable (not the image itself), re-deploy with `--force` to trigger a rolling restart:

```bash
docker service update --force syslog-app_api
docker service update --force syslog-app_frontend
docker service update --force syslog-app_rsyslog
```

Or re-deploy the whole stack with both flags:

```bash
docker stack deploy --resolve-image always --with-registry-auth -c docker-stack.app.yml syslog-app
```

#### 4. Watch the rollout

```bash
watch docker service ls          # services move from "old/NEW" to "NEW/NEW" as tasks converge
docker service ps syslog-app_api # per-task status — look for "Running" replacing the old slot
docker service logs -f syslog-app_api   # follow logs during the update
```

Each stack's `update_config` controls the pace:

| Stack | Policy | Meaning |
|-------|--------|---------|
| `syslog-pg` (postgres1/2/3) | `stop-first` | Old Patroni node stops, new one starts — Patroni re-elects leader on the new task |
| `syslog-redis` (redis/sentinel) | default | One-by-one rolling restart; Sentinel quorum stays intact |
| `syslog-app` (api/frontend) | `start-first`, parallelism 1 | New replica starts and becomes healthy before the old one is removed — zero-downtime |
| `syslog-app` (rsyslog) | `mode: global` | Updates every `edge=true` node one at a time; only the VIP-holder receives traffic |

#### 5. Roll back if something went wrong

If a new image breaks, revert to the previous tag:

```bash
export TAG=v1   # previous working tag

docker stack deploy \
  --resolve-image always \
  --with-registry-auth \
  -c docker-stack.app.yml syslog-app
```

Swarm rolls back each replica in the same rolling fashion. For a faster emergency rollback, drain the affected node:

```bash
docker node drain app1
```
This forces all tasks on `app1` to reschedule onto other `app=true` nodes, giving you time to fix the image.

## Syslog Relay (Optional, Multi-VLAN)

Lets one or more small, standalone rsyslog hosts sit in VLANs that don't route directly to the central server, collect syslog from local devices, and forward it over an authenticated, encrypted channel (mTLS on port 6514) to this server's normal ingestion pipeline. The central server keeps accepting direct syslog on 514 from its own VLAN exactly as in the Quick Start — relays are additive, not a replacement, and any number of them can point at the same central server. This works the same whether the central side is a single server (`docker-compose.yml`) or the [High Availability](#high-availability-deployment-multi-node-optional) multi-node stack above.

```
VLAN A (devices)          VLAN B (DMZ/relay)              VLAN C (central)
 device1 ─┐
 device2 ─┼─► syslog-relay ──mTLS 6514/tcp──► rsyslog ──► logs.jsonl ──► tailer
 device3 ─┘   (small server,                      ▲        (unchanged)
               forward-only)                       │
VLAN D (DMZ/relay 2)                                │
 device4 ──► syslog-relay-2 ──────mTLS 6514/tcp──────┘
```

Each relay reuses the same JSON conversion the central server already does locally (`rsyslog/syslog.conf`'s `JsonLines` template), so `fromhost_ip` stays the real device IP — the field parser matching and per-device stats rely on — instead of becoming the relay's own IP.

### How it's secured

- **mTLS**: the central server runs its own internal CA (generated automatically the first time you use this feature — see `backend/relaypki`). The CA uses RSA 4096-bit keys. Every relay gets a client certificate signed by that CA; the central listener rejects any connection without a valid one.
- **IP whitelist**: a valid certificate alone isn't enough — the peer's IP must also be on the whitelist (Admin > Syslog Relay > Whitelist IP). Together these mean only a relay you've explicitly approved, from an IP you've explicitly approved, gets in.
- **Revocation is a real, immediate cutoff** — there's no X.509 CRL/OCSP at the TLS layer, so instead every relay certificate gets a CommonName unique to that one issuance (`label#serial`, see `backend/relaypki`), and the mTLS listener's `StreamDriver.AuthMode="x509/name"` only accepts a handshake whose CommonName is in the current `StreamDriver.PermittedPeers` list — regenerated, alongside the IP allow-list, in the very same `allowed-relays.conf` every time a certificate is issued or revoked. Revoking one (Admin > Syslog Relay > Certificates > Revoke), or replacing it via Regenerate/Renew, drops its exact CommonName from that list, so the *old* key specifically stops working — not just "some cert signed by our CA" — even though it's still cryptographically valid and unexpired.
  - Applying this needs a real `rsyslogd` restart, not just a config nudge: rsyslog has no lightweight reload (`SIGHUP` only reopens output files), so `entrypoint.sh` runs `rsyslogd` as a supervised child and the reload sidecar kills just that child to force a restart against the regenerated config. This briefly interrupts ingestion on **both** 514 and 6514 (one process serves both), every time a relay whitelist/certificate change is applied.
  - The whitelist entry itself is left in place either way, now shown as **Blocked** on the Whitelist IP tab, rather than deleted — generate a replacement certificate for it (from either tab) to restore access.
  - Removing an IP from the **whitelist** entirely (Whitelist IP > delete) also revokes its certificate, since a device that's no longer allowed in shouldn't leave an "issued" certificate lying around either.
  - The old, revoked certificate row is always kept for the audit trail — "Regenerate" on a revoked row issues a fresh certificate (with its own fresh CommonName) for the same entry without deleting its history.
- The private key for a relay's certificate is generated on the server but **never stored** there — it's handed to you exactly once, in the `.tar.gz` bundle the browser downloads when you generate it. If you lose it, revoke (or regenerate from) that certificate; there's no way to re-download the old key.

### Certificate expiry, renewal, and CA rotation

- **Relay certificates** are valid 5 years from issuance; the Certificates tab shows each one's expiry date, with an amber "Expires in Nd" badge once it's within 30 days and a red "Expired" badge past that. A certificate in that window gets a **Renew** action (alongside Revoke) that issues a replacement for the same whitelist entry, downloads it once (same as generating a fresh one), and revokes the old certificate as soon as the new one is linked — no gap where the entry has no active certificate at all. Renewing outside that 30-day window isn't allowed; revoke the certificate first if you need to replace it early.
- **Get warned before it happens**: add an Alert rule (Alerts > New Alert Rule) with type "Syslog relay certificate expiring" and a "Warn Before Expiry (days)" threshold — it's checked hourly against every relay certificate and fires (through whichever notification channels you assign, same as any other alert) once a certificate falls inside that window, subject to the rule's cooldown so it doesn't renotify every hour.
- **The CA and the central listener's own server certificate renew themselves automatically** — the CA is valid 15 years, the server certificate 10, and neither needs any admin action: `relaypki.EnsureCA` checks both on every relay config sync (every whitelist/certificate change, plus an hourly background check — see `backend/main.go`) and re-signs whichever is within its renewal window (1 year out for the CA, 90 days for the server certificate). The CA specifically is re-signed **using the same private key**, just with a fresh validity window — TLS chain verification only needs the issuer's public key and a currently-valid, name-matching trust anchor, not the exact certificate object presented when a given relay certificate was originally signed, so every previously issued relay certificate keeps validating without being reissued or redistributed. This only handles ordinary expiry, not a suspected key compromise — that needs a real rotation (delete `/data/relay/ca.*` and `server.*`, restart, then reissue and redistribute a certificate to every relay), which isn't automatic and isn't something you should need to do on a routine basis.

### Enabling it

1. Admin > Settings > **Syslog Relay** > turn on "Enable Syslog Relay Ingestion". This starts accepting mTLS connections on port 6514 (still gated by the whitelist below, so nothing gets in until you add a relay).
2. A new **Syslog Relay** entry appears in the sidebar. Either open **Certificates** > "Generate Certificate" and give the relay a label and the IP it will connect from directly, or first add the IP under **Whitelist IP** and generate a certificate for that entry afterwards (its "Generate Certificate" row action) — useful if you want the IP approved before a certificate exists for it. Either way, the browser downloads `syslog-relay-<label>.tar.gz` — save it now, this is the only copy.
3. Copy that file to the small server you're deploying in the client VLAN, alongside `docker-compose.relay.yml` and `Dockerfile.rsyslog-relay` from this repo:
   ```bash
   mkdir -p relay-bundle && tar xzf syslog-relay-<label>.tar.gz -C relay-bundle
   docker compose -f docker-compose.relay.yml up -d --build
   ```
4. Point the devices in that VLAN at the relay's IP on port 514 (tcp or udp), same as you would the central server directly.

The target host baked into every generated `relay.conf` comes from, in order: the **Central Server Address** field under Admin > Settings > Syslog Relay (only editable once ingestion is enabled), then the `RELAY_CENTRAL_HOST` env var on the central server's `api` service, then `127.0.0.1` if neither is set — which only makes sense for same-host testing, so set one of the first two for any real cross-VLAN deployment.

### Firewall

The relay only ever needs one outbound rule: **relay → central, 6514/tcp**. It doesn't need to be reachable *from* the central VLAN at all. On the central side, only 6514/tcp needs to be reachable from the relay's VLAN — devices behind the relay never talk to the central server directly.

### Limitations

- One relay is a single small server with no built-in failover (unlike the edge nodes in the HA section above) — if it goes down, its VLAN stops forwarding until it's back. Run more than one relay (in different VLANs, or the same one) if that's not acceptable; each gets its own certificate and whitelist entry.
- The relay buffers to disk (`queue.type="LinkedList"` with `queue.saveOnShutdown` in the generated `relay.conf`) if the link to the central server drops, and catches up once it's back — but a full disk stops accepting new logs until the backlog is delivered.
- On a relay's very first boot, if the central server's own CA/server certificate hasn't been generated yet, `rsyslog/entrypoint.sh` on the central side generates a throwaway placeholder so its listener can still start; whichever of that script or the API's own cert generation runs first "wins" and both sides converge on the same CA. This only matters during the very first `docker compose up` after enabling the feature.

## Health Monitoring

Admin > Health shows the up/down status of every container the app depends on. It works the same way regardless of which deployment you're running:

- **Single server (`docker-compose.yml`)**: the `docker-proxy` sidecar sees every container on the one host, which is the complete picture — `api`, `frontend`, `rsyslog`, `postgres`, all of it.
- **Docker Swarm (`docker-stack.app.yml`)**: `docker-proxy` is placed on manager nodes only (`node.role == manager`) instead of alongside `api`/`frontend`/`rsyslog` on the `app=true` workers. This matters because Swarm's cluster-wide `/services` and `/tasks` endpoints only answer from a manager — a proxy colocated with `api` (a worker in the [example topology](#deployment-steps-from-scratch)) would only ever see its own node. Placed on a manager instead, it reports every service in the swarm (including the Postgres/Redis stacks `api` never runs alongside), not just the app tier.

### Why a proxy instead of mounting the socket into `api`

`api` never gets `/var/run/docker.sock` directly. Mounting it — even read-only — hands whatever's on the other end of that socket full control of the host: the `:ro` flag only stops writes to the socket *file*, not what the Docker Engine API on the other side of it will do for a caller with access to it. Instead, `/var/run/docker.sock` is mounted only into the small [`tecnativa/docker-socket-proxy`](https://github.com/Tecnativa/docker-socket-proxy) sidecar, which forwards just `GET /containers`, `/services`, `/tasks`, `/nodes`, `/info` and rejects everything else (`POST: 0`). It isn't published to the host — only reachable from other containers on `syslog_net`. A bug in the health handler (`backend/handler/health_docker.go`) can read container/service status and nothing more.

### Syslog Relay is different

A [relay](#syslog-relay-optional-multi-vlan) isn't on `syslog_net` and isn't reachable from the central server at all — by design, its only firewall rule is *outbound* 6514/tcp to the central server (see [Firewall](#firewall)). There's no socket, network path, or open port for the central server to check its container status through. The Health tab shows relay **liveness** instead, derived from data the app already has: whether a log has arrived recently from its whitelisted IP (`mv_device_stats.last_seen`, same rollup the Devices tab and device-silence alerts use) and whether its certificate is still `issued` rather than `revoked`. This is a proxy for "is it up and forwarding," not a container health check — a relay that's up but has nothing to forward will look identical to one that's down.

## Features

- **Live Log Viewer** �?" Browse, filter, and search ingested syslog messages in real-time
- **Parser Engine** �?" Define regex-based parsers to extract structured fields from raw log lines
- **Custom Dashboards** �?" Create dashboards filtered by device, severity, or parsed fields
- **Pin Dashboards** �?" Pin frequently-used dashboards to the sidebar for quick access
- **Export** �?" Download logs as CSV or HTML reports
- **Statistics** �?" Timeline charts, severity breakdown, and per-device metrics
- **Secure Authentication** �?" JWT access tokens (configurable timeout) + refresh tokens (7 days) with rotation, JWT blacklisting on logout
- **Account Lockout** �?" Automatic lockout after configurable failed login attempts, admin unlock from Admin panel
- **CSRF Protection** �?" Double-submit cookie pattern on all mutating endpoints
- **LDAP/AD Integration** �?" Authenticate against Active Directory or OpenLDAP with TLS support
- **Audit Logging** �?" Track login attempts, lockouts, unlocks, password changes, and admin actions
- **Rate Limiting** �?" Login endpoint protected against brute-force attacks (Redis-shared in HA mode)
- **CORS Protection** �?" Configurable allowed origins
- **Setup Wizard** �?" Guided initial configuration with admin account, database, security keys, and optional LDAP/CORS
- **Admin Panel** �?" User management, settings, audit log viewer, LDAP connection test
- **Health Monitoring** �?" Container/Swarm service status and syslog relay liveness in one place (see [Health Monitoring](#health-monitoring))

## GUI Configuration Guide

### Admin Settings
Navigate to **Admin > Settings** to configure application-wide options organized by category:

- **General**:
  - **Log Retention (days)**: How long logs are kept before automatic deletion (1–3650 days).
  - **Session Timeout (minutes)**: Maximum session inactivity before tokens expire (1–10080 minutes, default 15).
- **Security**:
  - **Max Failed Login Attempts**: Number of failed login attempts before account lockout (1–100). Leave empty to use `MAX_FAILED_ATTEMPTS` env var or default (5).
  - **Lockout Duration (minutes)**: How long an account stays locked after reaching max failed attempts (1–1440 minutes). Leave empty to use `LOCKOUT_DURATION_MIN` env var or default (15).
- **CORS**:
  - **Allowed CORS Origins**: Comma-separated list of origins allowed to call the API from a browser. Leave empty to only allow the origin the app is served from.
- **HTTPS**:
  - **Enable HTTPS**: Toggle to enable TLS termination on the API server.
  - **Redirect HTTP to HTTPS**: Force all HTTP traffic to HTTPS.
  - **Upload Certificate/Key**: Upload PEM-formatted certificate and private key files.

### Alerts
Navigate to the **Alerts** tab to create and manage alert rules. Alerts monitor incoming logs and notify you when specific conditions are met.

- **Rule Types**: Choose from predefined alert types:
  - `log_threshold`: Triggers when a specific field value exceeds a threshold.
  - `device_silence`: Triggers when a device stops sending logs for a configured period.
  - `config_change`: Triggers when a configuration change is detected in logs.
  - `relay_cert_expiring`: Triggers when a syslog relay certificate is nearing expiration.
- **Conditions**: Define the matching criteria using:
  - **Field**: The log field to monitor (e.g., `severity`, `parsed_status`, `device`).
  - **Operator**: Comparison method (`equals`, `contains`, `gt`, `lt`, `regex`).
  - **Value**: The value to compare against.
- **Cooling Period**: Set the cooldown duration (default 3600 seconds) to prevent alert fatigue.
- **Notification Channels**: Assign one or more channels to deliver alerts:
  - `email`, `webhook`, `slack`, `teams`, `in_app`, `push`.
- **Testing & History**: Use the "Test Channel" button to verify connectivity. View past alerts and their resolution status in the history panel.

### Parsers
The **Parsers** tab allows you to extract structured data from raw syslog messages using regex patterns.

- **Create Parser**: Click "Create Parser" to define a new rule.
  - **Name & Description**: Identify the parser purpose.
  - **Device Type**: Classify the source (e.g., `mikrotik`, `ubiquiti`, `cisco`).
  - **Match Type**: Determine how logs are matched:
    - `hostname`: Match based on the log hostname.
    - `app_name`: Match based on the application name.
    - `message`: Match against the full message content.
    - `all`: Apply to all incoming logs.
  - **Match Value**: The pattern to match (when not using `message`).
  - **Regex**: Enter a regex with named groups (e.g., `(?P<ip>\d+\.\d+\.\d+\.\d+)`) to extract fields.
  - **Fields**: Define each extracted field's `Name`, `Label`, and `Type` (`string`, `number`, `ip`, `datetime`).
- **Test Parser**: Paste a sample log line into the test modal to verify your regex extracts fields correctly.
- **Management**: Enable/disable parsers, clone existing ones, or trigger a "Reparse" to apply changes to historical unparsed logs.

### Dashboards
The **Dashboards** tab provides customizable views of your log data.

- **Create/Edit Dashboard**: Set a `Name`, `Description`, and `Visibility` (`private` or `public`).
- **Pin to Favorites**: Pin frequently used dashboards to the sidebar for instant access.
- **Filters**: Narrow down data using:
  - **Devices**: Select specific devices or groups.
  - **Severities**: Filter by log level (e.g., Error, Warning, Info).
  - **Parsed Fields**: Filter by extracted field values.
  - **Date Range**: Set a custom time window for analysis.
- **Data Views**: 
  - **Log Table**: Real-time paginated log entries with sortable columns.
  - **Statistics**: Cards showing log counts, severity distribution, and device metrics.
  - **Charts**: Visualize trends over time.
- **Export**: Download dashboard data as CSV or generate HTML reports.
- **Customization**: Adjust visible columns and sort order; settings are saved to your profile.

## Parser Creation Guide

### Overview
Syslytics includes a powerful regex-based parser engine that allows you to extract structured data from raw syslog messages. Parsers can be created directly through the web interface or via API endpoints.

### Creating a Parser
1. Navigate to the **Admin Panel** → **Parsers**
2. Click **Create New Parser**
3. Fill in the following fields:
   - **Name**: Descriptive name for the parser (e.g., "Apache Access Log")
   - **Description**: Optional explanation of what this parser does
   - **Device Type**: The type of device that generates this log format (e.g., "apache", "nginx", "firewall")
   - **Match Type**: How to match the logs:
     - `hostname` - Match on hostname pattern
     - `fromhost_ip` - Match on IP address pattern  
     - `regex` - Match against full message using regex pattern
   - **Match Value**: Pattern to match (when Match Type is not "regex")
   - **Regex**: Regular expression to extract fields from the log message
   - **Enabled**: Enable/disable the parser

### Writing Regex Patterns
- The regex pattern should capture named groups for each field you want to extract
- You can reference the parsed field name in your dashboard configuration
- Example: `(?P<ip>\d+\.\d+\.\d+\.\d+) .* (?P<method>[A-Z]+) (?P<url>.*?) (?P<status>\d+)`
  
### Parser Fields Configuration
In the parser creation interface, you can define the fields that should be extracted:
- **Name**: Internal name of the field (e.g., "ip_address")
- **Label**: Display name for users (e.g., "IP Address")
- **Type**: Data type for filtering (e.g., "string", "integer", "timestamp")

### REST API Integration
Parsers can also be created programmatically using the following API endpoints:

#### Create Parser
```
POST /api/parsers
{
  "name": "Apache Access Log",
  "description": "Parser for Apache access logs",
  "device_type": "apache", 
  "match_type": "regex",
  "match_value": null,
  "regex": "(?P<ip>\\d+\\.\\d+\\.\\d+\\.\\d+) .* (?P<method>[A-Z]+) (?P<url>.*?) (?P<status>\\d+)",
  "enabled": true,
  "fields": [
    {
      "name": "ip_address",
      "label": "IP Address",
      "type": "string"
    },
    {
      "name": "method", 
      "label": "HTTP Method",
      "type": "string"
    }
  ]
}
```

#### Test Parser
You can test your regex against sample log lines:
```
POST /api/parsers/test
{
  "pattern": "(?P<ip>\\d+\\.\\d+\\.\\d+\\.\\d+) .* (?P<method>[A-Z]+) (?P<url>.*?) (?P<status>\\d+)",
  "sample_log": "192.168.1.1 - - [01/Jan/2023:12:00:00 +0000] \"GET /index.html HTTP/1.1\" 200 1234"
}
```

## API Endpoints

### Authentication

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/auth/login` | Authenticate and receive JWT + refresh token |
| POST | `/api/auth/refresh` | Refresh access token using refresh token |
| POST | `/api/auth/logout` | Invalidate refresh token |
| GET | `/api/auth/me` | Get current user profile |
| POST | `/api/auth/change-password` | Change user password |

### Initialization

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/status/initialized` | Check if app is initialized |
| GET | `/api/init/generate-keys` | Generate random JWT secret and encryption key |
| GET | `/api/init/db-config` | Get current database configuration |
| POST | `/api/init` | Initialize app with admin, DB, keys, LDAP, CORS |

### Logs

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/logs` | Query logs with filters |
| GET | `/api/export/csv` | Export logs as CSV |
| GET | `/api/export/html` | Export logs as HTML report |

### Statistics

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/stats/dashboard` | Overview statistics |
| GET | `/api/stats/devices` | Per-device counts |
| GET | `/api/stats/severity` | Severity distribution |
| GET | `/api/stats/timeline` | Hourly timeline data |

### Parsers

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/parsers` | List all parsers |
| POST | `/api/parsers` | Create new parser |
| PUT | `/api/parsers/:id` | Update parser |
| DELETE | `/api/parsers/:id` | Delete parser |
| POST | `/api/parsers/test` | Test regex against sample |
| POST | `/api/parsers/reparse` | Re-parse unparsed logs |
| GET | `/api/parsers/fields` | List extracted field names |

### Dashboards

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/dashboards` | List dashboards (pinned first) |
| POST | `/api/dashboards` | Create dashboard |
| GET | `/api/dashboards/:id` | Get dashboard details |
| PUT | `/api/dashboards/:id` | Update dashboard |
| DELETE | `/api/dashboards/:id` | Delete dashboard |
| GET | `/api/dashboards/:id/data` | Get dashboard log data |
| PATCH | `/api/dashboards/:id/pin` | Toggle pin status |

### Admin

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/admin/users` | List all users |
| POST | `/api/admin/users` | Create user (admin only) |
| PUT | `/api/admin/users/:id` | Update user role/status |
| DELETE | `/api/admin/users/:id` | Delete user |
| PUT | `/api/admin/users/:id/reset-password` | Reset user password |
| GET | `/api/admin/settings` | Get application settings |
| PUT | `/api/admin/settings` | Update application settings |
| POST | `/api/admin/settings/cleanup` | Clean up old logs |
| DELETE | `/api/admin/logs` | Purge all logs |
| GET | `/api/admin/audit-log` | View audit log entries |
| POST | `/api/admin/ldap/test` | Test LDAP connection |
| GET | `/api/admin/health/containers` | Container/Swarm service status + relay liveness (see [Health Monitoring](#health-monitoring)) |


## Project Structure

```
├── docker-compose.yml
├── docker-compose.relay.yml  # Standalone compose for a remote syslog relay host
├── Dockerfile.backend
├── Dockerfile.frontend
├── Dockerfile.rsyslog
├── Dockerfile.rsyslog-relay   # Image for the standalone relay host
├── .env.example
├── backend/
│   ├── main.go              # Entry point, route setup, rate limiter, CORS
│   ├── auth/                 # JWT middleware, refresh tokens, bcrypt
│   ├── db/                   # Database connection, migrations, builtin parsers
│   ├── handler/              # HTTP handlers (auth, logs, parsers, dashboards, admin, init, relay)
│   ├── ldap/                 # LDAP/AD authentication with TLS
│   ├── model/                # Go structs for DB models
│   ├── parser/               # Regex parser engine
│   ├── relaypki/              # Internal CA + relay certificate issuance (mTLS)
│   ├── tailer/               # File tailer for rsyslog JSONL
│   └── util/                 # Key generation, encryption utilities
├── frontend/
│   ├── src/
│   │   ├── App.tsx           # Main layout with pinned sidebar
│   │   ├── pages/            # Page components (including SetupWizard, SyslogRelay)
│   │   └── services/         # API client with 401 interceptor, auth context
│   ├── nginx.conf
│   └── vite.config.ts
└── rsyslog/
    ├── syslog.conf            # rsyslog template + output config, incl. the mTLS relay listener
    ├── entrypoint.sh           # Generates a placeholder relay CA/cert if missing, supervises + restarts rsyslogd
    └── reload-sidecar/         # HTTP sidecar that restarts rsyslogd on relay config changes (SIGHUP can't reload config)
```

## Security

- **JWT Access Tokens** — Short-lived (configurable via `session_timeout_min` setting, default 15 minutes), HS256 signed
- **Refresh Tokens** — 7-day expiry with rotation; all of a user's tokens invalidated on logout via bulk `used = true`
- **JWT Blacklisting** — Access token JTI inserted into `jwt_blacklist` table on logout, checked on every request
- **CSRF Protection** — Double-submit cookie pattern: `csrf_token` cookie (SameSite=Strict) must match `X-CSRF-Token` header on all non-GET requests
- **Account Lockout** — After N failed login attempts (configurable via `security_max_failed_attempts`, default 5), the account is locked for a configurable duration (`security_lockout_duration_min`, default 15 minutes)
- **Admin Unlock** — Administrators can manually unlock any locked user from Admin > Users
- **Inactive Session Expiry** — Background job marks refresh tokens as used when inactive for longer than `session_timeout_min` (default 15 minutes)
- **Timing-Safe Comparison** — Constant-time password verification prevents timing attacks
- **bcrypt** — Password hashing with cost factor 14
- **Rate Limiting** — Lua-backed sliding-window rate limiter on login endpoint (Redis-shared in HA mode, in-memory fallback)
- **Cookie Security** — `Secure` flag respects `X-Forwarded-Proto` header, allowing correct behavior behind HTTP reverse proxies
- **CORS** — Configurable allowed origins; disabled by default
- **Audit Log** — All authentication events, lockouts, unlocks, and admin actions recorded
- **Sensitive Data Masking** — JWT secret, encryption key, and LDAP password masked in API responses
- **Certificate Verification** — LDAP TLS connections verify server certificates by default

## Development

```bash
# Backend (requires Go 1.21+)
cd backend
go run main.go

# Frontend (requires Node 18+)
cd frontend
npm install
npm run dev
```

## Parser Engine

Syslytics includes a robust parser engine that allows you to extract structured data from raw syslog messages using regular expressions.

### How Parsers Work
1. **Pattern Matching**: Parsers match incoming log messages based on hostname, IP address, or regex patterns
2. **Field Extraction**: Regular expressions define named capture groups to extract specific fields
3. **Dynamic Parsing**: As logs arrive, they are automatically parsed by enabled parsers
4. **Dashboard Integration**: Extracted fields can be used for filtering and visualization in dashboards

### Built-in Parsers
The system includes several built-in parsers that detect common log formats:
- SSH login attempts
- Nginx access logs  
- Apache access logs
- Firewall events
- System service messages

### Creating Parsers
Parsers can be created through the web interface or API with the following requirements:
- **Name**: Descriptive identifier for the parser
- **Device Type**: Log source classification (e.g., "nginx", "firewall")
- **Match Criteria**: How to determine if a log matches this parser
- **Regular Expression**: Named capture groups that define extracted fields
- **Fields Configuration**: Define labels and data types for extracted fields

## License

MIT