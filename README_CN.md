# WebDAV2S3 使用文档

## 项目简介

WebDAV2S3 是一个基于 S3 兼容存储的 WebDAV 服务器，支持多后端配置和 Web 管理界面。它允许你通过标准的 WebDAV 协议访问 S3 存储，同时提供一个友好的 Web 界面来管理多个 S3 后端和用户。

## 功能特性

### 核心功能

- **多后端支持** - 配置多个 S3 兼容存储后端（AWS S3、MinIO、Cloudflare R2 等）
- **写入复制** - 同一挂载路径可配置多个后端，写入时自动复制到所有后端
- **Web 管理界面** - 基于 htmx + Alpine.js 的现代化管理界面
- **完整 WebDAV 支持** - PROPFIND、GET、PUT、DELETE、MKCOL、COPY、MOVE、LOCK/UNLOCK
- **灵活认证** - WebDAV 使用 Basic Auth，API 使用 JWT，用户存储在数据库中
- **安全防护** - 速率限制、路径遍历保护、文件大小限制、扩展名过滤

### 运行模式

| 模式 | 配置方式 | 特点 |
|------|----------|------|
| 传统模式 | S3_* 环境变量 | 单后端，无需数据库 |
| 数据库模式 | DATABASE_URL | 多后端，Web UI，用户管理 |

## 快速开始

### 使用 Docker（推荐）

```bash
# 创建配置文件
cat > docker-compose.yml << 'EOF'
version: '3.8'
services:
  webdav2s3:
    image: czyt/webdav2s3:latest
    ports:
      - "8080:8080"
    environment:
      - DATABASE_PATH=/app/data/data.db
      - JWT_SECRET=your-secret-key-change-me
    volumes:
      - ./data:/app/data
    restart: unless-stopped
EOF

# 启动服务
docker compose up -d

# 访问管理界面
open http://localhost:8080/admin/
```

### 从源码构建

```bash
# 克隆仓库
git clone https://github.com/czyt/webdav2s3.git
cd webdav2s3

# 安装依赖
go mod download

# 运行
DATABASE_PATH=./data.db JWT_SECRET=secret go run ./cmd/server
```

## 配置说明

### 环境变量

#### 数据库模式（推荐）

| 变量 | 必需 | 默认值 | 说明 |
|------|------|--------|------|
| `DATABASE_PATH` | ✅ | - | SQLite 数据库文件路径 |
| `JWT_SECRET` | ✅ | - | JWT 签名密钥（至少 32 字符） |
| `PORT` | | 8080 | 监听端口 |
| `LOG_LEVEL` | | info | 日志级别 |

#### 传统模式（单后端）

| 变量 | 必需 | 默认值 | 说明 |
|------|------|--------|------|
| `S3_BUCKET` | ✅ | - | S3 存储桶名称 |
| `S3_REGION` | | us-east-1 | AWS 区域 |
| `S3_ENDPOINT` | | - | 自定义端点（MinIO、R2） |
| `S3_FORCE_PATH_STYLE` | | false | 使用路径风格 URL |
| `S3_ACCESS_KEY` | | - | 访问密钥 |
| `S3_SECRET_KEY` | | - | 密钥 |
| `S3_KEY_PREFIX` | | - | 键前缀 |
| `AUTH_MODE` | ✅ | local | 认证模式：local 或 api |
| `LOCAL_AUTH_USERNAME` | if local | - | 用户名 |
| `LOCAL_AUTH_PASSWORD` | if local | - | 密码 |
| `READ_ONLY` | | false | 只读模式 |

## Web 管理界面

### 访问地址

| 路径 | 说明 |
|------|------|
| `/admin/` | 仪表盘 |
| `/admin/login` | 登录页面 |
| `/admin/backends` | 后端管理 |
| `/admin/backends/new` | 添加后端 |
| `/admin/backends/{id}` | 编辑后端 |
| `/admin/users` | 用户管理（管理员） |

### 首次使用

1. 启动服务后访问 `http://localhost:8080/admin/`
2. 系统会自动创建默认管理员账户：
   - 用户名：`admin`
   - 密码：`admin123`
3. **请立即修改默认密码！**

### 后端配置

每个 S3 后端包含以下配置项：

| 字段 | 说明 |
|------|------|
| 名称 | 后端标识名称 |
| 挂载路径 | WebDAV 访问路径（如 `/minio`） |
| 端点 | S3 API 端点（留空使用 AWS S3） |
| 区域 | AWS 区域 |
| 存储桶 | S3 存储桶名称 |
| Access Key | 访问密钥 |
| Secret Key | 密钥 |
| Session Token | 会话令牌（可选） |
| 键前缀 | 对象键前缀（可选） |
| 路径风格 | 适用于 MinIO/R2 |
| 主后端 | 读取时优先使用 |
| 启用 | 是否启用 |
| 只读 | 禁止写入操作 |

## 写入复制功能

### 概述

写入复制允许将同一挂载路径映射到多个 S3 后端，实现数据冗余和备份：

- **写入**：同时写入所有非只读后端
- **读取**：从主后端（Primary）读取，若无可从其他后端读取
- **删除**：从所有非只读后端删除

### 配置示例

假设你有两个 MinIO 服务器，需要将文件同时写入两个服务器：

1. 添加第一个后端：
   - 名称：`minio-primary`
   - 挂载路径：`/backup`
   - 主后端：✅ 勾选

2. 添加第二个后端：
   - 名称：`minio-secondary`
   - 挂载路径：`/backup`（与第一个相同）
   - 主后端：不勾选

现在通过 WebDAV 写入 `/mounts/backup/` 的文件会自动复制到两个后端。

### 只读后端

