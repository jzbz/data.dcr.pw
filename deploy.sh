#!/usr/bin/env bash
#
# deploy.sh — provision dcrdata on a fresh Debian 13 / Ubuntu 24.04 LTS VPS
#             (amd64 or arm64).
#
# Installs Go, PostgreSQL, dcrd, and Caddy (automatic HTTPS); creates the
# database; builds the explorer; and runs it all as hardened systemd services
# behind Caddy. The front end is plain CSS + native ES modules (no Node.js).
# Safe to re-run: every step is idempotent, so this doubles as an updater.
# The first run records its network/topology choices in
# /etc/default/dcrdata-deploy; re-runs inherit them, so a bare
# `sudo ./deploy.sh` upgrades in place. Changing network or dcrd topology
# against the recorded state aborts with an explanation (delete the state
# file after wiping the data to start over).
#
# A re-run also repairs the one failure dcrdata cannot recover from on its own:
# if an unclean stop leaves the stake database behind the PostgreSQL index,
# every start panics on the same block and systemd restarts into it forever.
# deploy.sh detects exactly that and performs the one-shot purge the panic
# asks for. Pass --no-repair to leave it alone.
#
# Install or upgrade data.dcr.pw (re-runs inherit the recorded options). The
# script is downloaded whole before it runs, so a dropped connection cannot
# execute half of it:
#   f=$(mktemp) && curl -fsSL https://raw.githubusercontent.com/jzbz/data.dcr.pw/master/deploy.sh -o "$f" && sudo bash "$f" --domain data.dcr.pw; rm -f "$f"
#
# Usage (run as root or with sudo):
#   curl -fsSL https://raw.githubusercontent.com/jzbz/data.dcr.pw/master/deploy.sh \
#     | sudo bash -s -- --domain explorer.example.com
#   sudo ./deploy.sh --domain explorer.example.com
#   sudo ./deploy.sh --domain explorer.example.com --repo https://github.com/me/dcrdata
#   sudo ./deploy.sh --http                       # no domain: serve plain HTTP on :80
#   sudo ./deploy.sh --domain x --skip-dcrd \      # use an existing dcrd node
#        --dcrdserv 10.0.0.5:9109 --dcrduser u --dcrdpass p --dcrdcert /path/rpc.cert
#   sudo ./deploy.sh                               # re-run: upgrade with recorded options
#
# Options:
#   --domain <host>    Domain to serve (Caddy provisions a TLS cert for it).
#   --http             Serve plain HTTP on :80 instead of HTTPS (for testing).
#   --repo <url>       Git repo to deploy        (default: jzbz/data.dcr.pw; use a fork to override).
#   --testnet          Index testnet instead of mainnet.
#   --skip-dcrd        Do not install dcrd; connect to an existing node.
#   --dcrdserv <addr>  Existing dcrd RPC host:port (with --skip-dcrd).
#   --dcrduser <user>  Existing dcrd RPC username  (with --skip-dcrd).
#   --dcrdpass <pass>  Existing dcrd RPC password  (with --skip-dcrd).
#   --dcrdcert <path>  Existing dcrd rpc.cert file (with --skip-dcrd).
#   --go-version <v>   Go toolchain version        (default: 1.27.1).
#   --dcrd-version <v> dcrd module version to go install (default: latest;
#                      dcrd 2.1.6 is module v1.10.8, or name its tag,
#                      release-v2.1.6).
#   --listen <addr>    dcrdata internal listen     (default: 127.0.0.1:7777).
#   --no-repair        Do not repair a stake-database desync; only report it.
#   -h, --help         Show this help.
#
set -euo pipefail

# ---- Configuration --------------------------------------------------------

GO_VERSION="1.27.1"
# sha256 of the official go${GO_VERSION} linux tarballs (https://go.dev/dl/).
# Bump together with GO_VERSION. A --go-version override is checked against
# the .sha256 file published beside its tarball instead.
GO_PINNED_VERSION="${GO_VERSION}"
GO_SHA256_AMD64="63d339f0da5ab53635a56f2490a7984dfe12dfcff22ad749f63edaf590168445"
GO_SHA256_ARM64="3450b45a3f9ee8568792736a5c5e70a1f2e9b36c35a8f74958c03e51d7d92bec"
DCRD_VERSION="latest"
REPO_URL="https://github.com/jzbz/data.dcr.pw"
# Where this script is served from, quoted in usage when it was piped to bash
# and so has no readable source of its own to print.
SELF_URL="https://raw.githubusercontent.com/jzbz/data.dcr.pw/master/deploy.sh"
# Earlier URLs of REPO_URL. GitHub redirects them after the rename; deployments
# that recorded one, or still fetch from one, are moved to REPO_URL.
RENAMED_REPO_URLS=(
  "https://github.com/jzbz/dcrdata-remix"
  "https://github.com/jzbz/dcrdata-remix.git"
)
LISTEN="127.0.0.1:7777"
DOMAIN=""
HTTP_ONLY=0
TESTNET=0
SKIP_DCRD=0
REPAIR=1
EXT_DCRDSERV=""; EXT_DCRDUSER=""; EXT_DCRDPASS=""; EXT_DCRDCERT=""
REPO_SET=0; LISTEN_SET=0; MODE_SET=0

# Options recorded by the previous run; re-runs inherit them and refuse
# incompatible changes (see the state checks after argument parsing).
STATE_FILE=/etc/default/dcrdata-deploy

DATA_USER="dcrdata"
DATA_HOME="/opt/dcrdata"
APP_DIR="${DATA_HOME}/app"
APP_CMD="${APP_DIR}/cmd/dcrdata"
APPDATA="${DATA_HOME}/appdata"
DCRD_CERT_DST="${DATA_HOME}/dcrd-rpc.cert"

NODE_USER="dcrd"
NODE_HOME="/opt/dcrd"
NODE_DCRD_DIR="${NODE_HOME}/.dcrd"

GO=/usr/local/go/bin/go

# ---- Logging --------------------------------------------------------------

if [[ -t 1 ]]; then
  C_BLUE=$'\e[34m'; C_GREEN=$'\e[32m'; C_RED=$'\e[31m'; C_DIM=$'\e[2m'; C_OFF=$'\e[0m'
else
  C_BLUE=""; C_GREEN=""; C_RED=""; C_DIM=""; C_OFF=""
fi
log()  { printf '%s==>%s %s\n' "$C_BLUE" "$C_OFF" "$*"; }
ok()   { printf '%s  ✓%s %s\n' "$C_GREEN" "$C_OFF" "$*"; }
warn() { printf '%s  !%s %s\n' "$C_RED" "$C_OFF" "$*" >&2; }
die()  { printf '%serror:%s %s\n' "$C_RED" "$C_OFF" "$*" >&2; exit 1; }

