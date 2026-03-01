package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/gchernikov/wallet_demo/internal/model"
	mysqlrepo "github.com/gchernikov/wallet_demo/internal/repository/mysql"
	"github.com/google/uuid"
)

// SelectForUpdateService implements pessimistic locking via SELECT ... FOR UPDATE.
// The balance row is locked for the duration of the transaction, preventing
// concurrent updates. High contention under many requests per wallet.
type SelectForUpdateService struct {
	db    *sql.DB
	stmts *mysqlrepo.Statements
}

func NewSelectForUpdateService(db *sql.DB, stmts *mysqlrepo.Statements) *SelectForUpdateService {
	return &SelectForUpdateService{db: db, stmts: stmts}
}

func (s *SelectForUpdateService) Update(ctx context.Context, req model.UpdateRequest) (model.UpdateResponse, error) {
	walletUUID, err := uuid.Parse(req.WalletUUID)
	if err != nil {
		return model.UpdateResponse{}, fmt.Errorf("invalid wallet_uuid: %w", err)
	}

	// clientTxID: non-nil when the client provides a transaction_id → enables idempotency.
	// nil → server generates a throwaway ID; no idempotency protection.
	var clientTxID *uuid.UUID
	if req.TransactionID != "" {
		id, _ := uuid.Parse(req.TransactionID) // already validated in handler
		clientTxID = &id
		if resp, ok := lookupIdempotency(ctx, s.stmts, id); ok {
			return resp, nil
		}
	}

	// txID: use client-provided ID or generate a throwaway one.
	txID := uuid.New()
	if clientTxID != nil {
		txID = *clientTxID
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return model.UpdateResponse{}, err
	}
	defer tx.Rollback() //nolint:errcheck

	// Lock the row — no concurrent update can proceed until we COMMIT/ROLLBACK
	var balance int64
	sfuStmt := tx.StmtContext(ctx, s.stmts.SelectBalanceForUpdate) //nolint:sqlclosecheck
	row := sfuStmt.QueryRowContext(ctx, walletUUID[:])
	if err := row.Scan(&balance); err != nil {
		return model.UpdateResponse{}, err
	}

	newBalance := balance + req.Amount

	// Atomic delta: balance = balance + amount (server-side, no absolute value passed)
	sfuUpdateStmt := tx.StmtContext(ctx, s.stmts.UpdateBalanceSFU) //nolint:sqlclosecheck
	if _, err := sfuUpdateStmt.ExecContext(ctx, req.Amount, walletUUID[:]); err != nil {
		return model.UpdateResponse{}, err
	}

	eventID := uuid.New()
	eventType := creditOrDebit(req.Amount)

	// transaction_id and wallet_uuid are BINARY(16) — pass raw bytes
	txStmt := tx.StmtContext(ctx, s.stmts.InsertTransaction) //nolint:sqlclosecheck
	if _, err := txStmt.ExecContext(ctx, txID[:], walletUUID[:], req.Amount, eventType); err != nil {
		return model.UpdateResponse{}, err
	}

	payload, _ := json.Marshal(map[string]any{ //nolint:errchkjson
		"wallet_uuid":    req.WalletUUID,
		"amount":         req.Amount,
		"event_type":     eventType,
		"transaction_id": txID.String(),
	})

	// event_outbox.uuid is CHAR(36) string
	outboxStmt := tx.StmtContext(ctx, s.stmts.InsertOutbox) //nolint:sqlclosecheck
	if _, err := outboxStmt.ExecContext(ctx, eventID.String(), "wallet.balance.updated", payload); err != nil {
		return model.UpdateResponse{}, err
	}

	resp := model.UpdateResponse{
		WalletUUID:    req.WalletUUID,
		Balance:       newBalance,
		Mode:          "select-for-update",
		TransactionID: txID.String(),
		OutboxEventID: eventID.String(),
	}

	if clientTxID != nil {
		if err := saveIdempotency(ctx, tx, s.stmts, *clientTxID, walletUUID, resp); err != nil {
			return model.UpdateResponse{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return model.UpdateResponse{}, err
	}

	return resp, nil
}

func creditOrDebit(amount int64) string {
	if amount >= 0 {
		return "credit"
	}
	return "debit"
}
