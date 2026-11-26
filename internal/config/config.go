package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Port             string
	BindAddr         string
	RepoDir          string
	BridgeToken      string
	OpenRouterAPIKey string
	AIGatewayURL     string
	HostName         string
}

// Load reads optional .env in the working directory (without overriding existing OS env vars)
// and returns a validated Config.
func Load(dotEnvPath string) Config {
	loadDotEnv(dotEnvPath)

	hostName, _ := os.Hostname()
	if hostName == "" {
		hostName = "WynnDiff-Host"
	}

	repoDir := envOr("REPO_DIR", ".")
	if abs, err := filepath.Abs(repoDir); err == nil {
		repoDir = abs
	}

	return Config{
		Port:             envOr("PORT", "8787"),
		BindAddr:         envOr("BIND_ADDR", "0.0.0.0"),
		RepoDir:          repoDir,
		BridgeToken:      strings.TrimSpace(os.Getenv("BRIDGE_TOKEN")),
		OpenRouterAPIKey: strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY")),
		AIGatewayURL:     strings.TrimRight(envOr("AI_GATEWAY_URL", "https://openrouter.ai/api/v1"), "/"),
		HostName:         hostName,
	}
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key := strings.TrimSpace(k)
		val := strings.TrimSpace(v)
		if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'')) {
			val = val[1 : len(val)-1]
		}
		if key != "" && os.Getenv(key) == "" {
			_ = os.Setenv(key, val)
		}
	}
}
