package helpers

import (
	"bufio"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// LoadAPIKeyFromDotenv sets the API key from a nearby .env file when it isn't
// already configured (via the environment). It looks in the current directory
// and the executable's directory, walking up a few levels, so running the
// wizard from the repo "just works" without exporting WHATSAPP_API_KEY.
// Returns true if a key is available afterwards.
func LoadAPIKeyFromDotenv() bool {
	if HasAPIKey() {
		return true
	}
	if k := discoverAPIKeyFromDotenv(); k != "" {
		SetAPIKey(k)
		slog.Info("loaded WHATSAPP_API_KEY from a nearby .env file")
		return true
	}
	return false
}

func discoverAPIKeyFromDotenv() string {
	var starts []string
	if cwd, err := os.Getwd(); err == nil {
		starts = append(starts, cwd)
	}
	if exe, err := os.Executable(); err == nil {
		starts = append(starts, filepath.Dir(exe))
	}

	seen := map[string]bool{}
	for _, start := range starts {
		dir := start
		for i := 0; i < 6; i++ { // walk up a few levels toward the repo root
			if seen[dir] {
				break
			}
			seen[dir] = true
			if k := readKeyFromEnvFile(filepath.Join(dir, ".env")); k != "" {
				return k
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return ""
}

func readKeyFromEnvFile(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		if v, ok := strings.CutPrefix(line, "WHATSAPP_API_KEY="); ok {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
		if v, ok := strings.CutPrefix(line, "WHATSAPP_API_SECRET="); ok { // deprecated alias
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}
