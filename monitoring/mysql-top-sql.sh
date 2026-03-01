#!/usr/bin/env bash
# Show MySQL lock stats from performance_schema.
# Run while load test is active or immediately after.
# Usage: bash monitoring/mysql-top-sql.sh [reset]
#   reset — truncate performance_schema stats before querying

set -euo pipefail

MYSQL_ROOT="docker compose exec -T mysql mysql -uroot -proot"
MYSQL_DB="docker compose exec -T mysql mysql -uroot -proot wallet_demo"

if [[ "${1:-}" == "reset" ]]; then
    echo "=== Resetting performance_schema stats ==="
    $MYSQL_ROOT -e "TRUNCATE performance_schema.events_statements_summary_by_digest;" 2>/dev/null
    $MYSQL_ROOT -e "FLUSH STATUS;" 2>/dev/null
    echo "Done. Run the script again without 'reset' during load test."
    exit 0
fi

echo "=== InnoDB row lock stats ==="
$MYSQL_ROOT -e "SHOW GLOBAL STATUS LIKE 'Innodb_row_lock%';" 2>/dev/null

echo ""
echo "=== Active lock waits (data_lock_waits) ==="
$MYSQL_ROOT -e "
SELECT COUNT(*) AS active_lock_waits
FROM performance_schema.data_lock_waits;" 2>/dev/null

echo ""
echo "=== Data locks breakdown by table/mode/status ==="
$MYSQL_ROOT -e "
SELECT
    OBJECT_NAME                   AS tbl,
    LOCK_TYPE                     AS type,
    LOCK_MODE                     AS mode,
    LOCK_STATUS                   AS status,
    COUNT(*)                      AS cnt
FROM performance_schema.data_locks
WHERE OBJECT_SCHEMA = 'wallet_demo'
GROUP BY OBJECT_NAME, LOCK_TYPE, LOCK_MODE, LOCK_STATUS
ORDER BY OBJECT_NAME, LOCK_STATUS;" 2>/dev/null

echo ""
echo "=== Top SQL by lock time (performance_schema) ==="
$MYSQL_DB -e "
SELECT
    DIGEST_TEXT                           AS query,
    COUNT_STAR                            AS exec_count,
    ROUND(SUM_LOCK_TIME   / 1e9,  2)     AS lock_ms,
    ROUND(AVG_TIMER_WAIT  / 1e9,  2)     AS avg_ms,
    ROUND(MAX_TIMER_WAIT  / 1e9,  2)     AS max_ms,
    SUM_ROWS_AFFECTED                     AS rows_aff
FROM performance_schema.events_statements_summary_by_digest
WHERE SCHEMA_NAME = 'wallet_demo'
  AND DIGEST_TEXT NOT LIKE '%performance_schema%'
ORDER BY lock_ms DESC
LIMIT 15;" 2>/dev/null

echo ""
echo "=== INNODB STATUS: TRANSACTIONS section ==="
$MYSQL_ROOT -e "SHOW ENGINE INNODB STATUS\G" 2>/dev/null \
    | awk '/^TRANSACTIONS/,/^[A-Z]/' \
    | head -40