# True when $1 is one of the repository's earlier URLs.
renamed_repo_url() {
  local u
  for u in "${RENAMED_REPO_URLS[@]}"; do
    [[ "$1" == "$u" ]] && return 0
  done
  return 1
}

# Print the header comment block (everything after the shebang up to the first
# non-comment line) as usage text. Piped to bash — curl ... | sudo bash — there
# is no such block to read: $0 is "bash", not a file. Fall back to a short form
# rather than failing, since that is exactly when a user has mistyped a flag and
# needs to be told what the flags are.
usage() {
  local line
  # "$0" is a bare "bash" under a pipe, which [[ -r ]] would resolve against the
  # current directory — so require a shebang too, or a stray ./bash would be
  # printed as this script's help.
  if [[ -r "$0" ]] && [[ "$(head -c2 "$0" 2>/dev/null)" == '#!' ]]; then
    while IFS= read -r line; do
      case "$line" in
        '#!'*) continue ;;
        '# '*) printf '%s\n' "${line#'# '}" ;;
        '#')   printf '\n' ;;
        '#'*)  printf '%s\n' "${line#'#'}" ;;
        *)     break ;;
      esac
    done < "$0"
  else
    cat <<EOF
deploy.sh — provision dcrdata on a fresh Debian 13 / Ubuntu 24.04 LTS VPS.

Usage:
  curl -fsSL ${SELF_URL} \\
    | sudo bash -s -- --domain explorer.example.com   # HTTPS via Caddy
  curl -fsSL ${SELF_URL} \\
    | sudo bash -s -- --http                          # plain HTTP on :80

Note the "-s --" : without it bash treats the flags as its own.

Other options: --testnet, --skip-dcrd (with --dcrdserv/-user/-pass/-cert),
--repo, --go-version, --dcrd-version, --listen, --no-repair.

Full documentation: ${REPO_URL}/blob/master/DEPLOY.md
For the complete option list, download the script and run: ./deploy.sh --help
EOF
  fi
  exit "${1:-0}"
}

# ---- Argument parsing -----------------------------------------------------

while [[ $# -gt 0 ]]; do
  case "$1" in
    --domain)       DOMAIN="${2:?--domain needs a value}"; MODE_SET=1; shift 2 ;;
    --http)         HTTP_ONLY=1; MODE_SET=1; shift ;;
    --repo)         REPO_URL="${2:?--repo needs a value}"; REPO_SET=1; shift 2 ;;
    --testnet)      TESTNET=1; shift ;;
    --skip-dcrd)    SKIP_DCRD=1; shift ;;
    --no-repair)    REPAIR=0; shift ;;
    --dcrdserv)     EXT_DCRDSERV="${2:?}"; shift 2 ;;
    --dcrduser)     EXT_DCRDUSER="${2:?}"; shift 2 ;;
    --dcrdpass)     EXT_DCRDPASS="${2:?}"; shift 2 ;;
    --dcrdcert)     EXT_DCRDCERT="${2:?}"; shift 2 ;;
    --go-version)   GO_VERSION="${2:?}"; shift 2 ;;
    --dcrd-version) DCRD_VERSION="${2:?}"; shift 2 ;;
    --listen)       LISTEN="${2:?}"; LISTEN_SET=1; shift 2 ;;
    -h|--help)      usage 0 ;;
    *)              warn "unknown option: $1"; usage 1 ;;
  esac
done

# $0 is "bash" when piped, so "sudo $0" would not be a runnable command; give
# the piped form instead of advice that cannot be followed.
if [[ $EUID -ne 0 ]]; then
  if [[ -r "$0" ]] && [[ "$(head -c2 "$0" 2>/dev/null)" == '#!' ]]; then
    die "must run as root (try: sudo $0 ...)"
  else
    die "must run as root. Pipe to 'sudo bash', keeping the -s -- :
       curl -fsSL ${SELF_URL} | sudo bash -s -- --domain explorer.example.com"
  fi
fi

# ---- Recorded deployment state --------------------------------------------
# The config files are regenerated from this invocation's flags on every run,
# while the database and dcrd data dir carry the *previous* run's choices.
# Inherit unspecified options from the recorded state and refuse changes that
# would desync the configs from the data (mainnet configs over a testnet
# database, a fresh local dcrd over an external-node install, ...).

PREV_NETWORK=""; PREV_SKIP_DCRD=""; PREV_DOMAIN=""; PREV_HTTP_ONLY=""
PREV_REPO_URL=""; PREV_LISTEN=""; PREV_DCRDSERV=""; PREV_DCRDUSER=""
if [[ -f "$STATE_FILE" ]]; then
  PREV_NETWORK=$(sed -n 's/^NETWORK=//p' "$STATE_FILE")
  PREV_SKIP_DCRD=$(sed -n 's/^SKIP_DCRD=//p' "$STATE_FILE")
  PREV_DOMAIN=$(sed -n 's/^DOMAIN=//p' "$STATE_FILE")
  PREV_HTTP_ONLY=$(sed -n 's/^HTTP_ONLY=//p' "$STATE_FILE")
  PREV_REPO_URL=$(sed -n 's/^REPO_URL=//p' "$STATE_FILE")
  PREV_LISTEN=$(sed -n 's/^LISTEN=//p' "$STATE_FILE")
  PREV_DCRDSERV=$(sed -n 's/^EXT_DCRDSERV=//p' "$STATE_FILE")
  PREV_DCRDUSER=$(sed -n 's/^EXT_DCRDUSER=//p' "$STATE_FILE")

  # Network: absence of --testnet is ambiguous (it is also the mainnet
  # default), so inherit the recorded network; an explicit --testnet against
  # a mainnet install is a real conflict.
  if [[ $TESTNET -eq 0 && "$PREV_NETWORK" == "testnet" ]]; then
    TESTNET=1
    log "Inheriting testnet from the previous deploy (${STATE_FILE})"
  elif [[ $TESTNET -eq 1 && "$PREV_NETWORK" == "mainnet" ]]; then
    die "this host is deployed for mainnet; --testnet would point the configs at a
       database and dcrd data dir full of mainnet data. Wipe them and delete
       ${STATE_FILE} to redeploy on testnet."
  fi

  # dcrd topology: same rules.
  if [[ $SKIP_DCRD -eq 0 && "$PREV_SKIP_DCRD" == "1" ]]; then
    SKIP_DCRD=1
    log "Inheriting --skip-dcrd (external node) from the previous deploy"
  elif [[ $SKIP_DCRD -eq 1 && "$PREV_SKIP_DCRD" == "0" ]]; then
    die "this host runs its own dcrd; switching to --skip-dcrd would leave it
       running and orphaned. Stop/disable the dcrd service and delete
       ${STATE_FILE} first."
  fi

  # Serving mode and repo: plain preferences, inherit when not specified.
  if [[ $MODE_SET -eq 0 ]]; then
    DOMAIN="$PREV_DOMAIN"
    [[ "$PREV_HTTP_ONLY" == "1" ]] && HTTP_ONLY=1
  fi
  # A recorded earlier name of this repository moves to the current REPO_URL.
  if [[ $REPO_SET -eq 0 && -n "$PREV_REPO_URL" ]] && ! renamed_repo_url "$PREV_REPO_URL"; then
    REPO_URL="$PREV_REPO_URL"
  fi
  [[ $LISTEN_SET -eq 0 && -n "$PREV_LISTEN" ]] && LISTEN="$PREV_LISTEN"
