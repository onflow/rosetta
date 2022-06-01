package api

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"strconv"

	"github.com/coinbase/rosetta-sdk-go/types"
	"github.com/onflow/cadence"
	"github.com/onflow/rosetta/indexdb"
	"github.com/onflow/rosetta/model"
	"github.com/onflow/rosetta/trace"
)

// AccountBalance implements the /account/balance endpoint.
func (s *Server) AccountBalance(ctx context.Context, r *types.AccountBalanceRequest) (*types.AccountBalanceResponse, *types.Error) {
	if r.AccountIdentifier == nil {
		return nil, errInvalidAccountAddress
	}
	acct, xerr := s.getAccount(r.AccountIdentifier.Address)
	if xerr != nil {
		return nil, xerr
	}
	trace.SetAttributes(
		ctx,
		trace.String("flow_account", r.AccountIdentifier.Address),
	)
	for _, currency := range r.Currencies {
		if currency == nil ||
			currency.Decimals != flowCurrency.Decimals ||
			currency.Symbol != flowCurrency.Symbol {
			return nil, errInvalidCurrency
		}
	}
	if r.BlockIdentifier == nil {
		block := s.Index.Latest()
		if block.Equal(s.genesis) {
			return s.genesisBalance()
		}
		if !s.Indexer.Synced() {
			return nil, wrapErrorf(
				errBlockNotIndexed, "node is not synced to tip",
			)
		}
		seq, err := s.getSequenceNumber(ctx, acct, block)
		if err != nil {
			return nil, wrapErrorf(
				errInternal,
				"failed to get sequence number for %x: %s", acct, err,
			)
		}
		v, err := s.Index.BalanceByHash(acct, block.Hash)
		if err != nil {
			if err == indexdb.ErrBlockNotIndexed {
				return nil, wrapErrorf(
					errBlockNotIndexed,
					"failed to lookup indexed data for block %x at height %d",
					block.Hash, block.Height,
				)
			}
			return nil, wrapErr(errInternal, err)
		}
		resp := s.balanceResponse(v)
		resp.Metadata = map[string]interface{}{
			"sequence_number": strconv.FormatUint(seq, 10),
		}
		return resp, nil
	}
	if r.BlockIdentifier.Index != nil {
		height := *r.BlockIdentifier.Index
		if height < int64(s.genesis.Height) {
			return nil, errInvalidBlockIndex
		}
		if height == int64(s.genesis.Height) {
			return s.genesisBalance()
		}
		v, err := s.Index.BalanceByHeight(acct, uint64(height))
		if err != nil {
			if err == indexdb.ErrBlockNotIndexed {
				return nil, wrapErrorf(
					errBlockNotIndexed,
					"failed to lookup indexed data for block at height %d",
					height,
				)
			}
			return nil, wrapErr(errInternal, err)
		}
		return s.balanceResponse(v), nil
	}
	if r.BlockIdentifier.Hash != nil {
		raw := *r.BlockIdentifier.Hash
		if len(raw) != 64 {
			return nil, errInvalidBlockHash
		}
		hash, err := hex.DecodeString(raw)
		if err != nil {
			return nil, wrapErr(errInvalidBlockHash, err)
		}
		if bytes.Equal(hash, s.genesis.Hash) {
			return s.genesisBalance()
		}
		v, err := s.Index.BalanceByHash(acct, hash)
		if err != nil {
			if err == indexdb.ErrBlockNotIndexed {
				return nil, wrapErrorf(
					errBlockNotIndexed,
					"failed to lookup indexed data for block %x",
					hash,
				)
			}
			return nil, wrapErr(errInternal, err)
		}
		return s.balanceResponse(v), nil
	}
	return nil, errInvalidBlockIdentifier
}

// AccountCoins implements the /account/coins endpoint.
func (s *Server) AccountCoins(ctx context.Context, r *types.AccountCoinsRequest) (*types.AccountCoinsResponse, *types.Error) {
	if s.Offline {
		return nil, errOfflineMode
	}
	return nil, errNotImplemented
}

func (s *Server) balanceResponse(v *indexdb.BalanceData) *types.AccountBalanceResponse {
	return &types.AccountBalanceResponse{
		BlockIdentifier: &types.BlockIdentifier{
			Hash:  hex.EncodeToString(v.Hash),
			Index: int64(v.Height),
		},
		Balances: []*types.Amount{{
			Currency: flowCurrency,
			Value:    strconv.FormatUint(v.Balance, 10),
		}},
	}
}

func (s *Server) genesisBalance() (*types.AccountBalanceResponse, *types.Error) {
	return &types.AccountBalanceResponse{
		BlockIdentifier: &types.BlockIdentifier{
			Hash:  hex.EncodeToString(s.genesis.Hash),
			Index: int64(s.genesis.Height),
		},
		Balances: []*types.Amount{{
			Currency: flowCurrency,
			Value:    "0",
		}},
	}, nil
}

func (s *Server) getSequenceNumber(ctx context.Context, addr []byte, block *model.BlockMeta) (uint64, error) {
	client := s.DataAccessNodes.Client()
	if s.Chain.IsProxyContractDeployed() {
		resp, err := client.Execute(
			ctx, block.Hash, s.scriptGetProxyNonce,
			[]cadence.Value{cadence.BytesToAddress(addr)},
		)
		if err != nil {
			return 0, err
		}
		nonce, ok := resp.ToGoValue().(int64)
		if !ok {
			return 0, fmt.Errorf("failed to convert get_proxy_nonce result to int64")
		}
		if nonce >= 0 {
			return uint64(nonce), nil
		}
	}
	// NOTE(tav): If it's not a proxy account, we fetch the account sequence
	// number for it's key instead.
	acct, err := client.AccountAtHeight(ctx, addr, block.Height)
	if err != nil {
		return 0, err
	}
	if len(acct.Keys) != 1 {
		return 0, fmt.Errorf("found %d keys on the account: expected just one", len(acct.Keys))
	}
	return uint64(acct.Keys[0].SequenceNumber), nil
}
