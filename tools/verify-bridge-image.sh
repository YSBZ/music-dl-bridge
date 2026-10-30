#!/usr/bin/env bash
set -euo pipefail
image="${1:?image required}"
name="music-bridge-verify-$$"
work="$(mktemp -d)"
cleanup() { docker rm -f "$name" >/dev/null 2>&1 || true; rm -rf "$work"; }
trap cleanup EXIT
mkdir -p "$work/data" "$work/downloads"
docker run -d --name "$name" -p 127.0.0.1::8080 \
  -e MUSIC_BRIDGE_TOKEN=ci-test-token \
  -v "$work/data:/home/appuser/data" \
  -v "$work/downloads:/home/appuser/data/downloads" \
  -v "$work/downloads:/app/downloads" "$image" >/dev/null
port="$(docker port "$name" 8080/tcp | head -1 | sed 's/.*://')"
url="http://127.0.0.1:$port/music"
ready=false
for i in $(seq 1 30); do
  if curl -fsS "$url" -o /dev/null; then ready=true; break; fi
  sleep 1
done
[ "$ready" = true ]
[ "$(curl -s -o /dev/null -w '%{http_code}' "$url/api/music-bridge/search?q=test")" = 401 ]
[ "$(curl -s -o /dev/null -w '%{http_code}' -H 'Authorization: Bearer ci-test-token' "$url/api/music-bridge/search")" = 400 ]
docker exec "$name" ffprobe -version >/dev/null
docker exec "$name" sh -c 'test -s /etc/ssl/certs/ca-certificates.crt; echo verify > /app/downloads/.bridge-check; test -f /home/appuser/data/downloads/.bridge-check'
docker restart "$name" >/dev/null
docker exec "$name" sh -c 'test -f /app/downloads/.bridge-check'
echo 'Container startup, auth, media tools, shared mounts and persistence verified.'
