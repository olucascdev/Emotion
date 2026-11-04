# Emotion — Spec-Driven Development

This document is the executable product specification for Emotion. Each phase starts with requirements, defines acceptance criteria, then adds implementation and tests. A phase is complete only when its acceptance criteria pass.

## Product goal

Emotion replaces a paper cash book for a small photography business. It answers three questions clearly: what came in, what went out and what is still pending. It supports up to two users, Portuguese Brazilian formatting, Brazilian reais and `America/Sao_Paulo` display time.

## Product rules

- Cash basis is the primary view: received payments minus paid expenses.
- A sale, an expected receivable and a received payment are different records.
- Money is stored as integer cents; no floating-point totals.
- An order and its installments are created in one database transaction.
- The server owns financial calculations and authorization; the browser is not trusted.
- The system does not store card numbers, issue invoices, reconcile bank accounts or store photo files in the first release.

## Requirements

### AUTH — authentication and access

- `AUTH-01`: Create the first administrator from environment variables and reject the default password.
- `AUTH-02`: Store passwords with Argon2id and a unique random salt.
- `AUTH-03`: Create a short-lived JWT in an HttpOnly cookie; never place tokens in localStorage.
- `AUTH-04`: Reload the active user and enforce permissions on the server for protected routes.
- `AUTH-05`: Clear the cookie on logout; production cookies are Secure and SameSite protected.

Acceptance: invalid credentials return 401; protected endpoints without a valid cookie return 401; valid credentials return the current user; secrets do not appear in logs or source code.

### CASH — book cash and dashboard

- `CASH-01`: Show received, paid expenses, result, receivables and payables for a selected month.
- `CASH-02`: A paid expense reduces the result; a pending expense appears in payables but does not reduce the result.
- `CASH-03`: A payment appears once in received cash and can be traced to its order or installment.
- `CASH-04`: Dashboard values are derived on the server from database records.

Acceptance: a R$ 100 receipt and a R$ 40 paid expense produce R$ 60 result; pending values remain separate; reloading cannot create duplicate cash entries.

### ORDER — service orders and receivables

- `ORDER-01`: Record customer, service type, description, total and status.
- `ORDER-02`: Split the total into 1–120 installments and distribute cent residuals deterministically.
- `ORDER-03`: Record an initial payment as a paid entry and generate remaining receivables from the outstanding balance.
- `ORDER-05`: Capture the service/photography date, first installment due date, observations and separate payment methods for the entry and remaining installments.
- `ORDER-04`: Reject overpayments, negative amounts and invalid required fields.

Acceptance: a R$ 1,200 order in 12 installments creates twelve R$ 100 receivables; a R$ 500 signal creates a paid entry and leaves R$ 700 to receive; the form previews each remaining installment, stores the service date and supports separate Pix/credit/etc. methods; an overpayment returns 400 and creates no committed order.

### CAL — calendar

- `CAL-01`: Appointments have title, start, end, location, notes and optional order link.
- `CAL-02`: Clicking an appointment opens its full details, including linked order context, and exposes an in-app cancellation action without browser-native confirmation dialogs.
- `CAL-02`: The UI provides daily, weekly and monthly views.
- `CAL-03`: User-facing times use `America/Sao_Paulo`; instants are stored in UTC.
- `CAL-04`: Conflicting times are visible before saving.

Acceptance: an appointment created in São Paulo renders on the same local date; all three views navigate the selected period; malformed intervals are rejected.

### UX — accessibility and simplicity

- `UX-01`: Main navigation has Overview, Service Orders and Calendar.
- `UX-02`: Use plain language (“Entrou”, “Saiu”, “Falta receber”) and large controls.
- `UX-03`: Support light/dark theme, keyboard focus and mobile layout.
- `UX-04`: Forms show clear validation and success/error feedback.

Acceptance: a first-time user can log in, create an order and see its total without documentation; the layout remains usable at mobile width; color is not the only status indicator.

## Delivery phases

### Phase 0 — specification and repository

Status: **complete**. The plan, this specification, repository structure, environment template and acceptance checklist are in place.

### Phase 1 — foundation and secure access

Status: **complete**. Go service, SQLite migrations, first admin, Argon2id password hashing, JWT cookie session, protected routes, React/TypeScript/Tailwind shell and Docker image are implemented and tested.

### Phase 2 — cashbook and service orders

Status: **partially complete**. Dashboard API, expense creation/payment, order creation/detail, installment generation, initial-payment allocation, payment recording, audit writes and CSV export are implemented. Remaining: order editing and an audit history screen.

### Phase 3 — calendar workflow

Status: **partially complete**. Appointment API, daily/weekly/monthly grids, create modal and conflict validation are implemented. Remaining: edit/cancel actions and visible order links in the calendar UI.

### Phase 4 — operational hardening

Status: **partially complete**. Backup/restore command, login rate limiting, structured request logs, health checks and controlled CORS are implemented. Remaining: encrypted off-site backup automation, monitoring and final production CORS configuration.

### Phase 5 — production acceptance

Status: **planned**. Run the full acceptance checklist with the business owner, deploy to Dockploy with a persistent `/app/data` volume, verify HTTPS, confirm administrator workflows and tag the first production release.

## Architecture contract

```text
Browser ── HTTPS ──> Dockploy proxy ──> Emotion container
                                      ├── Go HTTP API
                                      ├── SQLite at /app/data
                                      └── Vite static build
```

The frontend calls relative `/api` routes. The backend uses parameterized SQL, transactions for financial writes, security headers and JSON errors. Docker exposes port 8080 and includes a health check at `/api/health`.

## Verification commands

```bash
go test ./...
go vet ./...
cd web && npm run build && npm audit --audit-level=high
docker build -t emotion:local .
```

The production gate additionally requires a container smoke test, a persistent volume, a real admin login, one order, one paid expense, one pending expense, a dashboard reconciliation and a successful backup restore.
