#!/bin/sh
set -eu

DATABASE_PATH=${DATABASE_PATH:-/app/data/emotion.db}
BACKUP_DIR=${BACKUP_DIR:-/app/data/backups}
STAMP=$(date -u +%Y-%m-%dT%H-%M-%SZ)
mkdir -p "$BACKUP_DIR"
/app/emotion-backup -mode backup -db "$DATABASE_PATH" -out "$BACKUP_DIR/emotion-$STAMP.db"
find "$BACKUP_DIR" -type f -name 'emotion-*.db' -mtime +30 -delete
printf '%s\n' "created $BACKUP_DIR/emotion-$STAMP.db"
