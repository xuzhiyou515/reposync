#!/usr/bin/env bash
set -euo pipefail

RELEASE_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="$RELEASE_ROOT/config/reposync.env"
BACKEND_EXE="$RELEASE_ROOT/backend/reposync"

if [[ ! -f "$BACKEND_EXE" ]]; then
  echo "Backend binary not found: $BACKEND_EXE" >&2
  exit 1
fi

if [[ -f "$ENV_FILE" ]]; then
  set -a
  # shellcheck disable=SC1090
  source "$ENV_FILE"
  set +a
fi

export REPOSYNC_FRONTEND_DIST="${REPOSYNC_FRONTEND_DIST:-$RELEASE_ROOT/frontend/dist}"
export REPOSYNC_DB_PATH="${REPOSYNC_DB_PATH:-$RELEASE_ROOT/data/reposync.db}"
export REPOSYNC_CACHE_DIR="${REPOSYNC_CACHE_DIR:-$RELEASE_ROOT/data/cache}"

exec "$BACKEND_EXE"
