#!/bin/sh
set -eu

BASE_URL=${BASE_URL:-http://127.0.0.1:8080}
EMAIL=${ADMIN_EMAIL:?ADMIN_EMAIL is required}
PASSWORD=${ADMIN_PASSWORD:?ADMIN_PASSWORD is required}
COOKIE_FILE=$(mktemp)
trap 'rm -f "$COOKIE_FILE"' EXIT

health=$(curl -fsS "$BASE_URL/api/health")
printf '%s\n' "$health" | grep -q '"status":"ok"'
curl -fsS -c "$COOKIE_FILE" -H 'Content-Type: application/json' -d "{\"email\":\"$EMAIL\",\"password\":\"$PASSWORD\"}" "$BASE_URL/api/auth/login" >/dev/null
curl -fsS -b "$COOKIE_FILE" -H 'Content-Type: application/json' -d '{"customer_name":"Smoke Test","service_type":"Foto 3x4","total_cents":1000,"installments":1}' "$BASE_URL/api/orders" >/dev/null
curl -fsS -b "$COOKIE_FILE" "$BASE_URL/api/dashboard" | grep -q 'received_cents'
printf '%s\n' 'Emotion smoke test passed.'
