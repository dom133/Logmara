# SysLog GUI

Web-based syslog monitoring, parsing, and visualization platform with Docker Compose deployment.

## Architecture

```
┌──────────┐     ┌──────────┐     ┌──────────┐
│  Client   │────▶│ Nginx    │────▶│ Go API   │
│  (Browser)│     │  (:80)   │     │  (:8080) │
└──────────┘     └──────────┘     └────┬─────┘
                                       │
                    ┌──────────┐       │       ┌──────────────┐
                    │ rsyslog  │       │       │   PostgreSQL   │
                    │  (:514)  │───────┘       │    (:5432)     │
                    └──────────┘               └──────────────┘
```

| Service | Port | Description |
|---------|------|-------------|
| Frontend | 80 | React SPA served via Nginx |
| API | 8080 | Gin REST API with JWT auth |
| rsyslog | 514 (TCP/UDP) | Syslog ingestion daemon |
| PostgreSQL | 5432 | Persistent log storage |

## Quick Start

```bash
# Clone and start all services
git clone https://gitlab.dom133.xyz/dominik.kruszewski/syslog_gui.git
cd syslog_gui
docker compose up --build

# First launch: complete the Setup Wizard at http://localhost
# Subsequent launches: login with configured credentials
```

## Configuration

Copy `.env.example` and adjust values:

| Variable | Default | Description |
|----------|---------|-------------|
| `DATABASE_URL` | *(required)* | PostgreSQL connection string — **mandatory in production** |
| `JWT_SECRET` | *(auto-generated)* | JWT signing key — set explicitly in production |
| `ENCRYPTION_KEY` | *(auto-generated)* | AES-256 key for encrypting sensitive settings |
| `PORT` | `8080` | API server port |
| `LOG_FILE_PATH` | `/data/logs.jsonl` | Path to rsyslog JSON output |
| `CORS_ORIGINS` | *(none)* | Comma-separated allowed origins for CORS |
| `LDAP_SERVER` | *(none)* | LDAP/AD server hostname |
| `LDAP_PORT` | `636` | LDAP server port |
| `LDAP_USE_TLS` | `true` | Enable TLS for LDAP |
| `LDAP_VERIFY_CERT` | `true` | Verify LDAP server certificate |
| `LDAP_CA_CERT` | *(none)* | Path to CA certificate for LDAP |
| `LDAP_BASE_DN` | *(none)* | Base DN for user search |
| `LDAP_BIND_DN` | *(none)* | Bind DN for LDAP queries |
| `LDAP_BIND_PASSWORD` | *(none)* | Bind password for LDAP queries |

## Features

- **Live Log Viewer** — Browse, filter, and search ingested syslog messages in real-time
- **Parser Engine** — Define regex-based parsers to extract structured fields from raw log lines
- **Custom Dashboards** — Create dashboards filtered by device, severity, or parsed fields
- **Pin Dashboards** — Pin frequently-used dashboards to the sidebar for quick access
- **Export** — Download logs as CSV or HTML reports
- **Statistics** — Timeline charts, severity breakdown, and per-device metrics
- **Secure Authentication** — JWT access tokens (15 min) + refresh tokens (7 days) with rotation
- **LDAP/AD Integration** — Authenticate against Active Directory or OpenLDAP with TLS support
- **Audit Logging** — Track login attempts, password changes, and admin actions
- **Rate Limiting** — Login endpoint protected against brute-force attacks
- **CORS Protection** — Configurable allowed origins
- **Setup Wizard** — Guided initial configuration with admin account, database, security keys, and optional LDAP/CORS
- **Admin Panel** — User management, settings, audit log viewer, LDAP connection test

## API Endpoints

### Authentication

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/auth/login` | Authenticate and receive JWT + refresh token |
| POST | `/api/auth/refresh` | Refresh access token using refresh token |
| POST | `/api/auth/logout` | Invalidate refresh token |
| GET | `/api/auth/me` | Get current user profile |
| POST | `/api/auth/change-password` | Change user password |

### Initialization

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/status/initialized` | Check if app is initialized |
| GET | `/api/init/generate-keys` | Generate random JWT secret and encryption key |
| GET | `/api/init/db-config` | Get current database configuration |
| POST | `/api/init` | Initialize app with admin, DB, keys, LDAP, CORS |