fi

if [[ $HTTP_ONLY -eq 0 && -z "$DOMAIN" ]]; then
  die "provide --domain <host>, or --http to serve plain HTTP for testing"
fi
if [[ $SKIP_DCRD -eq 1 && -z "$PREV_DCRDSERV" ]]; then
  # First skip-dcrd run: all four connection parameters are required. Re-runs
  # inherit the server/user from the state file and the password/cert from the
  # previous run's dcrdata.conf and cert copy.
  [[ -n "$EXT_DCRDSERV" && -n "$EXT_DCRDUSER" && -n "$EXT_DCRDPASS" && -n "$EXT_DCRDCERT" ]] \
    || die "--skip-dcrd requires --dcrdserv, --dcrduser, --dcrdpass and --dcrdcert"
fi
if [[ -n "$EXT_DCRDCERT" ]]; then
  [[ -f "$EXT_DCRDCERT" ]] || die "dcrd cert not found: $EXT_DCRDCERT"
fi

NETWORK="mainnet"; RPC_PORT="9109"
if [[ $TESTNET -eq 1 ]]; then NETWORK="testnet"; RPC_PORT="19109"; fi

# ---- 1. Base packages -----------------------------------------------------

log "Installing base packages"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq git ufw curl gnupg ca-certificates openssl >/dev/null
ok "base packages installed"

# ---- 2. Firewall ----------------------------------------------------------

log "Configuring firewall (ufw)"
# Never enable a default-deny firewall without a working SSH rule: the OpenSSH
# app profile may be missing, and sshd may listen on a nonstandard port. Find
# the ports sshd actually listens on (fall back to sshd_config, then 22) and
# abort loudly if a rule cannot be added — a silent failure here locks the
# operator out of the VPS.
SSH_PORTS=$(ss -tlnpH 2>/dev/null | awk '/"sshd"/ {sub(/.*:/, "", $4); print $4}' | sort -un)
if [[ -z "$SSH_PORTS" ]]; then
  SSH_PORTS=$(sed -n 's/^[[:space:]]*[Pp]ort[[:space:]]\+//p' /etc/ssh/sshd_config 2>/dev/null | awk '{print $1}' | sort -un)
fi
[[ -z "$SSH_PORTS" ]] && SSH_PORTS="22"
for p in $SSH_PORTS; do
  ufw allow "${p}/tcp" >/dev/null || die "failed to allow SSH port ${p}/tcp in ufw; not enabling the firewall"
done
ufw allow 80/tcp  >/dev/null
ufw allow 443/tcp >/dev/null
ufw --force enable >/dev/null
ok "SSH ($(echo "$SSH_PORTS" | tr '\n' ' ' | sed 's/ $//')), 80/tcp and 443/tcp allowed"

# ---- 3. Go toolchain ------------------------------------------------------

case "$(uname -m)" in
  x86_64|amd64)  GO_ARCH="amd64"; GO_SHA256="$GO_SHA256_AMD64" ;;
  aarch64|arm64) GO_ARCH="arm64"; GO_SHA256="$GO_SHA256_ARM64" ;;
  *)             die "unsupported CPU architecture: $(uname -m)" ;;
esac

if $GO version 2>/dev/null | grep -q "go${GO_VERSION} "; then
  ok "Go ${GO_VERSION} already installed"
else
  log "Installing Go ${GO_VERSION} (${GO_ARCH})"
  tmp="$(mktemp -d)"
  # dl.google.com, not go.dev/dl: the latter is a redirector that answers the
  # .sha256 sidecar with an HTML page, which awk below would happily reduce to
  # "<!DOCTYPE" and sha256sum would then reject as malformed — failing the
  # deploy on a checksum that was never fetched. dl.google.com serves both the
  # tarball and the bare hash, and is where go.dev/dl redirects anyway.
  curl -fsSL "https://dl.google.com/go/go${GO_VERSION}.linux-${GO_ARCH}.tar.gz" -o "${tmp}/go.tar.gz"
  # Verify before the installed toolchain is removed: this tarball is extracted
  # as root and its toolchain builds everything else in the deploy. The pinned
  # sum does not come from the server that sent the tarball; the sidecar used
  # for an override does, so it catches a corrupt download but not a
  # tampered one.
  if [[ "$GO_VERSION" != "$GO_PINNED_VERSION" ]]; then
    warn "No pinned sha256 for Go ${GO_VERSION}; checking the one published beside it"
    GO_SHA256=$(curl -fsSL "https://dl.google.com/go/go${GO_VERSION}.linux-${GO_ARCH}.tar.gz.sha256" | awk '{print $1}') \
      || { rm -rf "$tmp"; die "could not fetch the published sha256 for Go ${GO_VERSION}"; }
  fi
  echo "${GO_SHA256}  ${tmp}/go.tar.gz" | sha256sum -c --quiet - \
    || { rm -rf "$tmp"; die "Go tarball sha256 mismatch (go${GO_VERSION}.linux-${GO_ARCH}.tar.gz)"; }
  rm -rf /usr/local/go
  tar -C /usr/local -xzf "${tmp}/go.tar.gz"
  rm -rf "$tmp"
  # shellcheck disable=SC2016
  echo 'export PATH=$PATH:/usr/local/go/bin' > /etc/profile.d/go.sh
  ok "Go installed: $($GO version)"
fi

# ---- 4. PostgreSQL --------------------------------------------------------

if command -v psql >/dev/null 2>&1; then
  ok "PostgreSQL already installed"
