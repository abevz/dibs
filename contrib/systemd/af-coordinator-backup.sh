#!/usr/bin/env bash
set -euo pipefail

if [ "${DIBS_DB+x}" ]; then
    DB_PATH="$DIBS_DB"
elif [ "${AF_COORDINATOR_DB+x}" ]; then
    DB_PATH="$AF_COORDINATOR_DB"
elif [ -f "$HOME/.local/share/af-coordinator/af-coordinator.db" ]; then
    DB_PATH="$HOME/.local/share/af-coordinator/af-coordinator.db"
else
    DB_PATH="$HOME/.local/share/dibs/dibs.db"
fi
if [ ! -f "$DB_PATH" ]; then
    echo "database does not exist: $DB_PATH" >&2
    exit 1
fi
BACKUPDIR="${BACKUPDIR:-$HOME/backups/dibs}"
TIMESTAMP=$(date +"%Y%m%d-%H%M")
BACKUP_FILE="$BACKUPDIR/dibs-$TIMESTAMP.db"

stat_mtime() {
    if stat -c %Y "$1" >/dev/null 2>&1; then
        stat -c %Y "$1"
    else
        stat -f %m "$1"
    fi
}

mkdir -p "$BACKUPDIR"

echo "Starting backup of $DB_PATH to $BACKUP_FILE"
sqlite3 "$DB_PATH" "VACUUM INTO '$BACKUP_FILE'"

echo "Running integrity check on backup file..."
CHECK=$(sqlite3 "$BACKUP_FILE" "PRAGMA integrity_check")

if [ "$CHECK" != "ok" ]; then
    echo "ERROR: Integrity check failed on backup file $BACKUP_FILE"
    echo "Output: $CHECK"
    rm -f "$BACKUP_FILE"
    exit 1
fi

echo "Integrity check passed."

echo "Pruning old backups (keeping last 14)..."
find "$BACKUPDIR" -maxdepth 1 -type f -name 'dibs-*.db' -print |
    while IFS= read -r file; do
        printf '%s\t%s\n' "$(stat_mtime "$file")" "$file"
    done |
    sort -rn |
    awk 'NR > 14 { sub(/^[^\t]*\t/, ""); print }' |
    while IFS= read -r file; do
        rm -f "$file"
    done

echo "Backup complete."
