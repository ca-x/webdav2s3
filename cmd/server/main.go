package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	_ "github.com/lib-x/entsqlite"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/joho/godotenv"
	"github.com/spf13/afero"
	xwebdav "golang.org/x/net/webdav"

	"github.com/example/webdav-s3/ent"
	"github.com/example/webdav-s3/internal/config"
	"github.com/example/webdav-s3/internal/s3client"
	"github.com/example/webdav-s3/internal/server"
	"github.com/example/webdav-s3/pkg/auth"
	s3fs "github.com/example/webdav-s3/pkg/s3fs"
	davadapter "github.com/example/webdav-s3/pkg/webdav"
)

// Build-time variables (set via ldflags)
var (
	Version   = "dev"
	BuildTime = "unknown"
	GitCommit = "unknown"
)

func main() {
	// Load .env file if present
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		log.Printf("warning: could not load .env file: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("configuration error: %v", err)
	}

	var handler http.Handler

	if cfg.IsDatabaseMode() {
		// Database mode - multi-backend
		handler, err = setupDatabaseMode(cfg)
	} else {
		// Legacy mode - single backend from env vars
		handler, err = setupLegacyMode(cfg)
	}

	if err != nil {
		log.Fatalf("setup error: %v", err)
	}

	// Start server
	addr := fmt.Sprintf(":%d", cfg.Port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	log.Printf("┌─────────────────────────────────────────")
	log.Printf("│  webdav2s3 server starting")
	log.Printf("│  listen:   %s", addr)
	log.Printf("│  mode:     %s", map[bool]string{true: "database (multi-backend)", false: "legacy (single-backend)"}[cfg.IsDatabaseMode()])
	if cfg.IsDatabaseMode() {
		log.Printf("│  database: %s", cfg.DatabasePath)
	} else {
		log.Printf("│  bucket:   %s", cfg.S3Bucket)
		log.Printf("│  region:   %s", cfg.S3Region)
		if cfg.S3Endpoint != "" {
			log.Printf("│  endpoint: %s", cfg.S3Endpoint)
		}
	}
	log.Printf("└─────────────────────────────────────────")

	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func setupDatabaseMode(cfg *config.Config) (http.Handler, error) {
	// Create database directory if needed
	if cfg.DatabaseURL == "" && cfg.DatabasePath != "" {
		if err := os.MkdirAll(filepath.Dir(cfg.DatabasePath), 0o755); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}

	// Open database
	db, err := ent.Open("sqlite3", cfg.DatabaseConnectionString())
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Run migrations
	ctx := context.Background()
	if err := db.Schema.Create(ctx); err != nil {
		return nil, fmt.Errorf("failed to create schema: %w", err)
	}

	if cfg.DatabaseURL != "" {
		log.Printf("Database initialized with DATABASE_URL")
	} else {
		log.Printf("Database initialized: %s", cfg.DatabasePath)
	}

	// Create S3 client pool
	pool := s3client.NewPool()

	// Setup router
	securityCfg := server.SecurityConfig{
		RateLimitPerMinute: cfg.RateLimitPerMinute,
		MaxFileSizeBytes:   cfg.MaxFileSizeBytes,
		AllowedExtensions:  cfg.AllowedExtensions,
		ReadOnly:           cfg.ReadOnly,
	}
	router, err := server.SetupRouter(db, pool, cfg.JWTSecret, securityCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to setup router: %w", err)
	}

	return router, nil
}

func setupLegacyMode(cfg *config.Config) (http.Handler, error) {
	// Build S3 client
	s3Client, err := buildS3Client(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create S3 client: %w", err)
	}

	// Build filesystem
	var baseFs afero.Fs = s3fs.New(s3Client, cfg.S3Bucket, cfg.S3KeyPrefix)
	if cfg.ReadOnly {
		baseFs = afero.NewReadOnlyFs(baseFs)
	}

	// Build WebDAV handler
	davHandler := &xwebdav.Handler{
		FileSystem: davadapter.NewAferoFS(baseFs),
		LockSystem: xwebdav.NewMemLS(),
		Logger: func(r *http.Request, err error) {
			if err != nil {
				log.Printf("WebDAV error: %s %s → %v", r.Method, r.URL.Path, err)
			}
		},
	}

	// Build authenticator
	var authenticator auth.Authenticator
	switch cfg.AuthMode {
	case "local":
		authenticator = auth.NewLocal(cfg.LocalUsername, cfg.LocalPassword)
		log.Printf("auth mode: local (user=%q)", cfg.LocalUsername)
	case "api":
		authenticator = auth.NewAPI(cfg.AuthAPIURL)
		log.Printf("auth mode: api (url=%s)", cfg.AuthAPIURL)
	}

	// Setup router
	securityCfg := server.SecurityConfig{
		RateLimitPerMinute: cfg.RateLimitPerMinute,
		MaxFileSizeBytes:   cfg.MaxFileSizeBytes,
		AllowedExtensions:  cfg.AllowedExtensions,
		ReadOnly:           cfg.ReadOnly,
	}
	router := server.SetupLegacyRouter(authenticator, davHandler, securityCfg)

	return router, nil
}

func buildS3Client(cfg *config.Config) (*s3.Client, error) {
	ctx := context.Background()
	var opts []func(*awsconfig.LoadOptions) error

	opts = append(opts, awsconfig.WithRegion(cfg.S3Region))

	if cfg.S3AccessKey != "" && cfg.S3SecretKey != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(
				cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3SessionToken,
			),
		))
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, err
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if cfg.S3Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.S3Endpoint)
		}
		o.UsePathStyle = cfg.S3ForcePathStyle
	})

	return client, nil
}
