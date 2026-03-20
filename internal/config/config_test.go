package config

import (
	"os"
	"strings"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DATABASE_PATH", "")
	t.Setenv("JWT_SECRET", "")
	t.Setenv("S3_BUCKET", "my-bucket")
	t.Setenv("S3_REGION", "")
	t.Setenv("S3_ENDPOINT", "")
	t.Setenv("S3_ACCESS_KEY", "key")
	t.Setenv("S3_SECRET_KEY", "secret")
	t.Setenv("AUTH_MODE", "local")
	t.Setenv("LOCAL_AUTH_USERNAME", "user")
	t.Setenv("LOCAL_AUTH_PASSWORD", "pass")
	t.Setenv("ALLOWED_EXTENSIONS", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want 8080", cfg.Port)
	}
	if cfg.S3Region != "us-east-1" {
		t.Errorf("S3Region = %q, want us-east-1", cfg.S3Region)
	}
	if cfg.AuthMode != "local" {
		t.Errorf("AuthMode = %q, want local", cfg.AuthMode)
	}
}

func TestLoadDatabaseURLTakesPrecedence(t *testing.T) {
	t.Setenv("DATABASE_URL", "file:./url.db?cache=shared&_fk=1")
	t.Setenv("DATABASE_PATH", "./path.db")
	t.Setenv("JWT_SECRET", "secret")
	t.Setenv("S3_BUCKET", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if !cfg.IsDatabaseMode() {
		t.Fatal("expected database mode to be enabled")
	}

	if got := cfg.DatabaseConnectionString(); got != "file:./url.db?cache=shared&_fk=1" {
		t.Fatalf("DatabaseConnectionString() = %q, want DATABASE_URL value", got)
	}
}

func TestLoadWithOnlyJWTSecretUsesDefaultDatabasePath(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DATABASE_PATH", "")
	t.Setenv("JWT_SECRET", "secret")
	t.Setenv("S3_BUCKET", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if !cfg.IsDatabaseMode() {
		t.Fatal("expected database mode to be enabled")
	}
	if cfg.DatabasePath == "" {
		t.Fatal("expected default DATABASE_PATH to be set")
	}
	if !strings.Contains(cfg.DatabaseConnectionString(), cfg.DatabasePath) {
		t.Fatalf("DatabaseConnectionString() should contain DatabasePath %q, got %q", cfg.DatabasePath, cfg.DatabaseConnectionString())
	}
}

func TestLoadWithDatabasePath(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DATABASE_PATH", "/custom/path.db")
	t.Setenv("JWT_SECRET", "secret")
	t.Setenv("S3_BUCKET", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.DatabasePath != "/custom/path.db" {
		t.Errorf("DatabasePath = %q, want /custom/path.db", cfg.DatabasePath)
	}
}

func TestAllowedExtensionsParsing(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DATABASE_PATH", "")
	t.Setenv("JWT_SECRET", "secret")
	t.Setenv("S3_BUCKET", "")
	t.Setenv("ALLOWED_EXTENSIONS", ".txt, .pdf, .doc")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	want := []string{".txt", ".pdf", ".doc"}
	if len(cfg.AllowedExtensions) != len(want) {
		t.Fatalf("len(AllowedExtensions) = %d, want %d", len(cfg.AllowedExtensions), len(want))
	}
	for i, ext := range cfg.AllowedExtensions {
		if ext != want[i] {
			t.Errorf("AllowedExtensions[%d] = %q, want %q", i, ext, want[i])
		}
	}
}

func TestAllowedExtensionsNoLeadingDot(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DATABASE_PATH", "")
	t.Setenv("JWT_SECRET", "secret")
	t.Setenv("S3_BUCKET", "")
	t.Setenv("ALLOWED_EXTENSIONS", "txt, pdf")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.AllowedExtensions[0] != ".txt" {
		t.Errorf("AllowedExtensions[0] = %q, want .txt", cfg.AllowedExtensions[0])
	}
}

func TestEnvInt(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		val     string
		want    int
		wantErr bool
	}{
		{name: "valid", key: "TEST_INT", val: "42", want: 42},
		{name: "invalid", key: "TEST_INT2", val: "notanumber", want: 0},
	}

	for _, tt := range tests {
		t.Setenv(tt.key, tt.val)
		got := envInt(tt.key, 0)
		if got != tt.want {
			t.Errorf("envInt(%q) = %d, want %d", tt.key, got, tt.want)
		}
	}
}

func TestEnvInt64(t *testing.T) {
	t.Setenv("TEST_INT64", "12345678901234")
	got := envInt64("TEST_INT64", 0)
	if got != 12345678901234 {
		t.Errorf("envInt64() = %d, want 12345678901234", got)
	}
}

func TestEnvBool(t *testing.T) {
	tests := []struct {
		name  string
		val   string
		want  bool
	}{
		{name: "true lower", val: "true", want: true},
		{name: "true upper", val: "TRUE", want: true},
		{name: "false", val: "false", want: false},
		{name: "1", val: "1", want: true},
		{name: "0", val: "0", want: false},
	}

	for _, tt := range tests {
		t.Setenv("TEST_BOOL", tt.val)
		got := envBool("TEST_BOOL", false)
		if got != tt.want {
			t.Errorf("envBool(%q) = %v, want %v", tt.val, got, tt.want)
		}
	}
}

func TestEnvStr(t *testing.T) {
	t.Setenv("TEST_STR", "hello")
	got := envStr("TEST_STR", "default")
	if got != "hello" {
		t.Errorf("envStr() = %q, want hello", got)
	}
}

func TestEnvStrFallback(t *testing.T) {
	got := envStr("NONEXISTENT", "default")
	if got != "default" {
		t.Errorf("envStr() = %q, want default", got)
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name      string
		cfg       Config
		wantErr   bool
		errSubstr string
	}{
		{
			name: "valid legacy mode",
			cfg: Config{
				LogMaxSizeMB:  100,
				S3Bucket:      "bucket",
				AuthMode:      "local",
				LocalUsername: "user",
				LocalPassword: "pass",
			},
			wantErr: false,
		},
		{
			name: "valid api auth mode",
			cfg: Config{
				LogMaxSizeMB: 100,
				S3Bucket:     "bucket",
				AuthMode:     "api",
				AuthAPIURL:   "http://auth.example.com",
			},
			wantErr: false,
		},
		{
			name: "invalid auth mode",
			cfg: Config{
				LogMaxSizeMB: 100,
				S3Bucket:     "bucket",
				AuthMode:     "unknown",
			},
			wantErr:   true,
			errSubstr: "AUTH_MODE must be",
		},
		{
			name: "local auth missing username",
			cfg: Config{
				LogMaxSizeMB:  100,
				S3Bucket:      "bucket",
				AuthMode:      "local",
				LocalPassword: "pass",
			},
			wantErr:   true,
			errSubstr: "LOCAL_AUTH_USERNAME",
		},
		{
			name: "api auth missing url",
			cfg: Config{
				LogMaxSizeMB: 100,
				S3Bucket:     "bucket",
				AuthMode:     "api",
			},
			wantErr:   true,
			errSubstr: "AUTH_API_URL",
		},
		{
			name: "missing s3 bucket",
			cfg: Config{
				LogMaxSizeMB: 100,
				S3Bucket:     "",
				AuthMode:     "local",
			},
			wantErr:   true,
			errSubstr: "S3_BUCKET",
		},
		{
			name: "database mode with jwt secret",
			cfg: Config{
				LogMaxSizeMB: 100,
				JWTSecret:    "secret",
			},
			wantErr: false,
		},
		{
			name: "database mode missing jwt",
			cfg: Config{
				LogMaxSizeMB: 100,
				DatabasePath:  "/path/to/db",
				JWTSecret:    "",
			},
			wantErr:   true,
			errSubstr: "JWT_SECRET",
		},
		{
			name: "invalid log max size",
			cfg: Config{
				LogMaxSizeMB:  0,
				S3Bucket:      "bucket",
				AuthMode:      "local",
				LocalUsername: "user",
				LocalPassword: "pass",
			},
			wantErr:   true,
			errSubstr: "LOG_MAX_SIZE_MB",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.errSubstr != "" && err != nil && !strings.Contains(err.Error(), tt.errSubstr) {
				t.Errorf("validate() error = %v, want containing %q", err, tt.errSubstr)
			}
		})
	}
}

func TestIsDatabaseMode(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		want    bool
	}{
		{name: "with database url", cfg: Config{DatabaseURL: "file:db.db"}, want: true},
		{name: "with database path", cfg: Config{DatabasePath: "db.db"}, want: true},
		{name: "with jwt secret", cfg: Config{JWTSecret: "secret"}, want: true},
		{name: "legacy s3 mode", cfg: Config{S3Bucket: "bucket"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.IsDatabaseMode(); got != tt.want {
				t.Errorf("IsDatabaseMode() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDatabaseConnectionString(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{
			name: "with database url",
			cfg:  Config{DatabaseURL: "file:custom.db?cache=shared"},
			want: "file:custom.db?cache=shared",
		},
		{
			name: "with database path",
			cfg:  Config{DatabasePath: "/path/to/db.sqlite"},
			want: "file:/path/to/db.sqlite?cache=shared&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(10000)",
		},
		{
			name: "empty",
			cfg:  Config{},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.DatabaseConnectionString(); got != tt.want {
				t.Errorf("DatabaseConnectionString() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMain(m *testing.M) {
	// Clean up any environment variables set by tests
	envVars := []string{
		"DATABASE_URL", "DATABASE_PATH", "JWT_SECRET", "S3_BUCKET",
		"S3_REGION", "S3_ENDPOINT", "S3_ACCESS_KEY", "S3_SECRET_KEY",
		"S3_SESSION_TOKEN", "S3_FORCE_PATH_STYLE", "S3_KEY_PREFIX",
		"AUTH_MODE", "LOCAL_AUTH_USERNAME", "LOCAL_AUTH_PASSWORD",
		"AUTH_API_URL", "MAX_FILE_SIZE_BYTES", "RATE_LIMIT_PER_MINUTE",
		"READ_ONLY", "ALLOWED_EXTENSIONS", "LOG_FILE_PATH",
		"LOG_STDOUT", "LOG_MAX_SIZE_MB", "LOG_MAX_BACKUPS",
		"LOG_MAX_AGE_DAYS", "LOG_COMPRESS",
	}
	for _, v := range envVars {
		os.Unsetenv(v)
	}
	os.Exit(m.Run())
}
