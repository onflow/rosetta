package api

import (
	"context"
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/coinbase/rosetta-sdk-go/types"
	"github.com/onflow/cadence"
	"github.com/onflow/flow-go/model/flow"
	"github.com/onflow/rosetta/crypto"
	"github.com/onflow/rosetta/log"
)

// Call implements the /call endpoint.
func (s *Server) Call(ctx context.Context, r *types.CallRequest) (*types.CallResponse, *types.Error) {
	switch r.Method {
	case callAccountBalances:
		return s.accountBalances(ctx, r.Parameters)
	case callAccountPublicKeys:
		return s.accountPublicKeys(ctx, r.Parameters)
	case callBalanceValidationStatus:
		return s.balanceValidationStatus(ctx)
	case callEcho:
		return s.echo(r.Parameters)
	case callLatestBlock:
		return s.latestBlock(ctx, r.Parameters)
	case callListAccounts:
		return s.listAccounts(ctx)
	case callVerifyAddress:
		return s.verifyAddress(ctx, r.Parameters)
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
		err  error
		hash []byte
	)
	idempotent := false
	if param, ok := params["block_hash"]; ok {
		idempotent = true
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
	} else {
		client := s.DataAccessNodes.Client()
		latest, err := client.LatestBlockHeader(ctx)
		if err != nil {
			return nil, wrapErr(errInternal, err)
		}
		hash = latest.Id
	}
	onchain, xerr := s.getOnchainData(ctx, addr, hash)
	if xerr != nil {
		return nil, xerr
	}
	return &types.CallResponse{
		Idempotent: idempotent,
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
		rawKey, ok := resp.(cadence.String)
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
			pub, err := hex.DecodeString(string(rawKey))
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

func (s *Server) balanceValidationStatus(ctx context.Context) (*types.CallResponse, *types.Error) {
	v := s.getValidationStatus()
	switch v.status {
	case "failure":
		return &types.CallResponse{
			Result: map[string]interface{}{
				"error":  v.err,
				"status": v.status,
			},
		}, nil
	case "in_progress":
		return &types.CallResponse{
			Result: map[string]interface{}{
				"accounts": v.accounts,
				"checked":  v.checked,
				"status":   v.status,
			},
		}, nil
	case "not_started":
		return &types.CallResponse{
			Result: map[string]interface{}{
				"status": v.status,
			},
		}, nil
	case "success":
		return &types.CallResponse{
			Result: map[string]interface{}{
				"accounts": v.accounts,
				"status":   v.status,
			},
		}, nil
	default:
		log.Fatalf("Unsupported validation status %q", v.status)
		panic("unreachable code")
	}
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

func (s *Server) getOnchainData(ctx context.Context, addr []byte, block []byte) (*onchainData, *types.Error) {
	client := s.DataAccessNodes.Client()
	script := s.scriptGetBalancesBasic
	scriptName := "get_balances_basic"
	if s.Chain.IsProxyContractDeployed() {
		script = s.scriptGetBalances
		scriptName = "get_balances"
	}
	resp, err := client.Execute(
		ctx, block, script,
		[]cadence.Value{cadence.BytesToAddress(addr)},
	)
	if err != nil {
		return nil, handleExecutionErr(err, "execute "+scriptName)
	}
	arrayValue, ok := resp.(cadence.Array)
	if !ok {
		return nil, wrapErrorf(
			errInternal,
			"failed to convert %s result to array",
			scriptName,
		)
	}
	fields := arrayValue.Values
	if len(fields) != 3 {
		return nil, wrapErrorf(
			errInternal,
			"expected 3 fields for the %s result: got %d",
			scriptName, len(arrayValue.Values),
		)
	}
	onchain := &onchainData{}
	defaultBalance, ok := fields[0].(cadence.UInt64)
	if !ok {
		return nil, wrapErrorf(
			errInternal,
			"expected first field of the %s result to be uint64: got %T",
			scriptName, fields[0],
		)
	}
	onchain.DefaultBalance = uint64(defaultBalance)
	isProxy, ok := fields[1].(cadence.Bool)
	if !ok {
		return nil, wrapErrorf(
			errInternal,
			"expected second field of the %s result to be bool: got %T",
			scriptName, fields[1],
		)
	}
	onchain.IsProxy = bool(isProxy)
	proxyBalance, ok := fields[2].(cadence.UInt64)
	if !ok {
		return nil, wrapErrorf(
			errInternal,
			"expected third field of the %s result to be uint64: got %T",
			scriptName, fields[2],
		)
	}
	onchain.ProxyBalance = uint64(proxyBalance)
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

func (s *Server) listAccounts(ctx context.Context) (*types.CallResponse, *types.Error) {
	accts, err := s.Index.AccountsInfo()
	if err != nil {
		return nil, wrapErr(errInternal, err)
	}
	return &types.CallResponse{
		Idempotent: false,
		Result: map[string]interface{}{
			"accounts": accts,
		},
	}, nil
}

func (s *Server) verifyAddress(ctx context.Context, params map[string]interface{}) (*types.CallResponse, *types.Error) {
	if s.Offline {
		return nil, errOfflineMode
	}
	acct, xerr := s.getAccountParam(params)
	if xerr != nil {
		return &types.CallResponse{
			Result: map[string]interface{}{
				"error": "invalid Flow address format",
				"valid": false,
			},
		}, nil
	}
	addr := flow.Address{}
	copy(addr[:], acct)
	chainID := flow.ChainID("flow-" + s.Chain.Network)
	if !chainID.Chain().IsValid(addr) {
		return &types.CallResponse{
			Result: map[string]interface{}{
				"error": "invalid address for Flow " + s.Chain.Network,
				"valid": false,
			},
		}, nil
	}
	client := s.DataAccessNodes.Client()
	_, err := client.Account(ctx, acct)
	if err != nil {
		if strings.Contains(err.Error(), "account not found") {
			return &types.CallResponse{
				Result: map[string]interface{}{
					"error": "account not found on Flow " + s.Chain.Network,
					"valid": false,
				},
			}, nil
		}
		return nil, wrapErr(errInternal, err)
	}
	return &types.CallResponse{
		Result: map[string]interface{}{
			"valid": true,
		},
	}, nil
}
