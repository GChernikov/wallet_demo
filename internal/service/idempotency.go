package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/gchernikov/wallet_demo/internal/model"
	mysqlrepo "github.com/gchernikov/wallet_demo/internal/repository/mysql"
	"github.com/google/uuid"
)

// lookupIdempotency checks whether this key was already processed and committed.
// Returns (response, true) on cache hit, (zero, false) on miss or any error.
func lookupIdempotency(ctx context.Context, stmts *mysqlrepo.Statements, key uuid.UUID) (model.UpdateResponse, bool) {
	var raw []byte
	err := stmts.SelectIdempotencyKey.QueryRowContext(ctx, key[:]).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) || err != nil {
		return model.UpdateResponse{}, false
	}
	var resp model.UpdateResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return model.UpdateResponse{}, false
	}
	return resp, true
}

// saveIdempotency persists the result inside the open transaction.
// ON DUPLICATE KEY UPDATE result = result keeps the first committed result on retry races.
func saveIdempotency(
	ctx context.Context,
	tx *sql.Tx,
	stmts *mysqlrepo.Statements,
	key, walletUUID uuid.UUID,
	resp model.UpdateResponse,
) error {
	payload, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	idemStmt := tx.StmtContext(ctx, stmts.InsertIdempotencyKey) //nolint:sqlclosecheck
	_, err = idemStmt.ExecContext(ctx, key[:], walletUUID[:], payload)
	return err
}
