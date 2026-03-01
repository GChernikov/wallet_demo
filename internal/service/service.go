package service

import (
	"context"

	"github.com/gchernikov/wallet_demo/internal/model"
)

// BalanceService is the interface for balance update strategies.
type BalanceService interface {
	Update(ctx context.Context, req model.UpdateRequest) (model.UpdateResponse, error)
}
