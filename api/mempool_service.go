package api

import (
	"context"

	"github.com/coinbase/rosetta-sdk-go/types"
)

// Mempool implements the /mempool endpoint.
func (s *Server) Mempool(ctx context.Context, r *types.NetworkRequest) (*types.MempoolResponse, *types.Error) {
	if s.Offline {
		return nil, errOfflineMode
	}
	return &types.MempoolResponse{
		TransactionIdentifiers: []*types.TransactionIdentifier{},
	}, nil
}

// MempoolTransaction implements the /mempool/transaction endpoint.
func (s *Server) MempoolTransaction(ctx context.Context, r *types.MempoolTransactionRequest) (*types.MempoolTransactionResponse, *types.Error) {
	if s.Offline {
		return nil, errOfflineMode
	}
	return nil, errTransactionNotInMempool
}
