package natsrepo

import (
	"context"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const StreamName = "WALLETS"

// WalletEvent is the event payload stored in the stream (a delta, not an absolute balance).
type WalletEvent struct {
	Amount        int64  `json:"amount"`
	TransactionID string `json:"transaction_id"`
	EventType     string `json:"event_type"`
}

// Connect returns a *nats.Conn and jetstream.JetStream.
// Creates or updates the WALLETS stream (MemoryStorage, subjects: wallets.>).
func Connect(url string) (*nats.Conn, jetstream.JetStream, error) {
	nc, err := nats.Connect(url)
	if err != nil {
		return nil, nil, err
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, nil, err
	}

	_, err = js.CreateOrUpdateStream(context.Background(), jetstream.StreamConfig{
		Name:     StreamName,
		Subjects: []string{"wallets.>"},
		Storage:  jetstream.MemoryStorage,
	})
	if err != nil {
		nc.Close()
		return nil, nil, err
	}

	return nc, js, nil
}
