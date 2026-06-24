package helpers

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// BridgeStatus mirrors the bridge's GET /api/auth/status response.
type BridgeStatus struct {
	Connected       bool   `json:"connected"`
	LoggedIn        bool   `json:"logged_in"`
	PairingRequired bool   `json:"pairing_required"`
	WAVersion       string `json:"wa_version"`
}

// IsInteractive reports whether stdout is attached to a real terminal.
//
// When the MCP server is spawned by an MCP host (e.g. Claude Desktop) over
// stdio, or runs inside a container, stdout is a pipe and this returns false.
// The connection wizard must never run in that case — writing to stdout there
// would corrupt the MCP stdio protocol.
func IsInteractive() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// fetchStatus queries the bridge for its connection + login state. It returns
// an error if the bridge is unreachable or the API key is missing/invalid.
func fetchStatus() (*BridgeStatus, error) {
	data, err := callAPI(http.MethodGet, "/auth/status", nil)
	if err != nil {
		return nil, err
	}
	var st BridgeStatus
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("unexpected status response: %w", err)
	}
	return &st, nil
}

// fetchPairingQRPNG retrieves the current pairing QR as PNG bytes. The returned
// status code is 200 when a QR is available, 410 when the bridge is already
// logged in or pairing has not started yet.
func fetchPairingQRPNG() ([]byte, int, error) {
	token, err := GetOrRefreshJwtToken()
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequest(http.MethodGet, apiBaseURL+"/auth/pairing-qr", nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := (&http.Client{Timeout: apiTimeout}).Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, nil
	}
	return body, resp.StatusCode, nil
}

func isHTTPMode() bool {
	v := strings.ToLower(ReadEnv("IS_HTTP", "false"))
	return v == "true" || v == "1"
}

// handleCLI processes subcommands and the interactive connection wizard. It
// returns true when InitMcpTool should stop (the run was handled here, usually
// via os.Exit), or false to continue starting the MCP server.
func handleCLI() bool {
	if len(os.Args) > 1 {
		switch strings.ToLower(os.Args[1]) {
		case "connect", "wizard", "setup", "login":
			if err := RunConnectionWizard(); err != nil {
				fmt.Fprintf(os.Stderr, "\nConnection wizard failed: %v\n", err)
				os.Exit(1)
			}
			os.Exit(0)
		case "status":
			printStatus()
			os.Exit(0)
		case "help", "-h", "--help":
			printUsage()
			os.Exit(0)
		default:
			fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
			printUsage()
			os.Exit(2)
		}
	}

	// No subcommand. If a human launched the binary directly in a terminal,
	// make sure WhatsApp is linked instead of silently starting a stdio server
	// that only an MCP host is meant to drive.
	if !isHTTPMode() && IsInteractive() {
		if st, err := fetchStatus(); err != nil || st == nil || !st.LoggedIn {
			if werr := RunConnectionWizard(); werr != nil {
				fmt.Fprintf(os.Stderr, "\nConnection wizard failed: %v\n", werr)
				os.Exit(1)
			}
		} else {
			fmt.Println("✓ WhatsApp is already linked.")
		}
		fmt.Println("\nThis binary is started by your MCP host (e.g. Claude Desktop), not run")
		fmt.Println("directly. Add it to your MCP config and restart the host. See `whatsapp-mcp help`.")
		os.Exit(0)
	}

	return false
}

func printStatus() {
	st, err := fetchStatus()
	if err != nil {
		fmt.Printf("Could not reach the bridge at %s: %v\n", apiBaseURL, err)
		return
	}
	fmt.Printf("connected=%v  logged_in=%v  pairing_required=%v  wa_version=%s\n",
		st.Connected, st.LoggedIn, st.PairingRequired, st.WAVersion)
}

func printUsage() {
	fmt.Print(`whatsapp-mcp — WhatsApp MCP server

Usage:
  whatsapp-mcp [command]

Commands:
  connect    Run the interactive connection wizard (link WhatsApp)
  status     Print the bridge connection/login status
  help       Show this help

With no command the server starts in MCP mode (stdio by default, or HTTP when
IS_HTTP=true). When launched directly in a terminal without a linked WhatsApp
account, the connection wizard runs automatically.

Environment:
  WHATSAPP_API_KEY   API key shared with the bridge (required)
  API_BASE_URL       Bridge API base URL (default http://localhost:8080/api)
  IS_HTTP            Set true to serve MCP over HTTP instead of stdio
  HTTP_BASE_URL      HTTP listen address (default 0.0.0.0:5777)
`)
}

// RunConnectionWizard guides a user through linking the bridge to WhatsApp.
// It is interactive and must only be called when IsInteractive() is true.
func RunConnectionWizard() error {
	printWizardHeader()

	if apiKey == "" {
		fmt.Println("✗ WHATSAPP_API_KEY is not set.")
		fmt.Println("  Set it to the same value the bridge uses, then run the wizard again:")
		fmt.Println("    Windows (PowerShell):  $env:WHATSAPP_API_KEY = \"<your key>\"")
		fmt.Println("    macOS/Linux:           export WHATSAPP_API_KEY=\"<your key>\"")
		return fmt.Errorf("WHATSAPP_API_KEY not set")
	}

	if !confirm("Do you understand the ban risk and want to continue?") {
		fmt.Println("Aborted. No connection was made.")
		return nil
	}

	fmt.Printf("\nUsing bridge API: %s\n", apiBaseURL)

	st, err := waitForBridge()
	if err != nil {
		return err
	}
	if st.LoggedIn {
		fmt.Println("\n✓ This bridge is already linked to WhatsApp. Nothing to do.")
		return nil
	}

	if err := runPairing(); err != nil {
		return err
	}

	printNextSteps()
	return nil
}

