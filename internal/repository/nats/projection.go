package natsrepo

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/nats-io/nats.go/jetstream"
)

var ErrWalletNotFound = errors.New("wallet not found in projection")

// Entry holds the computed balance and the stream sequence of the last processed event.
type Entry struct {
	Balance int64
	LastSeq uint64
}

// Projection is an in-memory read model rebuilt from the WALLETS stream on startup.
type Projection struct {
	mu    sync.RWMutex
	state map[string]Entry
	ready chan struct{}
	once  sync.Once
}

func NewProjection() *Projection {
	return &Projection{
		state: make(map[string]Entry),
		ready: make(chan struct{}),
	}
}

// Get returns the current entry for walletID. Returns ErrWalletNotFound if unseen.
func (p *Projection) Get(walletID string) (Entry, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	e, ok := p.state[walletID]
	if !ok {
		return Entry{}, ErrWalletNotFound
	}
	return e, nil
}

// Ready is closed once the initial replay is complete.
func (p *Projection) Ready() <-chan struct{} {
	return p.ready
}

func (p *Projection) apply(walletID string, newBalance int64, seq uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.state[walletID]
	e.Balance = newBalance // absolute value from event, not accumulated delta
	e.LastSeq = seq
	p.state[walletID] = e
}

func (p *Projection) markReady() {
	p.once.Do(func() { close(p.ready) })
}

// RunConsumer starts an ordered consumer (DeliverAllPolicy) on wallets.> and
// replays all existing events into the projection. Marks ready when caught up.
// Blocks until ctx is cancelled.
func (p *Projection) RunConsumer(ctx context.Context, js jetstream.JetStream) error {
	si, err := js.Stream(ctx, StreamName)
	if err != nil {
		return err
	}
	info, err := si.Info(ctx)
	if err != nil {
		return err
	}
	// Empty stream — projection is ready immediately.
	if info.State.LastSeq == 0 {
		p.markReady()
	}

	cons, err := js.OrderedConsumer(ctx, StreamName, jetstream.OrderedConsumerConfig{
		FilterSubjects: []string{"wallets.>"},
		DeliverPolicy:  jetstream.DeliverAllPolicy,
	})
	if err != nil {
		return err
	}

	cc, err := cons.Consume(func(msg jetstream.Msg) {
		meta, err := msg.Metadata()
		if err != nil {
			msg.Ack() //nolint:errcheck
			return
		}

		// Subject format: "wallets.<walletID>"
		walletID := msg.Subject()[len("wallets."):]

		var ev WalletEvent
		if err := json.Unmarshal(msg.Data(), &ev); err != nil {
			msg.Ack() //nolint:errcheck
			return
		}

		p.apply(walletID, ev.NewBalance, meta.Sequence.Stream)
		msg.Ack() //nolint:errcheck

		if meta.NumPending == 0 {
			p.markReady()
		}
	})
	if err != nil {
		return err
	}

	<-ctx.Done()
	cc.Stop()
	return nil
}
