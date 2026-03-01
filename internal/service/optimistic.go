package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/gchernikov/wallet_demo/internal/model"
	mysqlrepo "github.com/gchernikov/wallet_demo/internal/repository/mysql"
	"github.com/google/uuid"
)

// OptimisticService implements optimistic locking via version-based CAS.
// Reads balance without holding a lock, then verifies version hasn't changed
// during the update. On conflict, retries up to maxRetries times.
// Returns ConflictError (→ HTTP 409) when retries are exhausted.
type OptimisticService struct {
	db         *sql.DB
	stmts      *mysqlrepo.Statements
	maxRetries int
}

func NewOptimisticService(db *sql.DB, stmts *mysqlrepo.Statements, maxRetries int) *OptimisticService {
	return &OptimisticService{db: db, stmts: stmts, maxRetries: maxRetries}
}

func (s *OptimisticService) Update(ctx context.Context, req model.UpdateRequest) (model.UpdateResponse, error) {
	walletUUID, err := uuid.Parse(req.WalletUUID)
	if err != nil {
		return model.UpdateResponse{}, fmt.Errorf("invalid wallet_uuid: %w", err)
	}

	// clientTxID: non-nil when the client provides a transaction_id → enables idempotency.
	// nil → server generates a throwaway ID per attempt; no idempotency protection.
	var clientTxID *uuid.UUID
	if req.TransactionID != "" {
		id, _ := uuid.Parse(req.TransactionID) // already validated in handler
		clientTxID = &id
		if resp, ok := lookupIdempotency(ctx, s.stmts, id); ok {
			return resp, nil
		}
	}

	for attempt := 0; attempt <= s.maxRetries; attempt++ {
		resp, err := s.tryUpdate(ctx, req, walletUUID, clientTxID)
		if err == nil {
			return resp, nil
		}
		var conflictErr *model.ConflictError
		if !errors.As(err, &conflictErr) {
			// Non-retryable error
			return model.UpdateResponse{}, err
		}
		// ConflictError: another writer updated the row — retry
	}

	return model.UpdateResponse{}, &model.ConflictError{}
}

func (s *OptimisticService) tryUpdate(
	ctx context.Context,
	req model.UpdateRequest,
	walletUUID uuid.UUID,
	clientTxID *uuid.UUID,
) (model.UpdateResponse, error) {
	// Read current balance WITHOUT locking — allows concurrent reads
	var balance, version int64
	row := s.stmts.SelectBalance.QueryRowContext(ctx, walletUUID[:])
	if err := row.Scan(&balance, &version); err != nil {
		return model.UpdateResponse{}, err
	}

	newBalance := balance + req.Amount

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return model.UpdateResponse{}, err
	}
	defer tx.Rollback() //nolint:errcheck

	// CAS update: only succeeds if version hasn't changed since our read
	updateStmt := tx.StmtContext(ctx, s.stmts.UpdateBalance) //nolint:sqlclosecheck
	result, err := updateStmt.ExecContext(ctx, newBalance, walletUUID[:], version)
	if err != nil {
		return model.UpdateResponse{}, err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return model.UpdateResponse{}, err
	}
	if rowsAffected == 0 {
		// Another writer updated the row — version mismatch
		return model.UpdateResponse{}, &model.ConflictError{}
	}

	// txID: use client-provided ID (stable across retries) or generate a throwaway one.
	txID := uuid.New()
	if clientTxID != nil {
		txID = *clientTxID
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
		Mode:          "optimistic",
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
