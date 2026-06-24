# WhatsApp MCP — one-step setup for Windows.
# Checks Docker, generates secrets, starts the bridge, fetches the connection
# wizard, and opens it in your browser. Double-click ..\start.cmd to run.

#Requires -Version 5
$ErrorActionPreference = "Stop"

# Repo to download prebuilt binaries from. Change this if you run your own fork.
$Repo = "iamatulsingh/whatsapp-mcp-go"

# Work from the repo root (this script lives in scripts\).
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

function Have($cmd) { [bool](Get-Command $cmd -ErrorAction SilentlyContinue) }
function Fail($msg) { Write-Host $msg -ForegroundColor Red; Read-Host "Press Enter to exit"; exit 1 }

Write-Host "== WhatsApp MCP - easy start ==" -ForegroundColor Cyan

# 1. Docker present + running.
if (-not (Have docker)) {
  Write-Host "Docker isn't installed. Install Docker Desktop, then run this again:" -ForegroundColor Yellow
  Write-Host "  https://www.docker.com/products/docker-desktop/"
  Read-Host "Press Enter to exit"; exit 1
}
docker info *> $null
if ($LASTEXITCODE -ne 0) {
  Fail "Docker is installed but not running. Start Docker Desktop, wait until it's ready, then run this again."
}

# 2. Create .env with fresh random secrets if it doesn't exist yet.
if (-not (Test-Path ".env")) {
  Write-Host "Creating .env with fresh secrets..."
  function RandB64($n) {
    $b = New-Object byte[] $n
    [System.Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($b)
    [Convert]::ToBase64String($b)
  }
  $content = @(
    "WHATSAPP_API_KEY=$(RandB64 48)",
    "WHATSAPP_JWT_SECRET=$(RandB64 48)",
    "POSTGRES_USER=whatsapp",
    "POSTGRES_PASS=$((RandB64 24) -replace '[+/=]','x')",
    "LOG_LEVEL=info"
  ) -join "`n"
  # UTF-8 without BOM so docker compose reads the first variable correctly.
  [System.IO.File]::WriteAllText("$root\.env", $content + "`n", (New-Object System.Text.UTF8Encoding($false)))
} else {
  Write-Host ".env already exists - keeping your settings."
}

# 3. Build + start the stack.
Write-Host "Starting the bridge and database (the first run can take a few minutes)..."
docker compose up -d --build
if ($LASTEXITCODE -ne 0) { Fail "docker compose failed to start the stack." }

# 4. Get the connection-wizard binary: use a local build, else download a
#    release, else build from source if Go is available.
$exe = "$root\whatsapp-mcp-server\whatsapp-mcp.exe"
if (-not (Test-Path $exe)) {
  $url = "https://github.com/$Repo/releases/latest/download/whatsapp-mcp_windows_amd64.exe"
  Write-Host "Downloading the connection wizard..."
  try {
    Invoke-WebRequest -Uri $url -OutFile $exe -UseBasicParsing
  } catch {
    if (Have go) {
      Write-Host "Download unavailable; building from source instead..."
      Push-Location "$root\whatsapp-mcp-server"
      $env:CGO_ENABLED = "0"
      go build -o whatsapp-mcp.exe .
      Pop-Location
      if (-not (Test-Path $exe)) { Fail "Build failed." }
    } else {
      Fail "Couldn't download the wizard, and Go isn't installed to build it. See the README for manual steps."
    }
  }
}

# 5. Launch the wizard. It auto-detects the API key from .env.
Write-Host "Opening the connection wizard in your browser..." -ForegroundColor Green
& $exe connect
