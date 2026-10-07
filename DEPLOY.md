# Deploying dcrdata on a VPS

This guide walks through a production deployment on a fresh Linux VPS
(Ubuntu 24.04 LTS "Noble" or Debian 13 "Trixie"), including `arm64` hosts. The
explorer runs as a plain Go binary behind [Caddy](https://caddyserver.com/),
which reverse-proxies it and provisions TLS automatically. In production you can
put [Cloudflare](#9-cloudflare-ddos-protection) in front for DDoS protection.

Unlike a single-binary app, dcrdata is a **three-tier stack**: it indexes a
[`dcrd`](https://github.com/decred/dcrd) full node into **PostgreSQL** and serves
the result over HTTP.

**Architecture**

```
                 (TLS, auto)          (HTTP, loopback)
Cloudflare ──► Caddy ──────────────► dcrdata ──┬─► dcrd  (RPC, 127.0.0.1:9109, TLS)
  (proxy)      :443                  :7777      │     └─ Decred P2P network :9108
                                                │
                                                └─► PostgreSQL (unix socket / :5432)
```

- **dcrd** — a full Decred node, run with `--txindex`, fully synced. dcrdata
  talks to its RPC over TLS on loopback.
- **PostgreSQL** — dcrdata's storage. The mainnet database is **large** and the
  initial population is the slow part of the whole deploy.
- **dcrdata** — the Go indexer + web server. Listens on `127.0.0.1:7777`.
- **Caddy** — terminates TLS, redirects HTTP→HTTPS, and reverse-proxies to
  dcrdata. HTTPS is automatic: it obtains and renews Let's Encrypt certificates
  on its own. No certbot, no cron. (This replaces the old `nginx` setup.)

> **Already running dcrd?** If you have a synced `dcrd` (on this host or
> reachable over the network), skip installing another one — see
> [`--skip-dcrd`](#connecting-to-an-existing-dcrd). Otherwise the deploy installs
> and configures a dedicated node.

---

## System requirements

dcrdata's PostgreSQL database for **mainnet** is sizeable and grows over time,
and the initial sync is CPU- and I/O-heavy.

| Resource | Minimum | Recommended |
| --- | --- | --- |
| vCPU | 2 | 4+ |
| RAM | 4 GB | 8–16 GB |
| Disk (SSD/NVMe) | 120 GB | 200 GB+ |
| Network | — | unmetered; the node syncs the whole chain |

> **Plan for the sync.** A from-scratch deploy must (1) sync `dcrd` (the full
> chain with `txindex`, ~15–20 GB) and then (2) let dcrdata build its PostgreSQL
> indexes from genesis. End to end this commonly takes **many hours to over a
> day** depending on disk speed and CPU. The web UI serves a "syncing" status
> page until it finishes. **Use SSD/NVMe** — spinning disks make this painful.

---

## Quick start (automated)

The repository ships a [`deploy.sh`](deploy.sh) that performs every step in this
guide. On a fresh VPS, one line:

```sh
curl -fsSL https://raw.githubusercontent.com/jzbz/data.dcr.pw/master/deploy.sh | sudo bash -s -- --domain explorer.example.com
```

The `-s --` is required: without it `bash` reads `--domain` as its own flag. To
serve plain HTTP on `:80` instead, pass `--http` and no domain.

Or clone first, which lets you read the script before running it and is the
better choice on a link you don't trust:

```sh
git clone https://github.com/jzbz/data.dcr.pw
sudo ./data.dcr.pw/deploy.sh --domain explorer.example.com
```

> Piping to `bash` runs whatever arrives, so a connection that drops mid-transfer
> executes a truncated script. The blast radius here is small — every step is
> idempotent, so re-running finishes the job — but the clone form avoids the
> question entirely.

> By default the script deploys `jzbz/data.dcr.pw`. Point `--repo` at a
> different fork to deploy your own build.

It installs Go, PostgreSQL, dcrd, and Caddy; creates the database; builds the
explorer; and starts everything as hardened systemd services behind Caddy with
automatic HTTPS. The front end is plain CSS + native ES modules, so there is no
Node.js or bundler to install.

The script is idempotent, so **running it again upgrades an existing install**:
it force-syncs the checkout to the latest remote commit, rebuilds, and only swaps
the new binary into place if the build succeeds — a broken build never takes down
the running service. (The force-sync discards local edits to tracked files in the
checkout.)

**Install or upgrade data.dcr.pw.** One line does both: a re-run inherits the
options recorded by the first run. It downloads the whole script before running
it, so a dropped connection cannot execute half of it:

```sh
f=$(mktemp) && curl -fsSL https://raw.githubusercontent.com/jzbz/data.dcr.pw/master/deploy.sh -o "$f" && sudo bash "$f" --domain data.dcr.pw; rm -f "$f"
```

raw.githubusercontent.com can serve the previous `deploy.sh` for a few minutes
after a push; the code it builds is always fetched fresh from the repository.

Useful flags:

| Flag | Effect |
| --- | --- |
| `--domain <host>` | Domain to serve; Caddy provisions a TLS certificate for it. |
| `--http` | Serve plain HTTP on `:80` (no domain) — handy for testing. |
| `--repo <url>` | Git repository to deploy (default: `jzbz/data.dcr.pw`). Point at a fork to override. |
| `--testnet` | Index testnet instead of mainnet. |
| `--skip-dcrd` | Don't install dcrd; connect to an existing node (see below). |
| `--dcrdserv/-user/-pass/-cert` | Coordinates of an existing dcrd (with `--skip-dcrd`). |
| `--go-version <v>` | Go toolchain version (default `1.27.1`). |
| `--dcrd-version <v>` | dcrd module version to `go install` (default `latest`). dcrd 2.1.6 is module `v1.10.8`; its tag, `release-v2.1.6`, works too. |
| `--no-repair` | Don't repair a desynced stake database; only report it (see [Troubleshooting](#troubleshooting)). |

Run `./deploy.sh --help` for the full list. The rest of this document explains
what the script does, for manual setups or customization.

---

## 1. Provision the server

Point your domain's `A`/`AAAA` DNS records at the VPS before starting (Caddy
needs them resolving to issue a certificate; if you use Cloudflare, see
[§9](#9-cloudflare-ddos-protection)).

SSH in as a sudo-capable user and update the base system:

```sh
sudo apt update && sudo apt upgrade -y
sudo apt install -y git ufw curl gnupg ca-certificates
```

Configure a basic firewall (allow SSH + web; the dcrd P2P port is outbound-only,
so it needs no inbound rule):

```sh
sudo ufw allow OpenSSH
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw enable
```

---

## 2. Install Go

dcrdata targets the Go version in `go.mod`. Distro packages are often older, so
install the official toolchain:

```sh
GO_VERSION=1.27.1
ARCH=$(dpkg --print-architecture)   # amd64 or arm64
case "$ARCH" in   # sha256 from https://go.dev/dl/; deploy.sh pins the same sums
  amd64) GO_SHA256=63d339f0da5ab53635a56f2490a7984dfe12dfcff22ad749f63edaf590168445 ;;
  arm64) GO_SHA256=3450b45a3f9ee8568792736a5c5e70a1f2e9b36c35a8f74958c03e51d7d92bec ;;
esac
curl -fsSL "https://dl.google.com/go/go${GO_VERSION}.linux-${ARCH}.tar.gz" -o /tmp/go.tar.gz
# Chained so a mismatch leaves the installed Go alone.
echo "${GO_SHA256}  /tmp/go.tar.gz" | sha256sum -c - \
  && sudo rm -rf /usr/local/go \
  && sudo tar -C /usr/local -xzf /tmp/go.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin' | sudo tee /etc/profile.d/go.sh
export PATH=$PATH:/usr/local/go/bin
go version
```

> The sums are for 1.27.1. For another version, take the sum from
> [go.dev/dl](https://go.dev/dl/) rather than from the server that sends the
> tarball.

---

## 3. Install and tune PostgreSQL

```sh
sudo apt install -y postgresql
```

Create a role and database. We run dcrdata as an OS user named `dcrdata` and let
it authenticate to PostgreSQL over the local unix socket via **peer auth**, so
there is no database password to manage:

```sh
sudo useradd --system --create-home --home-dir /opt/dcrdata --shell /usr/sbin/nologin dcrdata
sudo -u postgres createuser dcrdata
sudo -u postgres createdb -O dcrdata dcrdata
```

Apply tuning. dcrdata ships suggested settings in
[`db/dcrpg/postgresql-tuning.conf`](db/dcrpg/postgresql-tuning.conf); the values
below are scaled for an 8 GB host. Drop them into the cluster's `conf.d`:

```sh
PG_VER=$(ls /etc/postgresql | sort -V | tail -1)
sudo tee /etc/postgresql/$PG_VER/main/conf.d/dcrdata.conf >/dev/null <<'EOF'
# dcrdata tuning — adjust shared_buffers/effective_cache_size to your RAM.
synchronous_commit = off
max_connections = 32
shared_buffers = 2GB
effective_cache_size = 6GB
maintenance_work_mem = 512MB
work_mem = 28MB
wal_buffers = 16MB
max_wal_size = 2GB
min_wal_size = 1GB
checkpoint_completion_target = 0.9
random_page_cost = 1.1          # SSD/NVMe
effective_io_concurrency = 200  # SSD/NVMe
EOF
sudo systemctl restart postgresql
```

> `synchronous_commit = off` dramatically speeds the initial bulk load. It is
> safe for an explorer (the worst case after a crash is replaying a few recent
> blocks), but if you prefer maximum durability set it back to `on` once synced.

---

## 4. Install and sync dcrd

Build `dcrd` and `dcrctl` straight from source with the Go toolchain you just
installed (architecture-independent, always a compatible release):

```sh
sudo useradd --system --create-home --home-dir /opt/dcrd --shell /usr/sbin/nologin dcrd
sudo GOBIN=/usr/local/bin /usr/local/go/bin/go install github.com/decred/dcrd@latest
sudo GOBIN=/usr/local/bin /usr/local/go/bin/go install decred.org/dcrctl@release-v2.1.6
```

(Both installs run as root: `GOBIN=/usr/local/bin` is not writable by the
`dcrd` user. dcrd's 2.x releases carry v1 module versions (2.1.6 is
`v1.10.8`), so `@latest` is the newest release. `dcrctl` lives in its own module,
`decred.org/dcrctl`, whose `@latest` is still v1.6.2 from 2021; install the
`release-v` tag matching `dcrd --version`, as `deploy.sh` does.)

Configure it with an RPC user/password and `txindex` (required by dcrdata):

```sh
sudo -u dcrd mkdir -p /opt/dcrd/.dcrd
RPCUSER=dcrd
RPCPASS=$(openssl rand -hex 24)
sudo -u dcrd tee /opt/dcrd/.dcrd/dcrd.conf >/dev/null <<EOF
[Application Options]
rpcuser=$RPCUSER
rpcpass=$RPCPASS
rpclisten=127.0.0.1:9109
txindex=1
EOF
```

Run it as a service. dcrd auto-generates `rpc.cert`/`rpc.key` on first start:

```sh
sudo tee /etc/systemd/system/dcrd.service >/dev/null <<'EOF'
[Unit]
Description=dcrd Decred full node
After=network-online.target
Wants=network-online.target

[Service]
User=dcrd
Group=dcrd
ExecStart=/usr/local/bin/dcrd
Restart=on-failure
RestartSec=5
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/opt/dcrd

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable --now dcrd
journalctl -u dcrd -f      # watch it sync; this takes a while
```

dcrdata reads dcrd's RPC certificate. It is a **public** certificate (only
`rpc.key` is secret), so copy it where the `dcrdata` user can read it:

```sh
# Wait for first start to generate it, then:
sudo install -m 644 -o dcrdata -g dcrdata /opt/dcrd/.dcrd/rpc.cert /opt/dcrdata/dcrd-rpc.cert
```

### Connecting to an existing dcrd

If you already run a synced node, skip this section. Make sure it has
`txindex=1` and an RPC user/pass, copy its `rpc.cert` to the dcrdata host, and in
[§6](#6-configure-dcrdata) set `dcrdserv`/`dcrduser`/`dcrdpass`/`dcrdcert` to
match. With the script: `--skip-dcrd --dcrdserv host:9109 --dcrduser u
--dcrdpass p --dcrdcert /path/rpc.cert`.

---

## 5. Build the explorer

Clone (your fork) into the service user's directory and build the binary:

```sh
sudo git clone https://github.com/jzbz/data.dcr.pw /opt/dcrdata/app
# -C instead of cd: /opt/dcrdata may not be readable by your own user.
sudo /usr/local/go/bin/go build -C /opt/dcrdata/app/cmd/dcrdata -o /opt/dcrdata/app/cmd/dcrdata/dcrdata .
sudo chown -R dcrdata:dcrdata /opt/dcrdata
```

The front end is plain CSS + native ES modules served directly from `public/`,
so there is nothing else to build. dcrdata serves its templates (`views_v2/`)
and static assets (`public/`) relative to its working directory, so it must run
from `/opt/dcrdata/app/cmd/dcrdata`.

---

## 6. Configure dcrdata

Write the config into the service user's app-data directory. It wires dcrdata to
dcrd and PostgreSQL and marks it as sitting behind a trusted proxy (Caddy):

```sh
sudo -u dcrdata mkdir -p /opt/dcrdata/appdata
sudo -u dcrdata tee /opt/dcrdata/appdata/dcrdata.conf >/dev/null <<EOF
[Application Options]
# dcrd RPC
dcrduser=dcrd
dcrdpass=PASTE_THE_RPCPASS_FROM_SECTION_4
dcrdserv=127.0.0.1:9109
dcrdcert=/opt/dcrdata/dcrd-rpc.cert

# PostgreSQL (peer auth over the local socket — no password)
pghost=/run/postgresql
pguser=dcrdata
pgdbname=dcrdata

# Web server — listen on loopback; Caddy fronts it
apilisten=127.0.0.1:7777
apiproto=http

# Behind a reverse proxy: trust X-Forwarded-* and pin the public host
userealip=true
trustproxy=true
allowedhost=explorer.example.com
EOF
```

> Replace `allowedhost` with your real domain and paste the dcrd RPC password.
> For **testnet**, add `testnet=1` and use `dcrdserv=127.0.0.1:19109`.

---

## 7. Run dcrdata as a systemd service

```sh
sudo tee /etc/systemd/system/dcrdata.service >/dev/null <<'EOF'
[Unit]
Description=dcrdata Decred block explorer
After=network-online.target postgresql.service dcrd.service
Wants=network-online.target

[Service]
User=dcrdata
Group=dcrdata
WorkingDirectory=/opt/dcrdata/app/cmd/dcrdata
ExecStart=/opt/dcrdata/app/cmd/dcrdata/dcrdata --appdata=/opt/dcrdata/appdata
# always, not on-failure: dcrdata exits 0 when it requests its own shutdown on
# an unrecoverable condition, which on-failure would leave stopped.
Restart=always
RestartSec=10

# Hardening
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/opt/dcrdata

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable --now dcrdata
journalctl -u dcrdata -f      # watch the initial sync
```

The web server comes up immediately and serves a **syncing status page** until
the PostgreSQL index catches up to the chain tip. Be patient on first run.

---

## 8. Reverse proxy + automatic HTTPS with Caddy

Install Caddy from its official apt repository:

```sh
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' \
  | sudo gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' \
  | sudo tee /etc/apt/sources.list.d/caddy-stable.list
sudo apt update && sudo apt install -y caddy
```

Write a Caddyfile that proxies your domain to dcrdata, compresses responses,
caches static assets, and sets sensible security headers. WebSocket upgrades
(used for live updates on `/ws`) are handled automatically:

```sh
sudo tee /etc/caddy/Caddyfile >/dev/null <<'EOF'
explorer.example.com {
	encode zstd gzip
	reverse_proxy 127.0.0.1:7777 {
		# dcrdata runs with userealip=true and trusts True-Client-IP and
		# X-Real-IP over X-Forwarded-For; Caddy only manages X-Forwarded-*,
		# so strip/pin the others or clients can spoof their address.
		header_up -True-Client-IP
		header_up X-Real-IP {remote_host}
	}

	# Cache fingerprinted static assets aggressively.
	@assets path /css/* /js/* /fonts/* /images/*
	header @assets Cache-Control "public, max-age=604800"

	header {
		Referrer-Policy "same-origin"
		X-Content-Type-Options "nosniff"
		X-Frame-Options "SAMEORIGIN"
		-Server
	}
}
EOF

sudo caddy validate --config /etc/caddy/Caddyfile
sudo systemctl reload caddy
journalctl -u caddy -f      # watch certificate issuance
```

> Caddyfiles use **tabs** for indentation; the heredoc preserves them. Replace
> `explorer.example.com` with your domain (in two places — the Caddyfile and
> `allowedhost` in §6).

Within a few seconds Caddy provisions a Let's Encrypt certificate and your site
is live at `https://explorer.example.com`, HTTP redirecting to HTTPS, renewals
handled in the background.

> **Quick test without a domain:** replace the first line with `:80` (plain HTTP)
> and browse to the server's IP.

---

## 9. Cloudflare (DDoS protection)

To sit dcrdata behind Cloudflare in production:

1. **DNS** — in the Cloudflare dashboard add an `A`/`AAAA` record for your domain
   pointing at the VPS, **proxied** (orange cloud). This hides the origin IP and
   absorbs volumetric attacks.

2. **First certificate** — Caddy needs to reach Let's Encrypt to issue its cert.
   Either:
   - set the record **DNS-only** (grey cloud) for a minute so Caddy issues the
     cert, then flip it back to **proxied**; or
   - install a **Cloudflare Origin Certificate** and point Caddy at it with a
     `tls /path/cert.pem /path/key.pem` directive (no public ACME needed).

3. **SSL/TLS mode** — set Cloudflare to **Full (strict)** so it validates the
   origin's certificate end-to-end.

4. **Real client IP** — behind Cloudflare the connecting address Caddy sees is
   Cloudflare's. Forward the true client IP to dcrdata (which has
   `userealip=true`) by adding inside the `reverse_proxy` block:

   ```caddyfile
   reverse_proxy 127.0.0.1:7777 {
   	header_up X-Real-IP {http.request.header.Cf-Connecting-Ip}
   }
   ```

5. **Hardening** — optionally enable Cloudflare rate limiting / WAF rules, and
   restrict your origin firewall (ufw) to allow `80`/`443` only from
   [Cloudflare's IP ranges](https://www.cloudflare.com/ips/) so attackers can't
   bypass the proxy by hitting the origin directly.

---

## 10. Updating to a new version

```sh
# Built as the service user, which owns the checkout, into a new file that is
# swapped in only if the build succeeds. Chained so a failed step leaves the
# running binary alone.
sudo -u dcrdata git -C /opt/dcrdata/app pull \
  && sudo -u dcrdata env GOTOOLCHAIN=local /usr/local/go/bin/go build -C /opt/dcrdata/app/cmd/dcrdata -o /opt/dcrdata/app/cmd/dcrdata/dcrdata.new . \
  && sudo -u dcrdata mv -f /opt/dcrdata/app/cmd/dcrdata/dcrdata.new /opt/dcrdata/app/cmd/dcrdata/dcrdata \
  && sudo systemctl restart dcrdata
```

Or just re-run `deploy.sh`, which does all of this idempotently and only swaps in
the new binary if the build succeeds. It also rebuilds dcrd at `latest` (or the
`--dcrd-version` given), restarting it only if the version changed, and upgrades
Caddy from its apt repository. Re-runs read the original deployment
choices (network, dcrd topology, domain, repo) from `/etc/default/dcrdata-deploy`,
so a bare `sudo ./deploy.sh` upgrades in place; passing a conflicting network or
topology flag aborts with an explanation rather than desyncing the configs from
the data. A hand-customized `/etc/caddy/Caddyfile` (e.g. the Cloudflare edits in
§9) is detected and left untouched.

---

## Troubleshooting

| Symptom | Check |
| --- | --- |
| `502 Bad Gateway` | Is dcrdata up? `systemctl status dcrdata`. Listening on `127.0.0.1:7777`? `ss -ltnp \| grep 7777`. |
| Stuck on the "syncing" page | Normal on first run — initial PostgreSQL indexing is slow. Watch `journalctl -u dcrdata -f`. |
| dcrdata exits: can't reach dcrd | Is dcrd synced and its RPC up? `dcrctl --rpcserver=127.0.0.1:9109 --rpccert=/opt/dcrd/.dcrd/rpc.cert --rpcuser=… --rpcpass=… getinfo`. Cert path/permissions correct? |
| dcrdata exits: dcrd version incompatible | Update dcrd: re-run `deploy.sh`, or `sudo GOBIN=/usr/local/bin /usr/local/go/bin/go install github.com/decred/dcrd@latest` and `sudo systemctl restart dcrd`. dcrdata checks the RPC API version on startup and accepts 7.x and 8.x. |
| `password authentication failed` (PG) | Peer auth needs the OS user and DB role to match (`dcrdata`). Run dcrdata as the `dcrdata` user and connect via `pghost=/run/postgresql`. |
| dcrdata crash-loops: `The stake database is corrupted` | The stake database fell behind the PostgreSQL index and dcrdata cannot resync past it. Re-run `deploy.sh`: it detects this and purges the few blocks the panic asks for. See below. |
| Disk filling up | The mainnet PostgreSQL DB is large and grows. Monitor with `df -h` and `du -sh /var/lib/postgresql`. |
| TLS certificate not issued | DNS must resolve to this host and ports 80+443 reachable. Behind Cloudflare, see §9 step 2. Watch `journalctl -u caddy -f`. |
| Wrong client IPs in logs / rate limits | Set `userealip`/`trustproxy` (§6) and, behind Cloudflare, forward `CF-Connecting-IP` (§9 step 4). |

### Stake database desync

dcrdata keeps a stake database alongside the PostgreSQL index — an ffldb stake
node store (`stakenodes`) plus a badger ticket pool (`ticket_pool.bdgr`) — and
the sync loop can only connect the block exactly one above it. Two things can
leave it further behind than that: an unclean stop (the stake node store's
metadata is cached and flushed lazily, while PostgreSQL has already committed),
and a stake-database handler error during a new block, which the notifier logs
as `block handler failed` and then swallows while the PostgreSQL write proceeds
anyway. Either way dcrdata will not put it back on its own — its rewind only
fires when the stake database is *ahead*. The result is a panic on the same
block at every start:

```
panic: About to connect the wrong block: 1109523, 1109521
        The stake database is corrupted. Restart with --purge-n-blocks=4 to recover.
```

Re-running `deploy.sh` fixes it. It looks for that panic in the journal, confirms
PostgreSQL is still sitting at the height the panic named, then stops the
service and runs a one-shot purge — which drops PostgreSQL below the stake
database, so dcrdata rewinds the stake database to match and re-indexes the
purged blocks. The flag is passed on the command line of that single run only,
never written to `dcrdata.conf`, so it cannot purge again on a later start.

To do it by hand instead, or with `--no-repair` set:

```bash
systemctl stop dcrdata
systemctl reset-failed dcrdata
cd /opt/dcrdata/app/cmd/dcrdata
sudo -u dcrdata ./dcrdata --appdata=/opt/dcrdata/appdata --purge-n-blocks=4 --sync-and-quit
systemctl start dcrdata
```

`reset-failed` matters if the unit has hit a start limit: `systemctl stop` does
not clear that state, and `start` is then refused with "Start request repeated
too quickly." You can interrupt the run with Ctrl-C once the purge has logged
`Successfully purged data for …` — the rest is an ordinary catch-up sync that the
service finishes on its own while serving its syncing page.

Use the `N` from the panic message. `Rewinding StakeDatabase from block X to Y.`
in the output is the line that confirms the repair took. If it recurs
immediately with a larger `N`, the stake database itself is damaged. Deleting it
is the last resort, and note what that actually costs: nothing rebuilds the stake
database against an existing PostgreSQL index. With the stake database at height
0 and PostgreSQL at the chain tip, the very next start panics again with a gap of
a million blocks. So the last resort is a **full resync**, both stores together:

```bash
systemctl stop dcrdata
rm -rf /opt/dcrdata/appdata/data/mainnet/stakenodes /opt/dcrdata/appdata/data/mainnet/ticket_pool.bdgr
sudo -u dcrdata psql -d dcrdata -c 'DROP OWNED BY dcrdata;'
systemctl start dcrdata
```

That re-indexes from genesis and takes many hours. Exhaust the purge route first.

Pick `N` generously rather than minimally. `PurgeBestBlocks` counts back from the
`blocks` table tip and then rewinds the stake database to whatever height it
lands on, returning an error — which aborts startup entirely — if that height is
still above the stake database. Too small an `N` therefore fails *worse* than the
crash loop. `deploy.sh` guards against this by raising `N` to the real distance
between the blocks tip and the stake database when the panic's own number is too
small; doing it by hand, add a few blocks of margin.