else
  log "Installing PostgreSQL"
  apt-get install -y -qq postgresql >/dev/null
  ok "PostgreSQL installed"
fi

# Service user must exist before the DB role (peer auth maps OS user -> role).
if ! id "$DATA_USER" >/dev/null 2>&1; then
  log "Creating service user '${DATA_USER}'"
  useradd --system --create-home --home-dir "$DATA_HOME" --shell /usr/sbin/nologin "$DATA_USER"
  ok "created ${DATA_USER}"
fi

log "Ensuring PostgreSQL role and database"
if ! sudo -u postgres psql -tAc "SELECT 1 FROM pg_roles WHERE rolname='${DATA_USER}'" | grep -q 1; then
  sudo -u postgres createuser "$DATA_USER"
fi
if ! sudo -u postgres psql -tAc "SELECT 1 FROM pg_database WHERE datname='${DATA_USER}'" | grep -q 1; then
  sudo -u postgres createdb -O "$DATA_USER" "$DATA_USER"
fi
ok "role/database '${DATA_USER}' ready"

log "Applying PostgreSQL tuning (scaled to RAM)"
RAM_MB=$(awk '/MemTotal/ {print int($2/1024)}' /proc/meminfo)
SHARED=$(( RAM_MB / 4 )); CACHE=$(( RAM_MB * 3 / 4 )); MAINT=$(( RAM_MB / 16 ))
(( MAINT > 1024 )) && MAINT=1024
# Target the running cluster, not just the newest version directory (they can
# differ after a distro major-version upgrade leaves both installed).
PG_VER=""
if command -v pg_lsclusters >/dev/null 2>&1; then
  PG_VER=$(pg_lsclusters --no-header 2>/dev/null | awk '$4 ~ /online/ {print $1; exit}')
fi
if [[ -z "$PG_VER" ]]; then
  # shellcheck disable=SC2012  # version dirs are numeric (e.g. 16); ls + version-sort is fine
  PG_VER=$(ls /etc/postgresql | sort -V | tail -1)
fi
PG_CONF="/etc/postgresql/${PG_VER}/main/conf.d/dcrdata.conf"
PG_CONF_NEW="$(mktemp)"
cat > "$PG_CONF_NEW" <<EOF
# dcrdata tuning (generated by deploy.sh for ${RAM_MB} MB RAM).
synchronous_commit = off
max_connections = 32
shared_buffers = ${SHARED}MB
effective_cache_size = ${CACHE}MB
maintenance_work_mem = ${MAINT}MB
work_mem = 28MB
wal_buffers = 16MB
max_wal_size = 2GB
min_wal_size = 1GB
checkpoint_completion_target = 0.9
random_page_cost = 1.1
effective_io_concurrency = 200
EOF
# Only install and restart when the tuning actually changed: a needless
# restart on every re-run drops dcrdata's connections mid-sync.
if [[ -f "$PG_CONF" ]] && cmp -s "$PG_CONF_NEW" "$PG_CONF"; then
  rm -f "$PG_CONF_NEW"
  ok "PostgreSQL tuning unchanged (cluster ${PG_VER})"
else
  install -m 644 "$PG_CONF_NEW" "$PG_CONF"
  rm -f "$PG_CONF_NEW"
  systemctl restart postgresql
  ok "PostgreSQL tuned (shared_buffers=${SHARED}MB, effective_cache_size=${CACHE}MB)"
fi

# ---- 5. dcrd --------------------------------------------------------------

if [[ $SKIP_DCRD -eq 1 ]]; then
  # Explicit flags win; a bare re-run inherits the server/user recorded in the
  # state file, the password from the previous run's dcrdata.conf, and the
  # already-copied cert.
  DCRD_SERV="${EXT_DCRDSERV:-$PREV_DCRDSERV}"
  DCRD_RPCUSER="${EXT_DCRDUSER:-$PREV_DCRDUSER}"
  DCRD_RPCPASS="$EXT_DCRDPASS"
  if [[ -z "$DCRD_RPCPASS" && -f "${APPDATA}/dcrdata.conf" ]]; then
    DCRD_RPCPASS=$(sed -n 's/^dcrdpass=//p' "${APPDATA}/dcrdata.conf" | head -1)
  fi
  [[ -n "$DCRD_SERV" && -n "$DCRD_RPCUSER" && -n "$DCRD_RPCPASS" ]] \
    || die "external dcrd parameters incomplete; pass --dcrdserv/--dcrduser/--dcrdpass"
  log "Using existing dcrd at ${DCRD_SERV}"
  if [[ -n "$EXT_DCRDCERT" ]]; then
    install -m 644 -o "$DATA_USER" -g "$DATA_USER" "$EXT_DCRDCERT" "$DCRD_CERT_DST"
    ok "external dcrd cert copied to ${DCRD_CERT_DST}"
  else
    [[ -f "$DCRD_CERT_DST" ]] || die "no dcrd cert at ${DCRD_CERT_DST}; pass --dcrdcert"
    ok "reusing external dcrd cert at ${DCRD_CERT_DST}"
  fi
else
  if ! id "$NODE_USER" >/dev/null 2>&1; then
    log "Creating service user '${NODE_USER}'"
    useradd --system --create-home --home-dir "$NODE_HOME" --shell /usr/sbin/nologin "$NODE_USER"
  fi

  log "Installing dcrd (${DCRD_VERSION})"
  DCRD_VER_BEFORE=$(/usr/local/bin/dcrd --version 2>/dev/null | head -1 || true)
  GOBIN=/usr/local/bin GOTOOLCHAIN=local "$GO" install "github.com/decred/dcrd@${DCRD_VERSION}"
  DCRD_VER_AFTER=$(/usr/local/bin/dcrd --version 2>/dev/null | head -1 || true)
  ok "dcrd installed (${DCRD_VER_AFTER:-$DCRD_VERSION})"

  # dcrctl is an optional admin CLI in its own module, decred.org/dcrctl. Its
  # newest semver tag is v1.6.2 from 2021, so @latest installs that; the
  # repository instead tags each release in step with dcrd (release-v2.1.6),
  # so install the tag matching the dcrd just built. Best-effort: a hiccup
  # fetching it must not abort the deploy, since dcrd and dcrdata don't need it
  # to run.
  DCRD_SEMVER=$(sed -n 's/^dcrd version \([0-9]\{1,\}\.[0-9]\{1,\}\.[0-9]\{1,\}\).*/\1/p' <<<"$DCRD_VER_AFTER")
  DCRCTL_REF="release-v${DCRD_SEMVER}"
  [[ -n "$DCRD_SEMVER" ]] || DCRCTL_REF="latest"
  log "Installing dcrctl ${DCRCTL_REF} (optional admin CLI)"
  if GOBIN=/usr/local/bin GOTOOLCHAIN=local "$GO" install "decred.org/dcrctl@${DCRCTL_REF}"; then
    ok "dcrctl installed ($(/usr/local/bin/dcrctl --version 2>/dev/null | head -1 || true))"
  else
    warn "dcrctl install failed (optional); continuing without it"
  fi

  # Reuse the existing RPC password on re-runs so dcrdata's config stays valid.
  # (sed, not awk -F=: the password itself may contain '=' characters.)
  install -d -o "$NODE_USER" -g "$NODE_USER" "$NODE_DCRD_DIR"
  if [[ -f "${NODE_DCRD_DIR}/dcrd.conf" ]]; then
    DCRD_RPCPASS=$(sed -n 's/^rpcpass=//p' "${NODE_DCRD_DIR}/dcrd.conf" | head -1)
  fi
  DCRD_RPCUSER="dcrd"
  DCRD_RPCPASS="${DCRD_RPCPASS:-$(openssl rand -hex 24)}"
  DCRD_SERV="127.0.0.1:${RPC_PORT}"

  DCRD_CONF_NEW="$(mktemp)"
  cat > "$DCRD_CONF_NEW" <<EOF
