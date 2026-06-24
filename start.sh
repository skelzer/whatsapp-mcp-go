#!/usr/bin/env bash
# WhatsApp MCP - one-step setup for macOS / Linux.
# Checks Docker, generates secrets, starts the bridge, fetches the connection
# wizard, and opens it in your browser.
set -euo pipefail

# Repo to download prebuilt binaries from. Change this if you run your own fork.
REPO="skelzer/whatsapp-mcp-go"

cd "$(dirname "$0")"

echo "== WhatsApp MCP - easy start =="

# 1. Docker present + running.
if ! command -v docker >/dev/null 2>&1; then
  echo "Docker isn't installed. Install Docker, then run this again:"
  echo "  https://www.docker.com/products/docker-desktop/"
  exit 1
fi
if ! docker info >/dev/null 2>&1; then
  echo "Docker is installed but not running. Start Docker and run this again."
  exit 1
fi

# 2. Create .env with fresh random secrets if it doesn't exist yet.
gen() { openssl rand -base64 "$1" 2>/dev/null || head -c "$1" /dev/urandom | base64; }
if [ ! -f .env ]; then
  echo "Creating .env with fresh secrets..."
  {
    echo "WHATSAPP_API_KEY=$(gen 48 | tr -d '\n')"
    echo "WHATSAPP_JWT_SECRET=$(gen 48 | tr -d '\n')"
    echo "POSTGRES_USER=whatsapp"
    echo "POSTGRES_PASS=$(gen 24 | tr -d '\n' | tr '+/=' 'xxx')"
    echo "LOG_LEVEL=info"
  } > .env
else
  echo ".env already exists - keeping your settings."
fi

# 3. Build + start the stack.
echo "Starting the bridge and database (the first run can take a few minutes)..."
docker compose up -d --build

# 4. Get the connection-wizard binary: use a local build, else download a
#    release, else build from source if Go is available.
os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m); case "$arch" in x86_64|amd64) arch=amd64 ;; arm64|aarch64) arch=arm64 ;; esac
exe="./whatsapp-mcp-server/whatsapp-mcp"
if [ ! -x "$exe" ]; then
  url="https://github.com/$REPO/releases/latest/download/whatsapp-mcp_${os}_${arch}"
  echo "Downloading the connection wizard..."
  if curl -fsSL "$url" -o "$exe" 2>/dev/null; then
    chmod +x "$exe"
  elif command -v go >/dev/null 2>&1; then
    echo "Download unavailable; building from source instead..."
    ( cd whatsapp-mcp-server && CGO_ENABLED=0 go build -o whatsapp-mcp . )
  else
    echo "Couldn't download the wizard, and Go isn't installed to build it. See the README."
    exit 1
  fi
fi

# 5. Launch the wizard. It auto-detects the API key from .env.
echo "Opening the connection wizard in your browser..."
"$exe" connect
