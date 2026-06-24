package helpers

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

//go:embed gui_assets/wizard.html
var guiAssets embed.FS

// guiServer backs the browser-based connection wizard. It serves an embedded
// page on localhost and proxies the bridge's status + pairing-QR endpoints so
// the API key never leaves this process.
type guiServer struct {
	doneOnce sync.Once
	done     chan struct{}
}

// RunConnectionWizardGUI starts the local web wizard, opens the browser, and
// returns once WhatsApp is linked (or the process is interrupted).
func RunConnectionWizardGUI() error {
	if apiKey == "" {
		fmt.Println("✗ WHATSAPP_API_KEY is not set.")
		fmt.Println("  Set it to the same value the bridge uses, then run the wizard again:")
		fmt.Println("    Windows (PowerShell):  $env:WHATSAPP_API_KEY = \"<your key>\"")
		fmt.Println("    macOS/Linux:           export WHATSAPP_API_KEY=\"<your key>\"")
		return fmt.Errorf("WHATSAPP_API_KEY not set")
	}

	s := &guiServer{done: make(chan struct{})}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/info", s.handleInfo)
	mux.HandleFunc("/api/write-config", s.handleWriteConfig)
	mux.HandleFunc("/api/done", s.handleDone)
	mux.HandleFunc("/qr.png", s.handleQR)

	// Bind to loopback on an ephemeral port so nothing is exposed externally.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("failed to start local wizard server: %w", err)
	}
	url := fmt.Sprintf("http://%s/", ln.Addr().String())
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()

	printGUIBanner(url)
	if err := openBrowser(url); err != nil {
		fmt.Println("  (couldn't open the browser automatically — open the URL above manually)")
	}
	fmt.Println("\nComplete the steps in your browser. This window closes when you")
	fmt.Println("click \"Finish\" there (or press Ctrl+C).")

	// Block until the page signals it is finished.
	<-s.done
	fmt.Println("\n✓ All set. You can close the browser tab.")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	return nil
}

func (s *guiServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := guiAssets.ReadFile("gui_assets/wizard.html")
	if err != nil {
		http.Error(w, "wizard asset missing", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

func (s *guiServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	st, err := fetchStatus()
	resp := map[string]any{}
	if err != nil {
		resp["reachable"] = false
		resp["error"] = err.Error()
	} else {
		resp["reachable"] = true
		resp["connected"] = st.Connected
		resp["logged_in"] = st.LoggedIn
		resp["pairing_required"] = st.PairingRequired
	}
	writeJSON(w, resp)
}

// handleDone lets the page tell the wizard the user is finished, so the CLI can
// stop serving and exit cleanly.
func (s *guiServer) handleDone(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.doneOnce.Do(func() { close(s.done) })
	writeJSON(w, map[string]any{"ok": true})
}

func (s *guiServer) handleInfo(w http.ResponseWriter, r *http.Request) {
	exe, _ := os.Executable()
	writeJSON(w, map[string]any{"exe": exe, "apiBaseURL": apiBaseURL})
}

// claudeConfigPath returns the default Claude Desktop config path for this OS.
func claudeConfigPath() (string, error) {
	switch runtime.GOOS {
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			return "", fmt.Errorf("APPDATA is not set")
		}
		return filepath.Join(appData, "Claude", "claude_desktop_config.json"), nil
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json"), nil
	default:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".config", "Claude", "claude_desktop_config.json"), nil
	}
}

// handleWriteConfig merges the whatsapp-mcp server entry into the user's Claude
// Desktop config, preserving any existing settings, backing up the old file,
// and writing UTF-8 without a BOM (which Claude Desktop rejects).
func (s *guiServer) handleWriteConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path, err := claudeConfigPath()
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	cfg := map[string]any{}
	var backup string
	if orig, err := os.ReadFile(path); err == nil {
		// Tolerate an existing UTF-8 BOM when parsing.
		clean := bytes.TrimPrefix(orig, []byte{0xEF, 0xBB, 0xBF})
		if len(bytes.TrimSpace(clean)) > 0 {
			if err := json.Unmarshal(clean, &cfg); err != nil {
				writeJSON(w, map[string]any{"ok": false, "path": path,
					"error": "existing config is not valid JSON; not overwriting it: " + err.Error()})
				return
			}
		}
		// Back up the original bytes before changing anything.
		backup = path + ".bak"
		_ = os.WriteFile(backup, orig, 0o600)
	}

	servers, _ := cfg["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	exe, _ := os.Executable()
	servers["whatsapp-mcp"] = map[string]any{
		"command": exe,
		"env": map[string]any{
			"WHATSAPP_API_KEY": apiKey,
			"API_BASE_URL":     apiBaseURL,
		},
	}
	cfg["mcpServers"] = servers

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "path": path, "error": err.Error()})
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		writeJSON(w, map[string]any{"ok": false, "path": path, "error": err.Error()})
		return
	}
	// os.WriteFile emits raw UTF-8 bytes — no BOM.
	if err := os.WriteFile(path, out, 0o600); err != nil {
		writeJSON(w, map[string]any{"ok": false, "path": path, "error": err.Error()})
		return
	}

	writeJSON(w, map[string]any{"ok": true, "path": path, "backup": backup})
}

// handleQR proxies the bridge's pairing-QR PNG, adding the JWT server-side so
// the browser never sees the API key. Returns 204 when no QR is available yet.
func (s *guiServer) handleQR(w http.ResponseWriter, r *http.Request) {
	token, err := GetOrRefreshJwtToken()
	if err != nil {
		http.Error(w, "bridge auth failed", http.StatusBadGateway)
		return
	}
	req, err := http.NewRequest(http.MethodGet, apiBaseURL+"/auth/pairing-qr", nil)
	if err != nil {
		http.Error(w, "bad request", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := (&http.Client{Timeout: apiTimeout}).Do(req)
	if err != nil {
		http.Error(w, "bridge unreachable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.Copy(w, resp.Body)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func printGUIBanner(url string) {
	fmt.Println()
	fmt.Println("================ WhatsApp MCP — Connection Wizard ================")
	fmt.Println()
	fmt.Println("⚠  RISK OF ACCOUNT BAN: this links to WhatsApp via whatsmeow, an")
	fmt.Println("   unofficial client. WhatsApp may restrict or ban accounts that")
	fmt.Println("   use it — keep usage personal and low-volume, never spam.")
	fmt.Println()
	fmt.Printf("Opening the wizard in your browser:\n  %s\n", url)
	fmt.Println("=================================================================")
}

// openBrowser opens url with the OS default browser.
func openBrowser(url string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("cmd", "/c", "start", "", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}
