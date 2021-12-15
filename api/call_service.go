package api

import (
	"context"
	"encoding/hex"
	"strconv"

	"github.com/onflow/rosetta/crypto"
	"github.com/coinbase/rosetta-sdk-go/types"
	"github.com/onflow/cadence"
)

// Call implements the /call endpoint.
func (s *Server) Call(ctx context.Context, r *types.CallRequest) (*types.CallResponse, *types.Error) {
	switch r.Method {
	case callAccountBalances:
		return s.accountBalances(ctx, r.Parameters)
	case callAccountPublicKeys:
		return s.accountPublicKeys(ctx, r.Parameters)
	case callEcho:
		return s.echo(r.Parameters)
	case callLatestBlock:
		return s.latestBlock(ctx, r.Parameters)
	default:
		return nil, errNotImplemented
	}
}

func (s *Server) accountBalances(ctx context.Context, params map[string]interface{}) (*types.CallResponse, *types.Error) {
	if s.Offline {
		return nil, errOfflineMode
	}
	addr, xerr := s.getAccountParam(params)
	if xerr != nil {
		return nil, xerr
	}
	var (
		err    error
		hash   []byte
		height uint64
	)
	if param, ok := params["block_hash"]; ok {
		raw, ok := param.(string)
		if !ok {
			return nil, wrapErrorf(
				errInvalidBlockHash, "block_hash param is not a string: %v", param,
			)
		}
		hash, err = hex.DecodeString(raw)
		if err != nil {
			return nil, wrapErrorf(
				errInvalidBlockHash, "invalid block_hash value: %s", err,
			)
		}
		client := s.DataAccessNodes.Client()
		hdr, err := client.BlockHeaderByID(ctx, hash)
		if err != nil {
			return nil, wrapErr(errInternal, err)
		}
		height = hdr.Height
	} else {
		client := s.DataAccessNodes.Client()
		latest, err := client.LatestBlockHeader(ctx)
		if err != nil {
			return nil, wrapErr(errInternal, err)
		}
		hash = latest.Id
		height = latest.Height
	}
	onchain, xerr := s.getOnchainData(ctx, addr, hash, height)
	if xerr != nil {
		return nil, xerr
	}
	return &types.CallResponse{
		Idempotent: true,
		Result: map[string]interface{}{
			"default_balance": strconv.FormatUint(onchain.DefaultBalance, 10),
			"is_proxy":        onchain.IsProxy,
			"proxy_balance":   strconv.FormatUint(onchain.ProxyBalance, 10),
		},
	}, nil
}

func (s *Server) accountPublicKeys(ctx context.Context, params map[string]interface{}) (*types.CallResponse, *types.Error) {
	if s.Offline {
		return nil, errOfflineMode
	}
	addr, xerr := s.getAccountParam(params)
	if xerr != nil {
		return nil, xerr
	}
	client := s.DataAccessNodes.Client()
	acct, err := client.Account(ctx, addr)
	if err != nil {
		return nil, wrapErr(errInternal, err)
	}
	keys := []accountKey{}
	for _, key := range acct.Keys {
		if key.Revoked {
			continue
		}
		pub := key.PublicKey
		// NOTE(tav): We only convert the format of secp256k1 keys. Otherwise,
		// we just pass along the key in whatever format Flow uses.
		switch key.SignAlgo {
		case 3: // ECDSA_secp256k1
			pub, err = crypto.ConvertFlowPublicKey(pub)
			if err != nil {
				return nil, wrapErr(errInternal, err)
			}
		}
		keys = append(keys, accountKey{
			HashAlgorithm:      key.HashAlgo,
			KeyIndex:           key.Index,
			PublicKey:          hex.EncodeToString(pub),
			SequenceNumber:     key.SequenceNumber,
			SignatureAlgorithm: key.SignAlgo,
		})
	}
	if s.Chain.IsProxyContractDeployed() {
		latest, err := client.LatestBlockHeader(ctx)
		if err != nil {
			return nil, wrapErr(errInternal, err)
		}
		resp, err := client.Execute(
			ctx, latest.Id, s.scriptGetProxyPublicKey,
			[]cadence.Value{cadence.BytesToAddress(addr)},
		)
		if err != nil {
			return nil, handleExecutionErr(err, "execute get_proxy_public_key")
		}
		rawKey, ok := resp.ToGoValue().(string)
		if !ok {
			return nil, wrapErrorf(
				errInternal, "failed to convert get_proxy_public_key result to string",
			)
		}
		if rawKey != "" {
			if len(keys) > 0 {
				return nil, wrapErrorf(
					errInternal,
					"found proxy key and %d unexpected account keys",
					len(keys),
				)
			}
			pub, err := hex.DecodeString(rawKey)
			if err != nil {
				return nil, wrapErrorf(
					errInternal,
					"failed to hex-decode the get_proxy_public_key result: %s",
					err,
				)
			}
			pub, err = crypto.ConvertFlowPublicKey(pub)
			if err != nil {
				return nil, wrapErr(errInternal, err)
			}
			// NOTE(tav): For proxy accounts, we only return the public key and none
			// of the other fields.
			keys = append(keys, accountKey{
				PublicKey: hex.EncodeToString(pub),
			})
		}
	}
	return &types.CallResponse{
		Idempotent: false,
		Result: map[string]interface{}{
			"keys": keys,
		},
	}, nil
}

