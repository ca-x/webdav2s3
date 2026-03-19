# webdav2s3

A WebDAV server backed by S3-compatible storage with multi-backend support and a web management UI.

**[中文文档 (Chinese Documentation)](README_CN.md)**

## Features

- **Multi-backend support** - Configure multiple S3 backends (AWS S3, MinIO, Cloudflare R2, etc.)
- **Web management UI** - Browser-based admin interface with htmx + Alpine.js
- **Dual mode operation** - Legacy single-backend (env vars) or database-backed multi-backend
- **Full WebDAV support** - PROPFIND, GET, PUT, DELETE, MKCOL, COPY, MOVE, LOCK/UNLOCK
- **Authentication** - Basic auth for WebDAV, JWT for API, database-backed users
- **Security** - Rate limiting, path traversal protection, file size limits, extension filtering

## Modes of Operation

### Legacy Mode (Single Backend)
Set S3_* environment variables. No database required.
- WebDAV endpoint: `/*` (root)
- Auth: Local credentials or external API

### Database Mode (Multi-Backend)
Set `DATABASE_URL` (or `DATABASE_PATH`) and `JWT_SECRET`.
- Web UI: `/admin/*`
- REST API: `/api/v1/*`
- WebDAV: `/mounts/{mount_path}/*`
- Auth: Database users with bcrypt passwords

## Endpoints

| Path | Description |
|------|-------------|
| `/health` | Health check |
| `/admin/` | Dashboard (database mode) |
| `/admin/setup` | First-run setup wizard |
| `/admin/backends` | Backend management |
| `/admin/users` | User management (admin only) |
| `/api/v1/setup/status` | Setup status |
| `/api/v1/setup/init` | Initialize first admin user |
| `/api/v1/auth/login` | JWT login |
| `/api/v1/backends` | Backend CRUD API |
| `/api/v1/users` | User CRUD API (admin) |
| `/mounts/*` | WebDAV (database mode) |
| `/*` | WebDAV (legacy mode) |

## Configuration

### Legacy Mode Environment Variables

| Variable | Required | Default | Description |
|---|---|---|---|
| `S3_BUCKET` | ✅ | – | S3 bucket name |
| `S3_REGION` | | `us-east-1` | AWS region |
| `S3_ENDPOINT` | | – | Custom endpoint (MinIO, R2) |
| `S3_FORCE_PATH_STYLE` | | `false` | Path-style URLs |
| `S3_ACCESS_KEY` | | – | Access key |
| `S3_SECRET_KEY` | | – | Secret key |
| `S3_KEY_PREFIX` | | – | Key prefix |
| `AUTH_MODE` | ✅ | `local` | `local` or `api` |
| `LOCAL_AUTH_USERNAME` | if local | – | Username |
| `LOCAL_AUTH_PASSWORD` | if local | – | Password |
| `AUTH_API_URL` | if api | – | Auth API endpoint |
| `READ_ONLY` | | `false` | Block writes |

### Database Mode Environment Variables

| Variable | Required | Description |
|---|---|---|
| `DATABASE_URL` | ✅* | SQLite connection string (takes precedence) |
| `DATABASE_PATH` | ✅* | SQLite file path |
| `JWT_SECRET` | ✅ | JWT signing secret |
| `PORT` | | Listen port (default 8080) |

\* `DATABASE_URL` or `DATABASE_PATH`, at least one is required for database mode.

## Quick Start

### Database Mode (Multi-Backend)

```bash
# Set environment
export DATABASE_URL="file:./data.db?cache=shared&_fk=1"
export JWT_SECRET="your-secret-key"

# Run server
go run ./cmd/server

# Open browser to http://localhost:8080/admin/
# On first start, complete setup at /admin/setup to create the first admin user
```

### Legacy Mode (Single Backend) with MinIO

```bash
docker compose up
```

Connect WebDAV client to `http://localhost:8080` with `admin` / `changeme`.

### Test with curl

```bash
# Upload a file
curl -u admin:changeme -T myfile.txt http://localhost:8080/myfile.txt

# List directory (WebDAV PROPFIND)
curl -u admin:changeme -X PROPFIND http://localhost:8080/ -H "Depth: 1"

# Create directory
curl -u admin:changeme -X MKCOL http://localhost:8080/myfolder/
```

### Database Mode WebDAV

```bash
# After creating a backend with mount_path=/minio via web UI
curl -u user:password -X PROPFIND http://localhost:8080/mounts/minio/ -H "Depth: 1"
```

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                        HTTP Router (chi)                    │
├─────────────────────────────────────────────────────────────┤
│  /admin/*      │  /api/v1/*      │  /mounts/*              │
│  Web UI        │  REST API       │  WebDAV                 │
│  (htmx)        │  (JWT auth)     │  (Basic auth)           │
├────────────────┼─────────────────┼─────────────────────────┤
│                │                 │  MountFs                │
│                │                 │  (path-based routing)   │
├────────────────┴─────────────────┴─────────────────────────┤
│                      S3 Client Pool                        │
├─────────────────────────────────────────────────────────────┤
│                      ent ORM (SQLite)                      │
└─────────────────────────────────────────────────────────────┘
```

## Project Structure

```
webdav2s3/
├── cmd/server/main.go       # Entry point
├── ent/                     # Database schema & generated code
├── internal/
│   ├── config/              # Configuration loading
│   ├── server/              # HTTP router setup
│   ├── api/handlers/        # REST API handlers
│   ├── web/                 # Web UI handlers & templates
│   ├── s3client/            # S3 client pool
│   └── webdav/              # Multi-backend mount filesystem
├── pkg/
│   ├── auth/                # Authentication providers
│   ├── s3fs/                # S3 filesystem implementation
│   ├── middleware/          # HTTP middleware
│   └── webdav/              # afero-to-webdav adapter
└── web/templates/           # HTML templates
```

## License

MIT
