# Emotion — Software Design Description

## 1. Purpose

Emotion is a small internal management system for a photography business. It replaces the paper cash book with a clear workflow for service orders, receivables, cash movements, expenses and appointments. The first release is intentionally sized for one business and up to two users.

## 2. Product decisions

- **Frontend:** React, TypeScript, Vite and Tailwind CSS. The UI uses large controls, short forms, a three-item sidebar and light/dark themes.
- **Backend:** Go `net/http` service. Business rules live on the server so totals and permissions cannot be changed by the browser.
- **Persistence:** SQLite on a persistent VPS volume. Monetary values are integer cents. All multi-row order creation runs in a transaction.
- **Runtime:** One Docker image serves both the compiled frontend and API. A reverse proxy such as Dockploy's proxy terminates TLS.
- **Time:** Instants are stored in UTC; user-facing display uses `America/Sao_Paulo`. Local due dates remain date-only values.

## 3. System boundaries

The system owns orders, customers, payment plans, received payments, expenses, appointments and audit records. It does not process card numbers, issue invoices, reconcile banks, send WhatsApp messages or store photo files in phase 1.

```text
Browser ── HTTPS ──> Reverse proxy ──> Emotion container
                                      ├── Go API
                                      ├── SQLite volume (/app/data)
                                      └── Static Vite build
```

## 4. Domain model

`service_orders` represent the work sold. `receivables` represent the expected installments. `payments` represent money actually received. `expenses` represent accounts payable and their paid state. `appointments` represent time in the calendar. `audit_events` preserve the actor and action for financial changes.

The dashboard uses cash basis: `received payments - paid expenses`. Expected receivables and pending expenses are displayed separately. An order for R$ 1,200 in 12 installments creates twelve expected R$ 100 receivables; it does not create a R$ 1,200 cash entry until payments are recorded.

## 5. API contract (phase 1)

All protected endpoints require the `emotion_access` HttpOnly cookie. JSON errors use `{ "error": "message" }`.

| Method | Endpoint | Purpose |
| --- | --- | --- |
| GET | `/api/health` | Liveness probe. |
| POST | `/api/auth/login` | Authenticate and set a short-lived JWT cookie. |
| POST | `/api/auth/logout` | Clear the access cookie. |
| GET | `/api/auth/me` | Return the current user. |
| GET | `/api/dashboard?month=YYYY-MM` | Return cash, receivable and payable totals. |
| GET/POST | `/api/orders` | List or create service orders and installment plans. |
| GET/POST | `/api/expenses` | List or create expenses. |
| GET/POST | `/api/appointments` | List or create appointments. |

## 6. Security design

- First-run admin credentials are supplied through environment variables; the default password is rejected.
- Passwords use Argon2id with a unique random salt. Passwords and tokens are never logged.
- Access JWTs use HS256 with a server-only secret, explicit issuer/audience, 15-minute expiration and an allowlisted algorithm. Tokens are HttpOnly, Secure in production and SameSite Lax cookies; the frontend never reads them.
- Every protected request validates the cookie and reloads the active user from the database. No authorization decision is trusted from client input.
- Request bodies are size limited and required fields are validated. SQL uses placeholders. Security headers, CORS credentials and HTTPS deployment are documented.
- Production must set `JWT_SECRET` from Dockploy secrets, enable `COOKIE_SECURE=true`, restrict the proxy origin, mount `/app/data`, and back up the SQLite database outside the VPS.

## 7. Delivery phases

1. **Foundation (this release):** repository, SDD/plan, Docker, SQLite schema, auth, dashboard API, orders, expenses, appointments, responsive shell and API tests.
2. **Workflow completion:** payment recording UI, order detail/editing, expense payment actions, calendar day/week/month views, audit UI and CSV export.
3. **Operations:** backup/restore command, health checks, rate limiting, structured logs, security headers review, deployment runbook and staging restore rehearsal.

## 8. Verification strategy

- `go test ./...` covers login, protected routes, dashboard calculation path and invalid overpayment input.
- `npm run build` type-checks and compiles the frontend.
- `docker build -t emotion:local .` verifies the production image can build both layers.
- Before production: run the container with a persistent volume, check `/api/health`, perform an admin login, create an order and expense, verify dashboard totals, and restore a backup into a temporary database.
