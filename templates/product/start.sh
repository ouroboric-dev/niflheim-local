#!/bin/bash
# The product start script. In the grader (one container, no network, an ordinary user) it starts Postgres, the S3
# store (SeaweedFS), the API and the web leaf, and serves everything on port 3000. Odin's init writes it at the
# project root as start.sh. The product keeps all its data under .data/ in this folder, so a restart finds it.
# Each leaf starts from .ygg/release/repos/<leaf>/run.sh, or from repos/<leaf>/run.sh when there is no release.
# Postgres (initdb, postgres, createdb) and weed come from the task image's PATH. Needs bash 5.1 or later.
# YGG_START_WAIT (default 100) is the number of seconds all the waits for ports share.
set -euo pipefail

if [ "$(id -u)" -eq 0 ]; then
  echo "start: refusing to run as root; run it as the product's user"
  exit 1
fi
cd "$(dirname "${BASH_SOURCE[0]}")"
root="$PWD"

pg_port="${YGG_PG_PORT:-5432}" s3_port="${YGG_S3_PORT:-8333}"
api_port="${YGG_API_PORT:-8080}" web_port="${YGG_WEB_PORT:-3000}"
bucket="ygg-store" region="us-east-1" lo="127.0.0.1"

# leaf_run NAME prints the script that starts the leaf NAME.
leaf_run() {
  local release=".ygg/release/repos/$1/run.sh" leaf="repos/$1/run.sh"
  if [ -f "$release" ]; then echo "$release"; elif [ -f "$leaf" ]; then echo "$leaf"; else
    echo "start: neither $release nor $leaf exists" >&2
    return 1
  fi
}
for name in web api; do
  if [ ! -d "repos/$name" ]; then
    echo "start: the $name leaf is missing: $root/repos/$name"
    exit 1
  fi
done
api_run="$(leaf_run api)" || exit 1
web_run="$(leaf_run web)" || exit 1

data="$root/.data"
mkdir -p "$data/api" "$data/seaweed"
log="$(mktemp -d)"
names=() pids=() secret=""

# shellcheck disable=SC2329 # cleanup runs from the EXIT trap
cleanup() {
  trap '' TERM INT
  local i p
  for ((i = ${#pids[@]} - 1; i >= 0; i--)); do
    # Postgres shuts down fast on INT; the other parts stop on TERM. Each part has a process group of its own.
    if [ "${names[i]}" = postgres ]; then kill -INT -- "-${pids[i]}" 2>/dev/null || true
    else kill -TERM -- "-${pids[i]}" 2>/dev/null || true; fi
  done
  # A part that has not ended in 10 seconds is killed. The watchdog holds no output open.
  (
    sleep 10
    for p in "${pids[@]}"; do kill -KILL -- "-$p" 2>/dev/null || true; done
  ) >/dev/null 2>&1 &
  local watchdog=$!
  wait "${pids[@]}" 2>/dev/null || true
  kill "$watchdog" 2>/dev/null || true
  rm -rf -- "$log"
}
trap cleanup EXIT
trap 'exit 143' TERM
trap 'exit 130' INT

# show_log NAME prints the end of a part's log, without the secret key.
show_log() {
  tail -n 15 "$log/$1.log" 2>/dev/null | { if [ -n "$secret" ]; then sed "s/$secret/***/g"; else cat; fi; } || true
}

# launch NAME COMMAND... starts a part in a process group of its own, with its output in the log.
launch() {
  local name="$1"
  shift
  setsid "$@" >"$log/$name.log" 2>&1 &
  names+=("$name")
  pids+=($!)
}

deadline=$((SECONDS + ${YGG_START_WAIT:-100}))
# wait_for NAME PORT waits until the part NAME, the last one launched, answers on PORT.
wait_for() {
  local name="$1" port="$2" pid="${pids[${#pids[@]} - 1]}"
  while ! (exec 3<>"/dev/tcp/$lo/$port") 2>/dev/null; do
    if ! kill -0 "$pid" 2>/dev/null; then
      echo "start: $name stopped before it opened port $port"
      show_log "$name"
      exit 1
    fi
    if [ "$SECONDS" -ge "$deadline" ]; then
      echo "start: $name did not answer on port $port in time"
      show_log "$name"
      exit 1
    fi
    sleep 0.2
  done
}

echo "== postgres"
pgdata="$data/postgres"
if [ ! -f "$pgdata/PG_VERSION" ]; then
  initdb -D "$pgdata" -U postgres --auth=trust -E UTF8 --locale=C.UTF-8 >"$log/initdb.log" 2>&1 || {
    echo "start: initdb failed"
    show_log initdb
    exit 1
  }
fi
pgsock="$log/pgsock"
mkdir -p "$pgsock"
launch postgres postgres -D "$pgdata" -h "$lo" -p "$pg_port" -k "$pgsock"
wait_for postgres "$pg_port"
if [ ! -f "$data/.database-created" ]; then
  # The port opens before Postgres accepts connections, so createdb tries again until it does.
  until createdb -h "$lo" -p "$pg_port" -U postgres app >"$log/createdb.log" 2>&1; do
    if [ "$SECONDS" -ge "$deadline" ]; then
      echo "start: createdb could not make the database app"
      show_log createdb
      exit 1
    fi
    sleep 0.5
  done
  touch "$data/.database-created"
fi

echo "== s3"
# A new key pair at each start, kept only in the identity file (mode 0600) and the API's environment.
access_key="$(od -An -N10 -tx1 /dev/urandom | tr -d ' \n')"
secret="$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n')"
identity="$data/seaweed-identity.json"
(
  umask 077
  cat >"$identity" <<JSON
{"identities":[{"name":"app","credentials":[{"accessKey":"$access_key","secretKey":"$secret"}],"actions":["Admin","Read","Write","List","Tagging"]}]}
JSON
)
chmod 600 "$identity"
launch weed weed server -s3 -ip="$lo" -ip.bind="$lo" -dir="$data/seaweed" -s3.config="$identity" \
  -s3.port="$s3_port" -master.port=9333 -volume.port=8081 -filer.port=8888 -master.volumeSizeLimitMB=256
wait_for weed "$s3_port"
s3_endpoint="http://$lo:$s3_port"

echo "== api"
launch api env -C "$(dirname "$api_run")" HOST="$lo" PORT="$api_port" \
  DATABASE_URL="postgres://postgres@$lo:$pg_port/app?sslmode=disable" DATA_DIR="$data/api" \
  S3_ENDPOINT="$s3_endpoint" S3_REGION="$region" S3_BUCKET="$bucket" \
  S3_ACCESS_KEY_ID="$access_key" S3_SECRET_ACCESS_KEY="$secret" "$root/$api_run"
wait_for api "$api_port"

echo "== web"
launch web env -C "$(dirname "$web_run")" HOST=0.0.0.0 PORT="$web_port" API_URL="http://$lo:$api_port" \
  S3_ENDPOINT="$s3_endpoint" S3_BUCKET="$bucket" "$root/$web_run"
wait_for web "$web_port"
echo "== ready on port $web_port"

# Run until a part ends or the script is stopped; then the trap stops the rest.
status=0
wait -n -p ended "${pids[@]}" || status=$?
for i in "${!pids[@]}"; do
  if [ "${pids[i]}" = "${ended:-}" ]; then
    echo "start: ${names[i]} stopped (status $status)"
    show_log "${names[i]}"
  fi
done
exit 1
