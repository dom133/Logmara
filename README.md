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

# Default login: admin / admin123
# UI available at http://localhost
```

## Configuration

Copy `.env.example` and adjust values:

| Variable | Default | Description |
|----------|---------|-------------|
| `ADMIN_USERNAME` | `admin` | Initial admin username |
| `ADMIN_PASSWORD` | `admin123` | Initial admin password |
| `JWT_SECRET` | *(see .env)* | JWT signing key — **change in production** |
| `DATABASE_URL` | `postgres://…` | PostgreSQL connection string |
| `PORT` | `8080` | API server port |
| `LOG_FILE_PATH` | `/data/logs.jsonl` | Path to rsyslog JSON output |

## Features

- **Live Log Viewer** — Browse, filter, and search ingested syslog messages in real-time
- **Parser Engine** — Define regex-based parsers to extract structured fields from raw log lines
- **Custom Dashboards** — Create dashboards filtered by device, severity, or parsed fields
- **Pin Dashboards** — Pin frequently-used dashboards to the sidebar for quick access
- **Export** — Download logs as CSV or HTML reports
- **Statistics** — Timeline charts, severity breakdown, and per-device metrics
- **Multi-user Auth** — JWT-based authentication with password change support

## API Endpoints

### Authentication

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/auth/login` | Authenticate and receive JWT |
| POST | `/api/auth/register` | Register new user |
| GET | `/api/auth/me` | Get current user profile |
| POST | `/api/auth/change-password` | Change user password |

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

## Project Structure

```
├── docker-compose.yml
├── Dockerfile.backend
├── Dockerfile.frontend
├── Dockerfile.rsyslog
├── .env.example
├── backend/
│   ├── main.go              # Entry point, route setup
│   ├── auth/                 # JWT middleware, admin init
│   ├── db/                   # Database connection, migrations
│   ├── handler/              # HTTP handlers (logs, parsers, dashboards…)
│   ├── model/                # Go structs for DB models
│   ├── parser/               # Regex parser engine
│   └── tailer/               # File tailer for rsyslog JSONL
├── frontend/
│   ├── src/
│   │   ├── App.tsx           # Main layout with pinned sidebar
│   │   ├── pages/            # Page components
│   │   └── services/         # API client, auth context
│   ├── nginx.conf
│   └── vite.config.ts
└── rsyslog/
    └── syslog.conf           # rsyslog template + output config
```

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