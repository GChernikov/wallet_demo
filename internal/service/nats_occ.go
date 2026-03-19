package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/gchernikov/wallet_demo/internal/model"
	natsrepo "github.com/gchernikov/wallet_demo/internal/repository/nats"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/synadia-io/orbit.go/jetstreamext"
)

// NatsOCCService implements event-sourced balance updates via NATS JetStream.
// Balance is the sum of all delta events in the stream — MySQL is not used.
// OCC is enforced via WithExpectLastSequencePerSubject on every publish.
//
// Fast path: projection snapshot → attempt OCC publish.
// Conflict path: jetstreamext.GetBatch fetches the tail events directly
// (no consumer creation, no polling) — same pattern as wallet manager.
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

		// OCC conflict: another writer published ahead of us.
		// Use jetstreamext.GetBatch (Direct Get, no consumer) to read the
		// tail events for this wallet's subject and recompute current state.
		var apiErr *jetstream.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode == 10071 {
			curBalance, curSeq, err = s.fetchByDirectGet(ctx, subject, curBalance, curSeq)
			if err != nil {
				return model.UpdateResponse{}, err
			}
			continue
		}

		// Non-retryable error (context cancelled, network, etc.)
		return model.UpdateResponse{}, err
	}

	return model.UpdateResponse{}, &model.ConflictError{}
}

// fetchByDirectGet reads all events for the wallet subject from fromSeq+1 onwards
// using NATS Direct Get (jetstreamext.GetBatch) — no consumer is created.
// Returns the updated (balance, lastSeq) after accumulating all tail deltas.
func (s *NatsOCCService) fetchByDirectGet(
	ctx context.Context,
	subject string,
	startBalance int64,
	fromSeq uint64,
) (int64, uint64, error) {
	const batchSize = 256

	balance := startBalance
	lastSeq := fromSeq

	for {
		msgIter, err := jetstreamext.GetBatch(
			ctx,
			s.js,
			natsrepo.StreamName,
			batchSize,
			jetstreamext.GetBatchSubject(subject),
			jetstreamext.GetBatchSeq(lastSeq+1),
		)
		if err != nil {
			return 0, 0, fmt.Errorf("direct get batch: %w", err)
		}

		count := 0
		for msg, err := range msgIter {
			if err != nil {
				if errors.Is(err, jetstreamext.ErrNoMessages) {
					break
				}
				return 0, 0, fmt.Errorf("iterate direct get: %w", err)
			}

			var ev natsrepo.WalletEvent
			if err := json.Unmarshal(msg.Data, &ev); err != nil {
				return 0, 0, fmt.Errorf("unmarshal event: %w", err)
			}

			balance += ev.Amount
			lastSeq = msg.Sequence
			count++
		}

		// Fewer messages than requested → we've read the full tail.
		if count < batchSize {
			break
		}
	}

	return balance, lastSeq, nil
}
