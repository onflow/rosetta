package api

import (
	"context"
	"encoding/hex"

	"github.com/coinbase/rosetta-sdk-go/types"
	"github.com/onflow/rosetta/version"
)

// NetworkList implements the /network/list endpoint.
func (s *Server) NetworkList(ctx context.Context, r *types.MetadataRequest) (*types.NetworkListResponse, *types.Error) {
	return &types.NetworkListResponse{
		NetworkIdentifiers: s.networks,
	}, nil
}

// NetworkOptions implements the /network/options endpoint.
func (s *Server) NetworkOptions(ctx context.Context, r *types.NetworkRequest) (*types.NetworkOptionsResponse, *types.Error) {
	return &types.NetworkOptionsResponse{
		Allow: &types.Allow{
			CallMethods:             callMethods,
			Errors:                  serverErrors,
			HistoricalBalanceLookup: true,
			OperationStatuses: []*types.OperationStatus{
				{Status: statusSuccess, Successful: true},
				{Status: statusFailed, Successful: false},
			},
			OperationTypes: opTypes,
		},
		Version: &types.Version{
			MiddlewareVersion: types.String(version.FlowRosetta),
			NodeVersion:       version.Flow,
			RosettaVersion:    types.RosettaAPIVersion,
		},
	}, nil
}

// NetworkStatus implements the /network/status endpoint.
func (s *Server) NetworkStatus(ctx context.Context, r *types.NetworkRequest) (*types.NetworkStatusResponse, *types.Error) {
	if s.Offline {
		return nil, errOfflineMode
	}
	latest := s.Index.Latest()
	return &types.NetworkStatusResponse{
		CurrentBlockIdentifier: &types.BlockIdentifier{
			Hash:  hex.EncodeToString(latest.Hash),
			Index: int64(latest.Height),
		},
		CurrentBlockTimestamp: int64(latest.Timestamp / 1000000),
		GenesisBlockIdentifier: &types.BlockIdentifier{
			Hash:  hex.EncodeToString(s.genesis.Hash),
			Index: int64(s.genesis.Height),
		},
		Peers: []*types.Peer{},
		SyncStatus: &types.SyncStatus{
			Synced: types.Bool(s.Indexer.Synced()),
		},
	}, nil
}