### Logs

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/logs` | Query logs with filters |
| GET | `/api/export/csv` | Export logs as CSV |
| GET | `/api/export/html` | Export logs as HTML report |

### Statistics

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/stats/dashboard` | Overview statistics |
| GET | `/api/stats/devices` | Per-device counts |
| GET | `/api/stats/severity` | Severity distribution |
| GET | `/api/stats/timeline` | Hourly timeline data |

### Parsers

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/parsers` | List all parsers |
| POST | `/api/parsers` | Create new parser |
| PUT | `/api/parsers/:id` | Update parser |
| DELETE | `/api/parsers/:id` | Delete parser |
| POST | `/api/parsers/test` | Test regex against sample |
| POST | `/api/parsers/reparse` | Re-parse unparsed logs |
| GET | `/api/parsers/fields` | List extracted field names |

### Dashboards

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/dashboards` | List dashboards (pinned first) |
| POST | `/api/dashboards` | Create dashboard |
| GET | `/api/dashboards/:id` | Get dashboard details |
| PUT | `/api/dashboards/:id` | Update dashboard |
| DELETE | `/api/dashboards/:id` | Delete dashboard |
| GET | `/api/dashboards/:id/data` | Get dashboard log data |
| PATCH | `/api/dashboards/:id/pin` | Toggle pin status |

### Admin

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/admin/users` | List all users |
| POST | `/api/admin/users` | Create user (admin only) |
| PUT | `/api/admin/users/:id` | Update user role/status |
| DELETE | `/api/admin/users/:id` | Delete user |
| PUT | `/api/admin/users/:id/reset-password` | Reset user password |
| GET | `/api/admin/settings` | Get application settings |
| PUT | `/api/admin/settings` | Update application settings |
| POST | `/api/admin/settings/cleanup` | Clean up old logs |
| DELETE | `/api/admin/logs` | Purge all logs |
| GET | `/api/admin/audit-log` | View audit log entries |
| POST | `/api/admin/ldap/test` | Test LDAP connection |

## Project Structure

```
├── docker-compose.yml
├── Dockerfile.backend
├── Dockerfile.frontend
├── Dockerfile.rsyslog
├── .env.example
├── backend/
│   ├── main.go              # Entry point, route setup, rate limiter, CORS
│   ├── auth/                 # JWT middleware, refresh tokens, bcrypt
│   ├── db/                   # Database connection, migrations, builtin parsers
│   ├── handler/              # HTTP handlers (auth, logs, parsers, dashboards, admin, init)
│   ├── ldap/                 # LDAP/AD authentication with TLS
│   ├── model/                # Go structs for DB models
│   ├── parser/               # Regex parser engine
│   ├── tailer/               # File tailer for rsyslog JSONL
│   └── util/                 # Key generation, encryption utilities
├── frontend/
│   ├── src/
│   │   ├── App.tsx           # Main layout with pinned sidebar
│   │   ├── pages/            # Page components (including SetupWizard)
│   │   └── services/         # API client with 401 interceptor, auth context
│   ├── nginx.conf
│   └── vite.config.ts
└── rsyslog/
    └── syslog.conf           # rsyslog template + output config
```

## Security

- **JWT Access Tokens** — Short-lived (15 minutes), HS256 signed
- **Refresh Tokens** — 7-day expiry with rotation; invalidated on logout
- **Timing-Safe Comparison** — Constant-time password verification prevents timing attacks
- **bcrypt** — Password hashing with cost factor 14
- **Rate Limiting** — In-memory rate limiter on login endpoint
- **CORS** — Configurable allowed origins; disabled by default
- **Audit Log** — All authentication events and admin actions recorded
- **Sensitive Data Masking** — JWT secret, encryption key, and LDAP password masked in API responses
- **Certificate Verification** — LDAP TLS connections verify server certificates by default

## Development

```bash
# Backend (requires Go 1.21+)
cd backend
go run main.go

# Frontend (requires Node 18+)
cd frontend
npm install
npm run dev
```

## License

MIT