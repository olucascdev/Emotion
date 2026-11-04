# Emotion

Emotion is a small, secure management system for a photography business. It replaces the paper cash book with a simple interface for recording income, expenses, service orders, receivables and appointments.

The product is designed for a small operation with one or two users. The interface is in Brazilian Portuguese, uses Brazilian reais (BRL), follows `America/Sao_Paulo` for display and keeps the workflow intentionally simple for daily use.

## What the project does

- Provides a cash-book dashboard with received income, paid expenses, monthly result, receivables and payables.
- Creates service orders (OS) with customer, service type, description, service date, time, location and payment plan.
- Calculates installment values on the server and distributes remaining cents deterministically.
- Supports an initial payment plus a remaining installment plan, with separate payment methods.
- Records payments and expenses using integer cents instead of floating-point money values.
- Provides daily, weekly and monthly calendar views in Brasília time.
- Automatically creates a linked calendar appointment for photo shoots, weddings and events.
- Keeps product orders out of the calendar: Foto 3x4, photo printing, mugs, shirts, pillows, frames and other products do not create appointments.
- Shows full appointment details and allows cancellation without browser-native confirmation dialogs.
- Includes administrator and manager roles, audit events, CSV cash-book export and SQLite backup/restore tooling.

## Architecture

```text
Browser
  │ HTTPS in production
  ▼
Dockploy / reverse proxy
  ▼
Emotion container (port 8080)
  ├── Go HTTP API
  ├── SQLite database at /app/data/emotion.db
  └── Vite-built React static files
```

The frontend is a React + TypeScript + Tailwind application. It calls relative `/api` routes and never performs financial calculations as the source of truth.

The backend is a Go `net/http` service using SQLite through `modernc.org/sqlite`. Financial writes are transactional: creating an OS, its receivables and its automatic appointment either completes together or rolls back together.

SQLite is intentional for the current scope: the business has at most two users and needs a reliable single-file database with simple backups. PostgreSQL is not required for this first release.

## Main domain rules

### Cash book

Emotion uses a cash-basis view:

```text
monthly result = payments received - expenses paid
```

A service order, an expected receivable and a received payment are separate records. This prevents a R$ 1,200 order paid in installments from being counted as R$ 1,200 received on the creation date.

### Service orders and calendar

When an OS is created with a service date:

| Service type | Calendar behavior |
| --- | --- |
| Ensaio fotográfico | Creates a linked appointment |
| Casamento | Creates a linked appointment |
| Evento | Creates a linked appointment |
| Foto 3x4 | Does not create an appointment |
| Impressão, caneca, camiseta, almofada, porta-retrato and other products | Does not create an appointment |

The OS form stores the service date, start/end time, location and notes. Existing eligible OS records without an appointment are backfilled at application startup without creating duplicates. The default service interval is 09:00–10:00 when no time is supplied.

Appointment instants are stored in UTC and rendered in `America/Sao_Paulo`. The calendar includes the current day and a recent history window so appointments do not disappear immediately after their start time.

## Repository structure

```text
Emotion/
├── cmd/
│   ├── server/             # HTTP server entrypoint
│   └── backup/             # SQLite backup and restore command
├── internal/app/           # API, authentication, migrations and tests
├── web/
│   ├── src/                # React/TypeScript application
│   ├── index.html
│   └── package.json
├── docs/
│   ├── SDD.md              # Spec-Driven Development specification
│   ├── plan.md             # Product and delivery plan
│   └── DEPLOYMENT.md       # Dockploy/VPS runbook
├── scripts/
│   ├── backup.sh           # Scheduled backup wrapper
│   └── smoke.sh            # Post-deployment smoke test
├── data/                   # Local SQLite data directory
├── Dockerfile              # Multi-stage production image
├── docker-compose.yml      # Local/container orchestration
├── Makefile
└── README.md
```

## Security model

- Passwords are hashed with Argon2id and a unique random salt.
- Sessions use short-lived JWTs signed with HS256 and stored in an `HttpOnly` cookie.
- The session cookie is `SameSite=Lax`, has a 15-minute max age and is `Secure` when `COOKIE_SECURE=true`.
- `JWT_SECRET` must contain at least 32 characters; the default placeholder password is rejected on first setup.
- Protected routes validate the session server-side. The browser is never trusted for authorization or financial totals.
- Administrator-only routes protect user management and audit access.
- Login attempts are rate-limited per client address after repeated failures.
- SQL statements use parameters, and financial mutations use database transactions.
- Security headers include CSP, `X-Frame-Options`, `X-Content-Type-Options` and a restrictive referrer policy.
- CORS is limited to the exact configured origin.
- The production Docker image runs as the unprivileged `emotion` user.
- Secrets and production databases must not be committed to Git.

## Configuration

Copy `.env.example` to `.env` and replace every production value:

```dotenv
PORT=8080
DATABASE_PATH=./data/emotion.db
JWT_SECRET=replace-with-a-random-32-byte-secret
COOKIE_SECURE=false
CORS_ORIGIN=http://localhost:5173
ADMIN_EMAIL=admin@example.com
ADMIN_PASSWORD=change-me-now
```

For production, use a random `JWT_SECRET`, a unique administrator password, `COOKIE_SECURE=true` and the exact HTTPS origin in `CORS_ORIGIN`.

## Run locally

### With Docker Compose

```bash
cp .env.example .env
# Edit .env before the first start.
docker compose up --build
```

Open [http://localhost:8080](http://localhost:8080). The SQLite database is persisted in the `emotion-data` volume.

### Without Docker

Build the frontend first so the Go server can serve `web/dist`:

```bash
cd web
npm ci
npm run build
cd ..

export JWT_SECRET='replace-with-a-random-32-byte-secret'
export ADMIN_EMAIL='admin@example.com'
export ADMIN_PASSWORD='use-a-real-local-password'
export COOKIE_SECURE=false
go run ./cmd/server
```

The server listens on port `8080` by default. `make dev` starts the Go server; `make build` builds the frontend and API binaries.

## Tests and verification

```bash
go test ./...
go vet ./...
cd web && npm run build && npm audit --audit-level=high
cd ..
docker build -t emotion:local .
```

After a deployment, run the smoke test with the deployment credentials:

```bash
BASE_URL=https://emotion.example.com \
ADMIN_EMAIL='admin@example.com' \
ADMIN_PASSWORD='your-password' \
./scripts/smoke.sh
```

The smoke test checks health, login, order creation and dashboard access.

## Dockploy deployment

Use the repository `olucascdev/Emotion` and the included `Dockerfile`.

1. Expose container port `8080`.
2. Mount persistent storage at `/app/data`.
3. Configure `JWT_SECRET`, `ADMIN_EMAIL`, `ADMIN_PASSWORD`, `COOKIE_SECURE=true` and the production `CORS_ORIGIN`.
4. Put the container behind HTTPS.
5. Configure scheduled backups using `/app/emotion-backup` or `scripts/backup.sh`.
6. Keep encrypted copies of backups outside the VPS and rehearse restores.

See [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md) for the full runbook.

## Documentation

- [Spec-Driven Development specification](docs/SDD.md)
- [Product plan](docs/plan.md)
- [Deployment runbook](docs/DEPLOYMENT.md)

## Project status

Emotion is an actively developed first release. The foundation, secure access, cash book, service orders, automatic calendar synchronization, calendar views, Docker packaging and smoke tests are implemented. Future iterations can add richer order editing, advanced reports, encrypted off-site backup automation and production monitoring.
