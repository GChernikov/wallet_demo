package model

import "time"

type Balance struct {
	WalletUUID []byte
	Balance    int64
	Version    int64
	UpdatedAt  time.Time
}

type Transaction struct {
	ID            int64
	TransactionID []byte // BINARY(16)
	WalletUUID    []byte // BINARY(16)
	Amount        int64
	EventType     string
	CreatedAt     time.Time
}

type EventOutbox struct {
	ID           int64
	UUID         string // CHAR(36)
	Topic        string
	Payload      []byte
	Dispatched   bool
	CreatedAt    int64
}

type UpdateRequest struct {
	WalletUUID    string `json:"wallet_uuid"`
	Amount        int64  `json:"amount"`
	TransactionID string `json:"transaction_id,omitempty"` // client-generated UUID; doubles as idempotency key
}

type UpdateResponse struct {
	WalletUUID    string `json:"wallet_uuid"`
	Balance       int64  `json:"balance"`
	Mode          string `json:"mode"`
	TransactionID string `json:"transaction_id"`
	OutboxEventID string `json:"outbox_event_id"`
}

type ConflictError struct{}

func (e *ConflictError) Error() string {
	return "optimistic lock conflict: max retries exceeded"
}