// waitForBridge polls the bridge until it answers, printing guidance while it
// is unreachable. Returns the first successful status read.
func waitForBridge() (*BridgeStatus, error) {
	deadline := time.Now().Add(2 * time.Minute)
	warned := false
	for time.Now().Before(deadline) {
		st, err := fetchStatus()
		if err == nil {
			return st, nil
		}

		if strings.Contains(err.Error(), "401") {
			return nil, fmt.Errorf("the bridge rejected the API key (401). " +
				"Make sure WHATSAPP_API_KEY matches the bridge's value")
		}

		if !warned {
			fmt.Println("\n…can't reach the WhatsApp bridge yet.")
			fmt.Println("  Start it first (from the repo root):  docker compose up -d")
			fmt.Printf("  Waiting for it to come up at %s …\n", apiBaseURL)
			warned = true
		}
		time.Sleep(3 * time.Second)
	}
	return nil, fmt.Errorf("gave up waiting for the bridge at %s", apiBaseURL)
}

// runPairing shows the pairing QR and polls until the bridge reports logged_in.
func runPairing() error {
	qrPath := filepath.Join(os.TempDir(), "whatsapp-pairing-qr.png")
	var lastQR []byte
	opened := false
	deadline := time.Now().Add(5 * time.Minute)

	fmt.Println("\nWaiting for the pairing QR from the bridge…")
	for time.Now().Before(deadline) {
		if st, err := fetchStatus(); err == nil && st.LoggedIn {
			fmt.Println("\n✓ Linked to WhatsApp successfully!")
			_ = os.Remove(qrPath)
			return nil
		}

		png, code, err := fetchPairingQRPNG()
		if err != nil {
			time.Sleep(3 * time.Second)
			continue
		}

		switch code {
		case http.StatusOK:
			if !bytes.Equal(png, lastQR) {
				if err := os.WriteFile(qrPath, png, 0o600); err != nil {
					return fmt.Errorf("failed to write QR image: %w", err)
				}
				lastQR = png
				if !opened {
					fmt.Printf("\nQR code saved to: %s\n", qrPath)
					if err := openFile(qrPath); err != nil {
						fmt.Println("  (couldn't auto-open it — open the file above manually)")
					} else {
						fmt.Println("  Opening it in your default image viewer…")
					}
					fmt.Println("\n  On your phone: WhatsApp → Settings → Linked Devices → Link a Device")
					fmt.Println("  (Alternatively scan from the bridge logs: docker compose logs wa-bridge)")
					opened = true
				} else {
					fmt.Println("  • QR refreshed — re-open the saved image if your viewer didn't update.")
				}
			}
		case http.StatusGone:
			// Bridge isn't in the pairing flow yet; keep polling.
		}
		time.Sleep(3 * time.Second)
	}
	return fmt.Errorf("timed out after 5 min. Re-run `whatsapp-mcp connect` to try again")
}

// openFile opens path with the OS default application.
func openFile(path string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("cmd", "/c", "start", "", path).Start()
	case "darwin":
		return exec.Command("open", path).Start()
	default:
		return exec.Command("xdg-open", path).Start()
	}
}

// confirm asks a yes/no question and returns true only on an explicit yes.
func confirm(prompt string) bool {
	fmt.Printf("%s [y/N]: ", prompt)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

func printWizardHeader() {
	fmt.Println()
	fmt.Println("================ WhatsApp MCP — Connection Wizard ================")
	fmt.Println()
	fmt.Println("⚠  IMPORTANT — RISK OF ACCOUNT BAN")
	fmt.Println("   This links to WhatsApp through whatsmeow, an UNOFFICIAL WhatsApp")
	fmt.Println("   Web client that is not approved by WhatsApp/Meta. WhatsApp may")
	fmt.Println("   flag, restrict, or PERMANENTLY BAN accounts that use unofficial")
	fmt.Println("   clients — the risk is highest with automated or bulk sending.")
	fmt.Println("   Use an account you can afford to lose, keep usage personal and")
	fmt.Println("   low-volume, and never spam.")
	fmt.Println()
	fmt.Println("=================================================================")
}

func printNextSteps() {
	exe, _ := os.Executable()
	fmt.Println("\nYou're connected. To use this from Claude Desktop, add the MCP")
	fmt.Println("server to claude_desktop_config.json:")
	fmt.Println()
	fmt.Printf(`{
  "mcpServers": {
    "whatsapp-mcp": {
      "command": %q,
      "env": {
        "WHATSAPP_API_KEY": "<same key as the bridge>",
        "API_BASE_URL": %q
      }
    }
  }
}`, exe, apiBaseURL)
	fmt.Println()
	fmt.Println("\nWindows: save that file as UTF-8 WITHOUT a BOM, then fully quit")
	fmt.Println("(tray → Quit) and reopen Claude Desktop.")
}