勾选"只读"选项的后端不会被写入，适合用于：
- 归档存储
- 冷备份
- 只读共享

## API 接口

### 认证

所有 API 请求需要 JWT 认证：

```bash
# 登录获取 Token
curl -X POST http://localhost:8080/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"admin123"}'

# 响应
{
  "token": "eyJhbGciOiJIUzI1NiIs...",
  "expires_at": 1234567890,
  "user": {"id": 1, "username": "admin", "role": "admin"}
}

# 使用 Token
curl -H "Authorization: Bearer eyJhbGciOiJIUzI1NiIs..." \
  http://localhost:8080/api/v1/backends
```

### 端点列表

#### 认证

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/v1/auth/login` | 登录获取 JWT |
| GET | `/api/v1/auth/me` | 获取当前用户信息 |

#### 后端管理

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/v1/backends` | 列出所有后端 |
| POST | `/api/v1/backends` | 创建后端 |
| GET | `/api/v1/backends/{id}` | 获取后端详情 |
| PUT | `/api/v1/backends/{id}` | 更新后端 |
| DELETE | `/api/v1/backends/{id}` | 删除后端 |
| POST | `/api/v1/backends/{id}/test` | 测试连接 |
| GET | `/api/v1/mount-paths` | 获取挂载路径列表 |

#### 用户管理（管理员）

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/v1/users` | 列出所有用户 |
| POST | `/api/v1/users` | 创建用户 |
| PUT | `/api/v1/users/{id}` | 更新用户 |
| DELETE | `/api/v1/users/{id}` | 删除用户 |

## WebDAV 使用

### 连接信息

| 模式 | WebDAV 地址 |
|------|-------------|
| 传统模式 | `http://host:8080/` |
| 数据库模式 | `http://host:8080/mounts/{挂载路径}/` |

### 客户端示例

#### curl

```bash
# 上传文件
curl -u username:password -T myfile.txt http://localhost:8080/mounts/minio/myfile.txt

# 列出目录
curl -u username:password -X PROPFIND http://localhost:8080/mounts/minio/ -H "Depth: 1"

# 创建目录
curl -u username:password -X MKCOL http://localhost:8080/mounts/minio/myfolder/

# 删除文件
curl -u username:password -X DELETE http://localhost:8080/mounts/minio/myfile.txt

# 移动/重命名
curl -u username:password -X MOVE http://localhost:8080/mounts/minio/old.txt \
  -H "Destination: http://localhost:8080/mounts/minio/new.txt"
```

#### macOS Finder

1. 打开 Finder
2. 按 Cmd+K 或菜单「前往」→「连接服务器」
3. 输入地址：`http://localhost:8080/mounts/minio/`
4. 输入用户名和密码

#### Windows

1. 打开「此电脑」
2. 点击「映射网络驱动器」
3. 输入地址：`http://localhost:8080/mounts/minio/`
4. 输入凭据

#### Linux (davfs2)

```bash
# 安装 davfs2
sudo apt install davfs2

# 挂载
sudo mount -t davfs http://localhost:8080/mounts/minio/ /mnt/webdav \
  -o username=user,password=pass
```

## 架构设计

```
┌─────────────────────────────────────────────────────────────┐
│                        HTTP Router (chi)                    │
├─────────────────────────────────────────────────────────────┤
│  /admin/*      │  /api/v1/*      │  /mounts/*              │
│  Web UI        │  REST API       │  WebDAV                 │
│  (htmx)        │  (JWT auth)     │  (Basic auth)           │
├────────────────┼─────────────────┼─────────────────────────┤
│                │                 │  MountFs                │
│                │                 │  (路径路由 + 复制)        │
├────────────────┴─────────────────┴─────────────────────────┤
│                      S3 Client Pool                        │
├─────────────────────────────────────────────────────────────┤
│                      ent ORM (SQLite)                      │
└─────────────────────────────────────────────────────────────┘
```

### 核心组件

| 组件 | 说明 |
|------|------|
| MountFs | 多后端挂载文件系统，支持路径路由和写入复制 |
| S3 Client Pool | S3 客户端连接池，复用连接提高性能 |
| ent ORM | 数据库 ORM，管理用户和后端配置 |

## 数据目录结构

```
/app/data/
├── data.db          # SQLite 数据库
├── data.db-wal      # WAL 日志文件
└── data.db-shm      # 共享内存文件
```

## 常见问题

### Q: 如何修改管理员密码？

在用户管理页面点击编辑按钮，输入新密码即可。

### Q: 忘记密码怎么办？

直接修改数据库：

```bash
# 进入容器
docker exec -it webdav2s3 sh

# 使用 sqlite3 修改密码（密码为 newpassword）
sqlite3 /app/data/data.db "UPDATE users SET password_hash='\$2a\$10\$...' WHERE username='admin';"
```

或者删除数据库重新启动，会创建默认账户。

### Q: 支持哪些 S3 兼容存储？

理论上所有 S3 兼容的存储都支持：
- AWS S3
- MinIO
- Cloudflare R2
- Alibaba Cloud OSS（兼容 S3 API）
- Tencent Cloud COS（兼容 S3 API）
- Ceph RADOS Gateway
- 自建 MinIO

### Q: 为什么 MinIO/R2 需要勾选"路径风格"？

AWS S3 使用虚拟主机风格 URL（`bucket.s3.amazonaws.com`），而 MinIO 和 R2 通常使用路径风格（`endpoint/bucket`）。勾选此选项可正确访问这些服务。

### Q: 如何备份数据？

```bash
# 备份数据库
cp /app/data/data.db /backup/data-$(date +%Y%m%d).db

# 或使用 SQLite 在线备份
sqlite3 /app/data/data.db ".backup '/backup/data.db'"
```

## 许可证

MIT License