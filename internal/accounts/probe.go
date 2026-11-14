package accounts

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/knaus94/agentgate/internal/config"
)

// Probes ask a provider about an account; tests replace them.
var (
	ProbeClaude = probeClaude
	ProbeCodex  = probeCodex
	usageURL    = "https://api.anthropic.com/api/oauth/usage"
	now         = func() int64 { return time.Now().Unix() }
)

func identity(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:6])
}

func parseTime(value string) int64 {
	if stamp, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return stamp.Unix()
	}
	return 0
}

// probeClaude reads the profile's OAuth token and asks the usage endpoint. The token is never refreshed here:
// the CLI owns it, and rotating it from a second place would log the profile out.
func probeClaude(account config.Account) Status {
	status := Status{Account: account}
	home := config.ExpandHome(account.Home)
	raw, err := os.ReadFile(filepath.Join(home, ".credentials.json"))
	var credentials struct {
		OAuth struct {
			AccessToken  string `json:"accessToken"`
			RefreshToken string `json:"refreshToken"`
			ExpiresAt    int64  `json:"expiresAt"`
			Plan         string `json:"subscriptionType"`
			Tier         string `json:"rateLimitTier"`
		} `json:"claudeAiOauth"`
	}
	if err != nil || json.Unmarshal(raw, &credentials) != nil || credentials.OAuth.AccessToken == "" {
		status.Note = "no subscription login in " + account.Home
		return status
	}
	status.LoggedIn = true
	status.Plan = strings.ReplaceAll(strings.TrimPrefix(strings.TrimPrefix(credentials.OAuth.Tier, "default_"), "claude_"), "_", " ")
	if status.Plan == "" {
		status.Plan = credentials.OAuth.Plan
	}
	if credentials.OAuth.RefreshToken != "" {
		status.Identity = identity("claude", credentials.OAuth.RefreshToken)
	} else {
		status.Identity = identity("claude-token", credentials.OAuth.AccessToken)
	}
	var profile struct {
		Account struct {
			UUID string `json:"accountUuid"`
		} `json:"oauthAccount"`
	}
	if raw, err := os.ReadFile(filepath.Join(home, ".claude.json")); err == nil && json.Unmarshal(raw, &profile) == nil && profile.Account.UUID != "" {
		status.Identity = identity("claude", profile.Account.UUID)
	}
	if credentials.OAuth.ExpiresAt/1000 < now()+60 {
		status.Note = "limits unknown: the token is stale until this profile is used once"
		return status
	}
	request, _ := http.NewRequest(http.MethodGet, usageURL, nil)
	request.Header.Set("Authorization", "Bearer "+credentials.OAuth.AccessToken)
	request.Header.Set("anthropic-beta", "oauth-2025-04-20")
	request.Header.Set("User-Agent", "agentgate")
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		status.Note = "limits unknown: " + err.Error()
		return status
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode != http.StatusOK {
		status.Note = "limits unknown: " + response.Status
		return status
	}
	status.Windows, status.ProbedAt = ParseClaudeUsage(body), now()
	return status
}

// probeCodex asks the Codex app server of that home for its rate limits.
func probeCodex(account config.Account) Status {
	status := Status{Account: account}
	home := config.ExpandHome(account.Home)
	raw, err := os.ReadFile(filepath.Join(home, "auth.json"))
	var auth struct {
		Mode   string `json:"auth_mode"`
		Key    string `json:"OPENAI_API_KEY"`
		Tokens *struct {
			AccountID string `json:"account_id"`
		} `json:"tokens"`
	}
	if err != nil || json.Unmarshal(raw, &auth) != nil {
		status.Note = "no login in " + account.Home
		return status
	}
	if auth.Mode == "apikey" || auth.Tokens == nil || auth.Tokens.AccountID == "" {
		if auth.Key == "" {
			status.Note = "no login in " + account.Home
			return status
		}
		status.LoggedIn, status.APIKey, status.Plan = true, true, "api key"
		status.Identity = identity("codex-key", auth.Key)
		return status
	}
	status.LoggedIn, status.Identity = true, identity("codex", auth.Tokens.AccountID)
	body, err := codexRateLimits(home)
	if err != nil {
		status.Note = "limits unknown: " + err.Error()
		return status
	}
	status.Windows, status.Plan = ParseCodexLimits(body)
	status.ProbedAt = now()
	return status
}

// Probe asks the account's provider.
func Probe(account config.Account) Status {
	if account.Provider == "claude" {
		return ProbeClaude(account)
	}
	return ProbeCodex(account)
}

// Discover finds CLI profiles in the home directory: ~/.claude, ~/.claude-*, ~/.codex, ~/.codex-*.
func Discover() []Status {
	home, _ := os.UserHomeDir()
	var out []Status
	for _, provider := range []string{"claude", "codex"} {
		paths, _ := filepath.Glob(filepath.Join(home, "."+provider+"*"))
		sort.Strings(paths)
		for _, path := range paths {
			name := strings.TrimPrefix(filepath.Base(path), ".")
			info, err := os.Stat(path)
			if err != nil || !info.IsDir() || (name != provider && !strings.HasPrefix(name, provider+"-")) {
				continue
			}
			out = append(out, Probe(config.Account{Name: name, Provider: provider, Home: "~/" + filepath.Base(path)}))
		}
	}
	return out
}
