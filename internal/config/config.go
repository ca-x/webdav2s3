package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config holds all application configuration loaded from environment variables.
type Config struct {
	// Server
	Port int

	// Database (optional - for multi-backend mode)
	DatabasePath string // Path to database file

	// JWT settings for API auth
	JWTSecret string

	// S3 (legacy mode - single backend)
	S3Bucket         string
	S3Region         string
	S3Endpoint       string
	S3AccessKey      string
	S3SecretKey      string
	S3SessionToken   string
	S3ForcePathStyle bool
	S3KeyPrefix      string

	// Auth
	AuthMode string // "local" | "api"

	// Local auth
	LocalUsername string
	LocalPassword string

	// API auth
	AuthAPIURL string

	// Security
	MaxFileSizeBytes   int64
	AllowedExtensions  []string
	RateLimitPerMinute int
	ReadOnly           bool
}

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	// Default database path in user's home directory
	home, _ := os.UserHomeDir()
	dataDir := filepath.Join(home, ".webdav2s3")
	_ = os.MkdirAll(dataDir, 0o755)

	defaultDBPath := filepath.Join(dataDir, "webdav.db")

	c := &Config{
		Port:               envInt("PORT", 8080),
		DatabasePath:       envStr("DATABASE_PATH", ""),
		JWTSecret:          envStr("JWT_SECRET", ""),
		S3Bucket:           os.Getenv("S3_BUCKET"),
		S3Region:           envStr("S3_REGION", "us-east-1"),
		S3Endpoint:         os.Getenv("S3_ENDPOINT"),
		S3AccessKey:        os.Getenv("S3_ACCESS_KEY"),
		S3SecretKey:        os.Getenv("S3_SECRET_KEY"),
		S3SessionToken:     os.Getenv("S3_SESSION_TOKEN"),
		S3ForcePathStyle:   envBool("S3_FORCE_PATH_STYLE", false),
		S3KeyPrefix:        os.Getenv("S3_KEY_PREFIX"),
		AuthMode:           envStr("AUTH_MODE", "local"),
		LocalUsername:      os.Getenv("LOCAL_AUTH_USERNAME"),
		LocalPassword:      os.Getenv("LOCAL_AUTH_PASSWORD"),
		AuthAPIURL:         os.Getenv("AUTH_API_URL"),
		MaxFileSizeBytes:   envInt64("MAX_FILE_SIZE_BYTES", 100*1024*1024),
		RateLimitPerMinute: envInt("RATE_LIMIT_PER_MINUTE", 100),
		ReadOnly:           envBool("READ_ONLY", false),
	}

	if exts := os.Getenv("ALLOWED_EXTENSIONS"); exts != "" {
		for _, e := range strings.Split(exts, ",") {
			e = strings.TrimSpace(strings.ToLower(e))
			if e != "" && !strings.HasPrefix(e, ".") {
				e = "." + e
			}
			c.AllowedExtensions = append(c.AllowedExtensions, e)
		}
	}

	// Set default database path if in database mode but no path specified
	if c.IsDatabaseMode() && c.DatabasePath == "" {
		c.DatabasePath = defaultDBPath
	}

	if err := c.validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// IsDatabaseMode returns true if database mode is enabled.
func (c *Config) IsDatabaseMode() bool {
	return c.DatabasePath != "" || c.JWTSecret != ""
}

// DatabaseConnectionString returns the SQLite connection string.
func (c *Config) DatabaseConnectionString() string {
	if c.DatabasePath == "" {
		return ""
	}
	return fmt.Sprintf("file:%s?cache=shared&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(10000)", c.DatabasePath)
}

func (c *Config) validate() error {
	// In database mode, only require JWT_SECRET
	if c.IsDatabaseMode() {
		if c.JWTSecret == "" {
			return fmt.Errorf("JWT_SECRET is required in database mode")
		}
		return nil
	}

	// Legacy mode: require S3 config and auth
	if c.S3Bucket == "" {
		return fmt.Errorf("S3_BUCKET is required")
	}
	switch c.AuthMode {
	case "local":
		if c.LocalUsername == "" || c.LocalPassword == "" {
			return fmt.Errorf("LOCAL_AUTH_USERNAME and LOCAL_AUTH_PASSWORD are required when AUTH_MODE=local")
		}
	case "api":
		if c.AuthAPIURL == "" {
			return fmt.Errorf("AUTH_API_URL is required when AUTH_MODE=api")
		}
	default:
		return fmt.Errorf("AUTH_MODE must be 'local' or 'api', got %q", c.AuthMode)
	}
	return nil
}

func envStr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func envInt64(key string, fallback int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		b, err := strconv.ParseBool(v)
		if err == nil {
			return b
		}
	}
	return fallback
}