[Application Options]
rpcuser=${DCRD_RPCUSER}
rpcpass=${DCRD_RPCPASS}
rpclisten=127.0.0.1:${RPC_PORT}
txindex=1
$( [[ $TESTNET -eq 1 ]] && echo "testnet=1" )
EOF
  DCRD_NEEDS_RESTART=0
  if [[ ! -f "${NODE_DCRD_DIR}/dcrd.conf" ]] || ! cmp -s "$DCRD_CONF_NEW" "${NODE_DCRD_DIR}/dcrd.conf"; then
    DCRD_NEEDS_RESTART=1
  fi
  install -m 600 -o "$NODE_USER" -g "$NODE_USER" "$DCRD_CONF_NEW" "${NODE_DCRD_DIR}/dcrd.conf"
  rm -f "$DCRD_CONF_NEW"
  chown -R "$NODE_USER:$NODE_USER" "$NODE_DCRD_DIR"

  cat > /etc/systemd/system/dcrd.service <<EOF
[Unit]
Description=dcrd Decred full node
After=network-online.target
Wants=network-online.target

[Service]
User=${NODE_USER}
Group=${NODE_USER}
ExecStart=/usr/local/bin/dcrd
Restart=on-failure
RestartSec=5
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=${NODE_HOME}

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  systemctl enable dcrd >/dev/null 2>&1 || true
  # Restart only when the config or binary actually changed — a node mid-sync
  # should not be bounced by a no-op re-run, but stale-config/stale-binary
  # nodes previously kept running the old settings forever, so the "updater"
  # never really updated dcrd.
  if [[ -n "$DCRD_VER_BEFORE" && "$DCRD_VER_BEFORE" != "$DCRD_VER_AFTER" ]]; then
    DCRD_NEEDS_RESTART=1
  fi
  if ! systemctl is-active --quiet dcrd; then
    systemctl start dcrd
  elif [[ $DCRD_NEEDS_RESTART -eq 1 ]]; then
    log "dcrd config/binary changed; restarting dcrd"
    systemctl restart dcrd
  fi
  ok "dcrd service running (${NETWORK}, RPC 127.0.0.1:${RPC_PORT})"

  # dcrd writes rpc.cert on first start; wait for it, then expose to dcrdata.
  log "Waiting for dcrd to generate its RPC certificate"
  for _ in $(seq 1 30); do
    [[ -f "${NODE_DCRD_DIR}/rpc.cert" ]] && break
    sleep 1
  done
  [[ -f "${NODE_DCRD_DIR}/rpc.cert" ]] || die "dcrd rpc.cert not generated; check 'journalctl -u dcrd'"
  install -m 644 -o "$DATA_USER" -g "$DATA_USER" "${NODE_DCRD_DIR}/rpc.cert" "$DCRD_CERT_DST"
  ok "dcrd RPC cert published to ${DCRD_CERT_DST}"
fi

# ---- 6. Fetch & build dcrdata ---------------------------------------------

if [[ -d "${APP_DIR}/.git" ]]; then
  ACTION="upgrade"
  # Honor an explicit --repo change on upgrades; previously the flag was
  # silently ignored and the old origin kept being deployed. An origin still
  # on one of this repository's earlier URLs moves too.
  CUR_ORIGIN=$(sudo -u "$DATA_USER" git -C "$APP_DIR" remote get-url origin)
  if [[ "$CUR_ORIGIN" != "$REPO_URL" ]] \
     && { [[ $REPO_SET -eq 1 ]] || renamed_repo_url "$CUR_ORIGIN"; }; then
    log "Switching deploy repo: ${CUR_ORIGIN} -> ${REPO_URL}"
    sudo -u "$DATA_USER" git -C "$APP_DIR" remote set-url origin "$REPO_URL"
  fi
  log "Updating checkout (force-sync to remote; local edits to tracked files discarded)"
  sudo -u "$DATA_USER" git -C "$APP_DIR" fetch --depth 1 origin
  sudo -u "$DATA_USER" git -C "$APP_DIR" reset --hard '@{u}'
else
  ACTION="install"
  log "Cloning ${REPO_URL}"
  git clone --depth 1 "$REPO_URL" "$APP_DIR"
  chown -R "$DATA_USER:$DATA_USER" "$DATA_HOME"
fi

# Back end: build to a temp file, swapped into place atomically only on success
# so a broken build never takes down the running service. -buildvcs=false: the
# build runs as root over a checkout owned by the service user, which Go's VCS
# stamping would reject as "dubiously owned".
log "Building dcrdata"
( cd "$APP_CMD" && GOTOOLCHAIN=local "$GO" build -buildvcs=false -o "${APP_CMD}/dcrdata.new" . )
chown "$DATA_USER:$DATA_USER" "${APP_CMD}/dcrdata.new"
ok "dcrdata built"

# The front end is plain CSS + native ES modules served straight from
# cmd/dcrdata/public — no bundler, no Node.js, nothing to build.

# ---- 7. Configure dcrdata -------------------------------------------------

log "Writing dcrdata config"
install -d -o "$DATA_USER" -g "$DATA_USER" "$APPDATA"
cat > "${APPDATA}/dcrdata.conf" <<EOF
[Application Options]
$( [[ $TESTNET -eq 1 ]] && echo "testnet=1" )

