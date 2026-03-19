package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gchernikov/wallet_demo/internal/model"
	natsrepo "github.com/gchernikov/wallet_demo/internal/repository/nats"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
)

// NatsOCCService implements event-sourced balance updates via NATS JetStream.
// Balance is derived from summing all events in the stream; MySQL is not used.
// OCC is enforced via WithExpectLastSequencePerSubject on every publish.
type NatsOCCService struct {
	js         jetstream.JetStream
	proj       *natsrepo.Projection
	maxRetries int
}

func NewNatsOCCService(js jetstream.JetStream, proj *natsrepo.Projection, maxRetries int) *NatsOCCService {
	return &NatsOCCService{js: js, proj: proj, maxRetries: maxRetries}
}

func (s *NatsOCCService) Update(ctx context.Context, req model.UpdateRequest) (model.UpdateResponse, error) {
	entry, err := s.proj.Get(req.WalletUUID)
	if err != nil {
		if errors.Is(err, natsrepo.ErrWalletNotFound) {
			return model.UpdateResponse{}, fmt.Errorf("wallet not found: %w", sql.ErrNoRows)
		}
		return model.UpdateResponse{}, err
	}

	txID := req.TransactionID
	if txID == "" {
		txID = uuid.New().String()
	}

	curBalance := entry.Balance
	curSeq := entry.LastSeq
	subject := "wallets." + req.WalletUUID

	for attempt := 0; attempt <= s.maxRetries; attempt++ {
		newBalance := curBalance + req.Amount

		payload, err := json.Marshal(natsrepo.WalletEvent{
			Amount:        req.Amount,
			TransactionID: txID,
			EventType:     creditOrDebit(req.Amount),
		})
		if err != nil {
			return model.UpdateResponse{}, err
		}

		_, err = s.js.Publish(ctx, subject, payload,
			jetstream.WithExpectLastSequencePerSubject(curSeq))
		if err == nil {
			return model.UpdateResponse{
				WalletUUID:    req.WalletUUID,
				Balance:       newBalance,
				Mode:          "nats-occ",
				TransactionID: txID,
				OutboxEventID: "",
			}, nil
		}

		// Check for OCC conflict: another writer published ahead of us.
		// Re-read balance from the in-memory projection (updated by the background consumer).
		// This avoids creating any NATS consumers in the hot path.
		var apiErr *jetstream.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode == 10071 {
			entry, err = s.waitForProjection(ctx, req.WalletUUID, curSeq)
			if err != nil {
				return model.UpdateResponse{}, err
			}
			curBalance, curSeq = entry.Balance, entry.LastSeq
			continue
		}

		// Non-retryable error (context cancelled, network, etc.)
		return model.UpdateResponse{}, err
	}

	return model.UpdateResponse{}, &model.ConflictError{}
}

// waitForProjection blocks until the in-memory projection advances past oldSeq
// (meaning the background consumer has processed the conflicting event), then
// returns the fresh entry. No NATS API calls — purely in-memory.
func (s *NatsOCCService) waitForProjection(ctx context.Context, walletID string, oldSeq uint64) (natsrepo.Entry, error) {
	for {
		e, err := s.proj.Get(walletID)
		if err != nil {
			return natsrepo.Entry{}, err
		}
		if e.LastSeq > oldSeq {
			return e, nil
		}
		select {
		case <-ctx.Done():
			return natsrepo.Entry{}, ctx.Err()
		case <-time.After(time.Millisecond):
		}
	}
}
