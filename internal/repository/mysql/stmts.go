package mysql

import (
	"context"
	"database/sql"
)

const (
	querySelectBalance = `
		SELECT balance, version FROM balances WHERE wallet_uuid = ?
	`

	// SELECT FOR UPDATE: reads only balance (version unused in SFU path).
	querySelectBalanceForUpdate = `
		SELECT balance FROM balances WHERE wallet_uuid = ? FOR UPDATE
	`

	// Optimistic locking: WHERE version = ? is the CAS check.
	queryUpdateBalance = `
		UPDATE balances SET balance = ?, version = version + 1, updated_at = NOW(3)
		 WHERE wallet_uuid = ? AND version = ?
	 `

	// SELECT FOR UPDATE: row already locked, atomic delta applied server-side.
	queryUpdateBalanceSFU = `
		UPDATE balances SET balance = balance + ?, version = version + 1, updated_at = NOW(3)
		 WHERE wallet_uuid = ?
	 `

	// transaction_id and wallet_uuid are BINARY(16) — pass uuid[:]
	queryInsertTransaction = `
		INSERT INTO transactions (transaction_id, wallet_uuid, amount, event_type, created_at)
		 VALUES (?, ?, ?, ?, NOW(3))
	 `

	// event_outbox.uuid is CHAR(36), created_at is unix timestamp
	queryInsertOutbox = `
		INSERT INTO event_outbox (uuid, topic, payload, created_at)
		 VALUES (?, ?, ?, UNIX_TIMESTAMP())
	 `

	querySelectIdempotencyKey = `
		SELECT result FROM idempotency_keys WHERE key_hash = ?
	`

	// ON DUPLICATE KEY UPDATE result = result — keeps the first committed result (no-op on duplicate)
	queryInsertIdempotencyKey = `
		INSERT INTO idempotency_keys (key_hash, wallet_uuid, result, expires_at)
		VALUES (?, ?, ?, DATE_ADD(NOW(3), INTERVAL 24 HOUR))
		ON DUPLICATE KEY UPDATE result = result
	`

	queryPing = `
		SELECT 1
	`
)

// Statements holds all prepared statements used by repositories.
// Prepared statements are compiled once and reused across requests.
type Statements struct {
	SelectBalance          *sql.Stmt
	SelectBalanceForUpdate *sql.Stmt
	UpdateBalance          *sql.Stmt
	UpdateBalanceSFU       *sql.Stmt
	InsertTransaction      *sql.Stmt
	InsertOutbox           *sql.Stmt
	SelectIdempotencyKey   *sql.Stmt
	InsertIdempotencyKey   *sql.Stmt
	Ping                   *sql.Stmt
}

func PrepareStatements(db *sql.DB) (*Statements, error) {
	ctx := context.Background()
	stmts := &Statements{}
	var err error

	stmts.SelectBalance, err = db.PrepareContext(ctx, querySelectBalance)
	if err != nil {
		return nil, err
	}

	stmts.SelectBalanceForUpdate, err = db.PrepareContext(ctx, querySelectBalanceForUpdate)
	if err != nil {
		return nil, err
	}

	stmts.UpdateBalance, err = db.PrepareContext(ctx, queryUpdateBalance)
	if err != nil {
		return nil, err
	}

	stmts.UpdateBalanceSFU, err = db.PrepareContext(ctx, queryUpdateBalanceSFU)
	if err != nil {
		return nil, err
	}

	stmts.InsertTransaction, err = db.PrepareContext(ctx, queryInsertTransaction)
	if err != nil {
		return nil, err
	}

	stmts.InsertOutbox, err = db.PrepareContext(ctx, queryInsertOutbox)
	if err != nil {
		return nil, err
	}

	stmts.SelectIdempotencyKey, err = db.PrepareContext(ctx, querySelectIdempotencyKey)
	if err != nil {
		return nil, err
	}

	stmts.InsertIdempotencyKey, err = db.PrepareContext(ctx, queryInsertIdempotencyKey)
	if err != nil {
		return nil, err
	}

	stmts.Ping, err = db.PrepareContext(ctx, queryPing)
	if err != nil {
		return nil, err
	}

	return stmts, nil
}

func (s *Statements) Close() {
	for _, stmt := range []*sql.Stmt{
		s.SelectBalance,
		s.SelectBalanceForUpdate,
		s.UpdateBalance,
		s.UpdateBalanceSFU,
		s.InsertTransaction,
		s.InsertOutbox,
		s.SelectIdempotencyKey,
		s.InsertIdempotencyKey,
		s.Ping,
	} {
		if stmt != nil {
			stmt.Close() //nolint:errcheck,gosec
		}
	}
}