# dcrd RPC
dcrduser=${DCRD_RPCUSER}
dcrdpass=${DCRD_RPCPASS}
dcrdserv=${DCRD_SERV}
dcrdcert=${DCRD_CERT_DST}

# PostgreSQL (peer auth over the local socket — no password)
pghost=/run/postgresql
pguser=${DATA_USER}
pgdbname=${DATA_USER}

# Web server — loopback; Caddy fronts it
apilisten=${LISTEN}
apiproto=http

# Behind a reverse proxy
userealip=true
trustproxy=true
$( [[ $HTTP_ONLY -eq 0 ]] && echo "allowedhost=${DOMAIN}" )
EOF
chown "$DATA_USER:$DATA_USER" "${APPDATA}/dcrdata.conf"
chmod 600 "${APPDATA}/dcrdata.conf"
ok "config written to ${APPDATA}/dcrdata.conf"

# ---- 8. dcrdata systemd service -------------------------------------------

log "Writing dcrdata systemd unit"
cat > /etc/systemd/system/dcrdata.service <<EOF
[Unit]
Description=dcrdata Decred block explorer
After=network-online.target postgresql.service dcrd.service
Wants=network-online.target

[Service]
User=${DATA_USER}
Group=${DATA_USER}
WorkingDirectory=${APP_CMD}
ExecStart=${APP_CMD}/dcrdata --appdata=${APPDATA}
# always, not on-failure: dcrdata requests its own shutdown on unrecoverable
# conditions — a notification handler failing, SignalHeight timing out, the web
# server dying — and those paths exit 0, which on-failure would treat as a
# clean stop and leave the explorer down. An explicit systemctl stop is still
# exempt, so this does not fight the repair above.
Restart=always
RestartSec=10
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=${DATA_HOME}

[Install]
WantedBy=multi-user.target
EOF

# ---- 8b. Repair a desynced stake database ---------------------------------
# dcrdata keeps two databases in step: the PostgreSQL index and the stake
# database. The sync loop can only connect the block exactly one above stakedb,
# and it starts from the PostgreSQL height plus one — so the two must be level
# at startup. Two things can leave stakedb further behind than that: an unclean
# stop, and a stake-database handler error on a new block, which the notifier
# logs as "block handler failed" and swallows while the PostgreSQL write goes
# ahead anyway. Nothing in dcrdata puts it back — the rewind only fires when
# stakedb is *ahead* (cmd/dcrdata/main.go). Every start then re-panics on the
# same block, "The stake database is corrupted", and systemd loops on it.
#
# The panic names its own cure. Purging N blocks from PostgreSQL drops it below
# stakedb, which makes that rewind fire and pulls stakedb level again; the
# purged blocks are re-indexed from dcrd. Purging throws away real work, so the
# trigger is narrow: the panic must be in the journal, and BOTH stores must
# still be exactly where it left them — stakedb at the height it reported and
# PostgreSQL one below the block it choked on. A healthy or already-repaired
# install fails those checks and is left untouched.

PURGE_N=""; PANIC_BLOCK=""; PANIC_STAKEDB=""; PG_HEIGHT=""; BLOCKS_TIP=""
LAST_STAKEDB=""
if command -v psql >/dev/null 2>&1; then
  PANIC_RE='wrong block: ([0-9]+), ([0-9]+)'
  # Filter inside the pipeline: a busy unit's 30 days of journal is large, and
  # only these two kinds of line are ever read out of it.
  DCRDATA_LOG=$(journalctl -u dcrdata --since=-30d --no-pager 2>/dev/null \
                  | grep -E 'About to connect the wrong block:|Current best block \(stakedb\)' \
                  || true)
  PANIC_LINE=$(grep -F 'About to connect the wrong block:' <<<"$DCRDATA_LOG" \
                 | tail -1 || true)

  if [[ "$PANIC_LINE" =~ $PANIC_RE ]]; then
    PANIC_BLOCK="${BASH_REMATCH[1]}"      # block dcrdata tried to connect
    PANIC_STAKEDB="${BASH_REMATCH[2]}"    # where stakedb actually was

    # Derive the count from this panic's own two numbers, which is the formula
    # sync.go itself uses. Do NOT scrape the suggested "--purge-n-blocks=N" out
    # of the log: sync.go emits that same phrase from an unrelated unknown-ticket
    # error with a very different N, and a whole-buffer match could pair the two.
    PURGE_N=$(( 2 * (PANIC_BLOCK - PANIC_STAKEDB) ))

    # Is that state still live? Check both halves. dcrdata logs the stake
    # database height on every start, and resumes at meta.best_block_height + 1
    # — so the panic still applies only while the stake database is where the
    # panic left it AND PostgreSQL sits one block below the block it choked on.
    # A hand-repair that resynced but stopped short would otherwise be
    # indistinguishable on the PostgreSQL side alone.
    LAST_STAKEDB=$(sed -n 's/.*Current best block (stakedb): *\([0-9]\{1,\}\).*/\1/p' \
                     <<<"$DCRDATA_LOG" | tail -1 || true)
    PG_HEIGHT=$(sudo -u "$DATA_USER" psql -d "$DATA_USER" -tAc \
                  'SELECT best_block_height FROM meta' 2>/dev/null \
                  | tr -d '[:space:]' || true)

    if [[ "$LAST_STAKEDB" != "$PANIC_STAKEDB" ]]; then
      log "Ignoring a stake database panic that no longer matches the last start"
      log "(stakedb last reported ${LAST_STAKEDB:-unknown}, panic was at ${PANIC_STAKEDB})"
      PURGE_N=""
    elif [[ ! "$PG_HEIGHT" =~ ^[0-9]+$ ]]; then
      warn "journal shows a stake database panic, but the PostgreSQL height could"
      warn "not be read to confirm it is current; not repairing"
      PURGE_N=""
    elif (( PG_HEIGHT != PANIC_BLOCK - 1 )); then
      log "Ignoring a stale stake database panic (PostgreSQL is now at ${PG_HEIGHT})"
      PURGE_N=""
    elif (( PURGE_N < 1 || PURGE_N > 1000 )); then
      warn "stake database panic asks to purge ${PURGE_N} blocks, which is outside"
      warn "the range this script will do unattended; repair by hand (see DEPLOY.md)"
      PURGE_N=""
    fi
  fi

  # The purge counts back from the *blocks* table tip, not from the meta height
  # the panic reported, and PurgeBestBlocks then rewinds stakedb to whatever it
  # lands on — erroring, and aborting startup entirely, if that is still above
  # stakedb. A torn write (block row committed, meta not yet) leaves the tip
  # above meta, so floor the count at the real distance rather than trusting the
  # panic's number. Aborting startup would be worse than the crash loop.
  if [[ -n "$PURGE_N" ]]; then
    BLOCKS_TIP=$(sudo -u "$DATA_USER" psql -d "$DATA_USER" -tAc \
                   'SELECT height FROM blocks WHERE is_mainchain ORDER BY height DESC LIMIT 1' \
                   2>/dev/null | tr -d '[:space:]' || true)
    if [[ "$BLOCKS_TIP" =~ ^[0-9]+$ ]] && (( BLOCKS_TIP - PANIC_STAKEDB > PURGE_N )); then
      if (( BLOCKS_TIP - PANIC_STAKEDB > 1000 )); then
        warn "blocks table tip (${BLOCKS_TIP}) is far above the stake database"
        warn "(${PANIC_STAKEDB}); too much to purge unattended, repair by hand"
        PURGE_N=""
      else
        PURGE_N=$(( BLOCKS_TIP - PANIC_STAKEDB ))
        log "Raising the purge to ${PURGE_N}: the blocks tip is ${BLOCKS_TIP}, above the"
        log "recorded best block ${PG_HEIGHT}, and a smaller purge would abort startup."
      fi
    fi
  fi
