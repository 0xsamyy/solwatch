package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Config holds all runtime configuration for the service.
type Config struct {
	// Required
	TelegramBotToken    string
	TelegramAdminChatID int64
	HeliusWSS           string

	// Optional (with defaults)
	DBPath     string // default: "solwatch.db"
	Commitment string // default: "processed" (fastest)

	// Debug helpers (not strictly required, but nice to have)
	// LogLevel could be: "debug", "info", "warn", "error" (default: "info")
	LogLevel string
}

// allowedCommitments is kept small and explicit to avoid surprises.
var allowedCommitments = map[string]struct{}{
	"processed":  {},
	"confirmed":  {},
	"finalized":  {},
}

// Load reads environment variables, applies defaults, validates,
// and returns a Config instance. It attempts to load .env if present.
func Load() (Config, error) {
	// Load .env if it exists; ignore if missing.
	_ = godotenv.Load()

	var cfg Config
	var errs []string

	// Required: TELEGRAM_BOT_TOKEN
	cfg.TelegramBotToken = strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN"))
	if cfg.TelegramBotToken == "" {
		errs = append(errs, "TELEGRAM_BOT_TOKEN is required (get it from @BotFather)")
	}

	// Required: TELEGRAM_ADMIN_CHAT_ID (must be a valid int64)
	adminStr := strings.TrimSpace(os.Getenv("TELEGRAM_ADMIN_CHAT_ID"))
	if adminStr == "" {
		errs = append(errs, "TELEGRAM_ADMIN_CHAT_ID is required (your numeric chat id)")
	} else {
		id, err := strconv.ParseInt(adminStr, 10, 64)
		if err != nil || id == 0 {
			errs = append(errs, fmt.Sprintf("TELEGRAM_ADMIN_CHAT_ID must be a valid integer, got %q", adminStr))
		} else {
			cfg.TelegramAdminChatID = id
		}
	}

	// Required: HELIUS_WSS (must start with wss://)
	cfg.HeliusWSS = strings.TrimSpace(os.Getenv("HELIUS_WSS"))
	if cfg.HeliusWSS == "" {
		errs = append(errs, "HELIUS_WSS is required (your Helius WebSocket RPC URL, incl. api key)")
	} else if !strings.HasPrefix(strings.ToLower(cfg.HeliusWSS), "wss://") {
		errs = append(errs, fmt.Sprintf("HELIUS_WSS must start with wss://, got %q", cfg.HeliusWSS))
	}

	// Optional: DB_PATH (default: solwatch.db)
	cfg.DBPath = strings.TrimSpace(os.Getenv("DB_PATH"))
	if cfg.DBPath == "" {
		cfg.DBPath = "solwatch.db"
	}

	// Optional: COMMITMENT (default: processed; normalize to lowercase)
	commitment := strings.TrimSpace(os.Getenv("COMMITMENT"))
	if commitment == "" {
		commitment = "processed" // fastest, fits your use-case
	}
	commitment = strings.ToLower(commitment)
	if _, ok := allowedCommitments[commitment]; !ok {
		errs = append(errs, fmt.Sprintf("COMMITMENT must be one of processed|confirmed|finalized, got %q", commitment))
	} else {
		cfg.Commitment = commitment
	}

	// Optional: LOG_LEVEL (default: info)
	logLevel := strings.TrimSpace(strings.ToLower(os.Getenv("LOG_LEVEL")))
	switch logLevel {
	case "", "info", "debug", "warn", "error":
		// OK (empty becomes "info")
	default:
		errs = append(errs, fmt.Sprintf("LOG_LEVEL must be one of debug|info|warn|error, got %q", logLevel))
	}
	if logLevel == "" {
		logLevel = "info"
	}
	cfg.LogLevel = logLevel

	if len(errs) > 0 {
		return Config{}, errors.New("config validation error:\n  - " + strings.Join(errs, "\n  - "))
	}

	return cfg, nil
}

// MustLoad is a convenience for main(): exit fast with a readable error.
func MustLoad() Config {
	cfg, err := Load()
	if err != nil {
		// Print a clean error (no stack trace) so non-Go users can fix env quickly.
		fmt.Fprintf(os.Stderr, "\nFATAL: %v\n\n", err)
		os.Exit(1)
	}
	return cfg
}

// RedactedSummary returns a safe human-readable snapshot of the config.
// Useful to log at startup for quick debugging without leaking secrets.
func (c Config) RedactedSummary() string {
	return fmt.Sprintf(
		"config{ commitment=%s, db=%s, helius_wss=%s, telegram_bot_token=%s, admin_chat_id=%d, log_level=%s }",
		c.Commitment,
		c.DBPath,
		redactURL(c.HeliusWSS),
		redactToken(c.TelegramBotToken),
		c.TelegramAdminChatID,
		c.LogLevel,
	)
}

func redactToken(tok string) string {
	// Keep only first 6 chars if long, else "***"
	if len(tok) > 6 {
		return tok[:6] + "...(redacted)"
	}
	if tok == "" {
		return "(empty)"
	}
	return "***"
}

func redactURL(u string) string {
	// If the URL contains an API key as query, hide it crudely.
	// e.g., wss://.../?api-key=abcdef -> wss://.../?api-key=*** (redacted)
	parts := strings.Split(u, "api-key=")
	if len(parts) < 2 {
		return u
	}
	// Cut at next delimiter if any
	tail := parts[1]
	if i := strings.IndexAny(tail, "&;"); i >= 0 {
		tail = tail[:i]
	}
	return strings.Replace(u, "api-key="+tail, "api-key=***", 1)
}
