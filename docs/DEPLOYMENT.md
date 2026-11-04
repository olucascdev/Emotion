# Emotion deployment runbook

## Dockploy settings

- Build from the repository `olucascdev/Emotion` and use the included `Dockerfile`.
- Expose container port `8080`.
- Mount a persistent volume at `/app/data`.
- Set `COOKIE_SECURE=true` behind HTTPS.
- Set `CORS_ORIGIN` to the exact public origin, for example `https://emotion.example.com`.
- Set `JWT_SECRET` to a random secret of at least 32 characters.
- Set a unique `ADMIN_EMAIL` and `ADMIN_PASSWORD` before the first start. The default password is rejected.

## Backup and restore

Run backups while the service is available:

```bash
/app/emotion-backup -mode backup -db /app/data/emotion.db -out /app/data/backups/emotion-$(date +%Y-%m-%d).db
```

Copy the backup outside the VPS and encrypt it with the organization's secret-management process. Keep at least one recent copy outside the server. To restore, stop the application first, then run:

```bash
/app/emotion-backup -mode restore -out /path/to/backup.db -db /app/data/emotion.db
```

The included `/app/scripts/backup.sh` wrapper creates UTC-stamped backups and removes local copies older than 30 days. Schedule it with Dockploy or a VPS timer, then copy each new file to an encrypted off-site location.

The restore command checks SQLite integrity before replacing the database file. Always perform a restore rehearsal in a temporary container before a real incident.

## Smoke test after deployment

1. Open `/api/health` and expect `{"status":"ok","service":"emotion"}`.
2. Log in with the administrator account.
3. Create one order with an initial payment and verify “Falta receber”.
4. Create one paid expense and one pending expense.
5. Verify the dashboard result and download the CSV cashbook.
6. Create overlapping appointments and confirm the second one is rejected.

The repository also includes `scripts/smoke.sh`. Set `BASE_URL`, `ADMIN_EMAIL` and `ADMIN_PASSWORD`, then run it after a deployment to automate the health, login, order and dashboard checks.