fi

# Atomically swap in the freshly built binary, then (re)start.
mv -f "${APP_CMD}/dcrdata.new" "${APP_CMD}/dcrdata"
systemctl daemon-reload
systemctl enable dcrdata >/dev/null 2>&1 || true
# systemctl refuses to start a unit that has hit a start limit, which would abort
# this script under set -e. Clear it unconditionally.
systemctl reset-failed dcrdata >/dev/null 2>&1 || true

if [[ -n "$PURGE_N" && $REPAIR -eq 0 ]]; then
  warn "dcrdata's stake database is desynced (pg ${PG_HEIGHT}, stakedb"
  warn "${PANIC_STAKEDB}) and it cannot sync past block ${PANIC_BLOCK}. --no-repair"
  warn "was given, so leaving it alone; see DEPLOY.md to repair it by hand"
  PURGE_N=""
fi

if [[ -n "$PURGE_N" ]]; then
  # A hand-run repair may already be in progress, and the stake node store takes
  # an exclusive lock, so don't fight it. Exclude the service's own process:
  # during the crash loop it is running most of the time, and matching it would
  # skip the repair exactly when it is needed.
  SERVICE_PID=$(systemctl show -p MainPID --value dcrdata 2>/dev/null || echo 0)
  OTHER_DCRDATA=$(pgrep -u "$DATA_USER" -x dcrdata 2>/dev/null \
                    | grep -vx "${SERVICE_PID:-0}" || true)
  if [[ -n "$OTHER_DCRDATA" ]]; then
    warn "another dcrdata is running as ${DATA_USER} outside systemd (pid"
    warn "$(tr '\n' ' ' <<<"$OTHER_DCRDATA")); leaving the repair to it"
  else
    log "Stake database is $(( PG_HEIGHT - PANIC_STAKEDB )) block(s) behind PostgreSQL"
    log "(pg ${PG_HEIGHT}, stakedb ${PANIC_STAKEDB}) — dcrdata cannot sync past this."
    log "Purging ${PURGE_N} blocks; runs in the foreground, output below."
    systemctl stop dcrdata

    # The unit is stopped from here until the restart below, so bring it back if
    # this script exits early. Note the limit: bash defers a trap until the
    # current foreground command returns, so this covers an error exit under
    # set -e but NOT a signal arriving mid-repair — a dropped SSH session can
    # still leave the unit stopped. Re-running deploy.sh recovers that.
    trap 'systemctl start dcrdata >/dev/null 2>&1 || true' EXIT
    trap 'exit 130' INT TERM HUP

    # One-shot, on the command line: never written to dcrdata.conf or a drop-in,
    # so it cannot linger and purge again on a later start. --sync-and-quit skips
    # the web server, so this does not race the service for the listen address.
    # dcrdata resolves views_v2/ and public/ from the working directory, hence
    # the subshell cd; sudo leaves the working directory alone. --foreground
    # keeps the run attached to this terminal so Ctrl-C reaches it.
    #
    # The timeout is deliberately short. The purge and the stake-database rewind
    # both complete within the first seconds; everything after that is the
    # ordinary catch-up sync, which the service does perfectly well on its own
    # while serving its syncing page. Blocking the deploy for hours to finish it
    # here would only keep the site down longer.
    REPAIR_LOG=$(mktemp)
    REPAIR_RC=0
    ( cd "$APP_CMD" && timeout --foreground 30m sudo -u "$DATA_USER" ./dcrdata \
        --appdata="$APPDATA" --purge-n-blocks="$PURGE_N" --sync-and-quit ) 2>&1 \
        | tee "$REPAIR_LOG" || REPAIR_RC=$?

    trap - EXIT INT TERM HUP

    # Judge success by what the run actually did, not by its exit status: the
    # timeout above is expected to cut the catch-up sync short. dcrdata only
    # logs this after PurgeBestBlocks returns cleanly, and that call fails if
    # the stake-database rewind did not reach the purged height — so this one
    # line is evidence the repair itself landed.
    if grep -q 'Successfully purged data for' "$REPAIR_LOG"; then
      ok "purged ${PURGE_N} blocks and rewound the stake database"
      (( REPAIR_RC == 0 )) || log "catch-up sync unfinished; the service resumes it"
    else
      warn "the purge did not complete — starting the service anyway rather than"
      warn "leaving the site down. Check 'journalctl -u dcrdata' and DEPLOY.md"
    fi
    rm -f "$REPAIR_LOG"
  fi
fi

systemctl restart dcrdata
ok "dcrdata service running"

# ---- 9. Caddy ------------------------------------------------------------

CADDY_FRESH=0
if command -v caddy >/dev/null 2>&1; then
  # Pick up Caddy releases on re-runs; the lists were refreshed in step 1.
  # confold keeps the installed Caddyfile instead of stopping at dpkg's
  # conffile prompt (a managed one is rewritten below anyway). A Caddy that
  # apt did not install is skipped, not replaced. dcrdata has already been
  # restarted by now, so a failed upgrade warns rather than aborting before
  # the Caddyfile and the deployment state are written.
  if apt-get install -y -qq --only-upgrade \
       -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold \
       caddy >/dev/null; then
    ok "Caddy up to date ($(caddy version | head -1))"
  else
    warn "Caddy upgrade failed; continuing with $(caddy version | head -1)"
  fi
