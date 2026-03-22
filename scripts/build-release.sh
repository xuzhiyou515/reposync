#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RELEASE_DIR="$REPO_ROOT/release"
BACKEND_DIR="$REPO_ROOT/backend"
FRONTEND_DIR="$REPO_ROOT/frontend"
EMBEDDED_FRONTEND_DIR="$BACKEND_DIR/internal/app/embedded"

echo "Building frontend..."
pushd "$FRONTEND_DIR" >/dev/null
if [[ ! -f "$FRONTEND_DIR/node_modules/vite/bin/vite.js" ]]; then
  npm ci
fi
node "$FRONTEND_DIR/node_modules/vite/bin/vite.js" build
popd >/dev/null

echo "Building backend..."
rm -rf "$RELEASE_DIR"
mkdir -p "$RELEASE_DIR/backend" "$RELEASE_DIR/frontend" "$RELEASE_DIR/config" "$RELEASE_DIR/data"

echo "Embedding frontend assets into backend binary..."
EMBEDDED_BACKUP_DIR="$(mktemp -d)"
cp -R "$EMBEDDED_FRONTEND_DIR/." "$EMBEDDED_BACKUP_DIR/"
rm -rf "$EMBEDDED_FRONTEND_DIR"/*
cp -R "$FRONTEND_DIR/dist/." "$EMBEDDED_FRONTEND_DIR/"

cleanup() {
  rm -rf "$EMBEDDED_FRONTEND_DIR"/*
  cp -R "$EMBEDDED_BACKUP_DIR/." "$EMBEDDED_FRONTEND_DIR/"
  rm -rf "$EMBEDDED_BACKUP_DIR"
}
trap cleanup EXIT

pushd "$BACKEND_DIR" >/dev/null
go build -o "$RELEASE_DIR/backend/reposync" ./cmd/server
popd >/dev/null

trap - EXIT
cleanup

cp -R "$FRONTEND_DIR/dist" "$RELEASE_DIR/frontend/dist"
cp "$REPO_ROOT/scripts/reposync.env.example" "$RELEASE_DIR/config/reposync.env.example"
cp "$REPO_ROOT/scripts/run-release.ps1" "$RELEASE_DIR/run.ps1"
cp "$REPO_ROOT/scripts/run-release.sh" "$RELEASE_DIR/run.sh"
cp "$REPO_ROOT/docs/deployment.md" "$RELEASE_DIR/DEPLOYMENT.md"

echo "Release bundle created at $RELEASE_DIR"
