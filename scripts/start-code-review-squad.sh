#!/usr/bin/env bash
set -euo pipefail

export HTTP_PROXY="http://br0ke:neskaju@72.56.83.57:3128"
export HTTPS_PROXY="http://br0ke:neskaju@72.56.83.57:3128"
export ALL_PROXY="http://br0ke:neskaju@72.56.83.57:3128"
export NO_PROXY="127.0.0.1,localhost,minikube,*.avito.dev,*.avito.ru,*.k.avito.ru,*.db.avito-sd,192.168.*,*.avito.local,*.avito.lan,*.avito.sd,*.avito.st,*.k8s,raw.githubusercontent.com,registry.npmjs.org,pypi.org,*github.com,github.com,pypi.python.org,repo.gradle.org,*schemastore.org,*maven.org,*json-schema.org,packagist.org,go.dev,*.avito-sd"

OPENCODE_URL="http://127.0.0.1:4096"
OPENCODE_PID=""

opencode_is_ready() {
  curl -sS -o /dev/null "$OPENCODE_URL"
}

cleanup() {
  if [[ -n "$OPENCODE_PID" ]]; then
    kill "$OPENCODE_PID" 2>/dev/null || true
    wait "$OPENCODE_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT INT TERM

if ! opencode_is_ready; then
  if ! command -v opencode >/dev/null 2>&1; then
    echo "opencode is required for the Critic agent but was not found in PATH" >&2
    exit 1
  fi

  opencode serve &
  OPENCODE_PID="$!"

  for _ in $(seq 1 50); do
    if opencode_is_ready; then
      break
    fi
    sleep 0.2
  done

  if ! opencode_is_ready; then
    echo "opencode serve did not become ready at $OPENCODE_URL" >&2
    exit 1
  fi
fi

exec go run ./cmd/agent-debug-squad serve --config configs/code-review-squad.yaml
