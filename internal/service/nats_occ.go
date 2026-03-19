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
)

// NatsOCCService implements event-sourced balance updates via NATS JetStream.
// Balance is derived from events in the stream; MySQL is not used.
// OCC is enforced via WithExpectLastSequencePerSubject on every publish.
//
// On conflict the service falls back to a single Direct Get API call
// (stream.GetLastMsgForSubject) instead of waiting for the projection or
// creating an ephemeral consumer.  One roundtrip is sufficient because each
// event carries the cumulative NewBalance after it was applied.
type NatsOCCService struct {
	js         jetstream.JetStream
	stream     jetstream.Stream // cached for Direct Get — avoids per-call StreamInfo lookups
	proj       *natsrepo.Projection
	maxRetries int
}

// NewNatsOCCService wires up the service.  It resolves the stream handle once
// so that subsequent directGet calls do not pay the StreamInfo lookup cost.
func NewNatsOCCService(js jetstream.JetStream, proj *natsrepo.Projection, maxRetries int) (*NatsOCCService, error) {
	stream, err := js.Stream(context.Background(), natsrepo.StreamName)
	if err != nil {
		return nil, fmt.Errorf("resolve stream %s: %w", natsrepo.StreamName, err)
	}
	return &NatsOCCService{js: js, stream: stream, proj: proj, maxRetries: maxRetries}, nil
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
			NewBalance:    newBalance,
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
		// Use Direct Get (no consumer, no waiting) to read the current state
		// in a single roundtrip.
		var apiErr *jetstream.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode == 10071 {
			curBalance, curSeq, err = s.directGet(ctx, subject)
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

// directGet fetches the last event for the given wallet subject via Direct Get API
// — one NATS request-reply, no consumer creation.
// Returns the cumulative balance and stream sequence from the latest event.
func (s *NatsOCCService) directGet(ctx context.Context, subject string) (int64, uint64, error) {
	raw, err := s.stream.GetLastMsgForSubject(ctx, subject)
	if err != nil {
		return 0, 0, fmt.Errorf("direct get last msg: %w", err)
	}

	var ev natsrepo.WalletEvent
	if err := json.Unmarshal(raw.Data, &ev); err != nil {
		return 0, 0, fmt.Errorf("unmarshal event: %w", err)
	}

	return ev.NewBalance, raw.Sequence, nil
}