func (s *Server) echo(params map[string]interface{}) (*types.CallResponse, *types.Error) {
	return &types.CallResponse{
		Idempotent: true,
		Result:     params,
	}, nil
}

func (s *Server) getAccountParam(params map[string]interface{}) ([]byte, *types.Error) {
	param, ok := params["account"]
	if !ok {
		return nil, wrapErrorf(errInvalidAccountAddress, "account param is missing")
	}
	raw, ok := param.(string)
	if !ok {
		return nil, wrapErrorf(
			errInvalidAccountAddress, "account param is not a string: %v", param,
		)
	}
	return s.getAccount(raw)
}

func (s *Server) getOnchainData(ctx context.Context, addr []byte, block []byte, height uint64) (*onchainData, *types.Error) {
	client := s.DataAccessNodes.Client()
	acct, err := client.AccountAtHeight(ctx, addr, height)
	if err != nil {
		return nil, handleExecutionErr(err, "call GetAccountAtBlockHeight")
	}
	onchain := &onchainData{
		DefaultBalance: acct.Balance,
	}
	if !s.Chain.IsProxyContractDeployed() {
		return onchain, nil
	}
	resp, err := client.Execute(
		ctx, block, s.scriptGetBalances,
		[]cadence.Value{cadence.BytesToAddress(addr)},
	)
	if err != nil {
		return nil, handleExecutionErr(err, "execute get_balances")
	}
	fields, ok := resp.ToGoValue().([]interface{})
	if !ok {
		return nil, wrapErrorf(
			errInternal,
			"failed to convert get_balances result to Go slice",
		)
	}
	if len(fields) != 2 {
		return nil, wrapErrorf(
			errInternal,
			"expected 2 fields for the get_balances result: got %d",
			len(fields),
		)
	}
	onchain.IsProxy, ok = fields[0].(bool)
	if !ok {
		return nil, wrapErrorf(
			errInternal,
			"expected first field of the get_balances result to be bool: got %T",
			fields[0],
		)
	}
	onchain.ProxyBalance, ok = fields[1].(uint64)
	if !ok {
		return nil, wrapErrorf(
			errInternal,
			"expected second field of the get_balances result to be uint64: got %T",
			fields[1],
		)
	}
	return onchain, nil
}

func (s *Server) latestBlock(ctx context.Context, params map[string]interface{}) (*types.CallResponse, *types.Error) {
	client := s.DataAccessNodes.Client()
	block, err := client.LatestBlockHeader(ctx)
	if err != nil {
		return nil, wrapErr(errInternal, err)
	}
	return &types.CallResponse{
		Idempotent: false,
		Result: map[string]interface{}{
			"block_hash":      hex.EncodeToString(block.Id),
			"block_height":    strconv.FormatUint(block.Height, 10),
			"block_timestamp": strconv.FormatInt(block.Timestamp.AsTime().UnixNano(), 10),
		},
	}, nil
}