else
  log "Installing Caddy"
  # --yes: a prior partial run may have left the keyring file behind, and
  # gpg --dearmor refuses to overwrite without it, breaking re-runs.
  curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' \
    | gpg --dearmor --yes -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
  curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' \
    > /etc/apt/sources.list.d/caddy-stable.list
  apt-get update -qq
  apt-get install -y -qq caddy >/dev/null
  # The .deb ships its own /etc/caddy/Caddyfile (the welcome page) and starts
  # caddy with it, so from here that file exists but is dpkg's, not an
  # operator's. The guard below needs to know that.
  CADDY_FRESH=1
  ok "Caddy installed"
fi

log "Writing Caddyfile"
SITE="$DOMAIN"; [[ $HTTP_ONLY -eq 1 ]] && SITE=":80"
CADDYFILE=/etc/caddy/Caddyfile
CADDY_MARKER="# Managed by dcrdata deploy.sh — remove this line to take manual control."
CADDY_NEW="$(mktemp)"
# Tabs are required for Caddyfile block indentation. The header_up lines
# matter: dcrdata runs with userealip=true, and its RealIP middleware prefers
# True-Client-IP and X-Real-IP over X-Forwarded-For — Caddy manages only the
# X-Forwarded-* headers, so without these a client could spoof its address.
cat > "$CADDY_NEW" <<EOF
${CADDY_MARKER}
${SITE} {
	encode zstd gzip
	reverse_proxy ${LISTEN} {
		header_up -True-Client-IP
		header_up X-Real-IP {remote_host}
	}

	@assets path /css/* /js/* /fonts/* /images/*
	header @assets Cache-Control "public, max-age=604800"

	header {
		Referrer-Policy "same-origin"
		X-Content-Type-Options "nosniff"
		X-Frame-Options "SAMEORIGIN"
$( [[ $HTTP_ONLY -eq 0 ]] && printf '\t\tStrict-Transport-Security "max-age=31536000; includeSubDomains"' )
		-Server
	}
}
EOF
# Overwrite only files this script owns (first line = managed marker). An
# existing file without the marker may carry operator edits — the Cloudflare
# or origin-cert changes DEPLOY.md describes, or a pre-marker script write —
# and telling those apart is not possible, so never overwrite it: warn with
# the important directives instead.
#
# Two files provably carry no operator edits, and both must be overwritten or a
# fresh VPS finishes the deploy serving Caddy's welcome page instead of the
# explorer: the one dpkg unpacked seconds ago in this same run, and Caddy's
# stock config, which is recognisable by the file_server root only it carries.
CADDY_UNMANAGED=0
if [[ -f "$CADDYFILE" ]] && ! grep -qF "$CADDY_MARKER" "$CADDYFILE" \
   && [[ $CADDY_FRESH -eq 0 ]] && ! grep -qF 'root * /usr/share/caddy' "$CADDYFILE"; then
  rm -f "$CADDY_NEW"
  CADDY_UNMANAGED=1
  warn "existing ${CADDYFILE} is not managed by this script; leaving it untouched."
  warn "ensure its reverse_proxy block strips client IP spoofing:"
  warn "    header_up -True-Client-IP"
  warn "    header_up X-Real-IP {remote_host}"
  warn "delete ${CADDYFILE} and re-run to regenerate a managed one."
elif [[ -f "$CADDYFILE" ]] && cmp -s "$CADDY_NEW" "$CADDYFILE"; then
  rm -f "$CADDY_NEW"
  ok "Caddyfile unchanged"
else
  # Never overwrite a file this script did not write without leaving a copy.
  if [[ -f "$CADDYFILE" ]] && ! grep -qF "$CADDY_MARKER" "$CADDYFILE"; then
    cp -a "$CADDYFILE" "${CADDYFILE}.bak"
    log "saved the previous ${CADDYFILE} to ${CADDYFILE}.bak"
  fi
  install -m 644 "$CADDY_NEW" "$CADDYFILE"
  rm -f "$CADDY_NEW"
  caddy validate --config "$CADDYFILE" >/dev/null
  systemctl reload caddy
  ok "Caddy configured for ${SITE}"
fi

# ---- Record deployment state ----------------------------------------------

# Later re-runs inherit these choices and refuse incompatible ones; see the
# state checks after argument parsing. No secrets are stored here.
cat > "$STATE_FILE" <<EOF
# Recorded by dcrdata deploy.sh — used by re-runs to inherit these options and
# refuse network/topology changes that would desync configs from data.
# Delete this file (after wiping the data) to reconfigure from scratch.
NETWORK=${NETWORK}
SKIP_DCRD=${SKIP_DCRD}
DOMAIN=${DOMAIN}
HTTP_ONLY=${HTTP_ONLY}
REPO_URL=${REPO_URL}
LISTEN=${LISTEN}
EXT_DCRDSERV=${DCRD_SERV}
EXT_DCRDUSER=${DCRD_RPCUSER}
EOF
chmod 644 "$STATE_FILE"

# ---- Done -----------------------------------------------------------------

echo
if [[ $CADDY_UNMANAGED -eq 1 ]]; then
  warn "Deployment incomplete: ${CADDYFILE} was left as it was, so nothing"
  warn "proxies to dcrdata yet. dcrd, PostgreSQL and dcrdata are installed and"
  warn "running on ${LISTEN}; add a reverse_proxy to it, or delete that file"
  warn "and re-run to have one generated."
elif [[ "$ACTION" == "upgrade" ]]; then
  ok "Upgrade complete — dcrdata rebuilt and restarted."
else
  ok "Deployment complete (${NETWORK})."
fi
if [[ $HTTP_ONLY -eq 1 ]]; then
  IP="$(curl -fsSL https://api.ipify.org 2>/dev/null || echo '<server-ip>')"
  echo "   ${C_DIM}Site:${C_OFF}  http://${IP}/"
else
  echo "   ${C_DIM}Site:${C_OFF}  https://${DOMAIN}/   ${C_DIM}(TLS provisions on first request)${C_OFF}"
fi
cat <<EOF
   ${C_DIM}dcrd:${C_OFF}     journalctl -u dcrd -f
   ${C_DIM}dcrdata:${C_OFF}  journalctl -u dcrdata -f
   ${C_DIM}caddy:${C_OFF}    journalctl -u caddy -f

First run syncs dcrd and then builds dcrdata's PostgreSQL index from genesis —
expect many hours. The site serves a "syncing" status page until it completes.
EOF
