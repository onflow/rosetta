package api

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"strconv"

	"github.com/coinbase/rosetta-sdk-go/types"
	"github.com/onflow/rosetta/indexdb"
	"github.com/onflow/rosetta/model"
)

// Block implements the /block endpoint.
func (s *Server) Block(ctx context.Context, r *types.BlockRequest) (*types.BlockResponse, *types.Error) {
	if s.Offline {
		return nil, errOfflineMode
	}
	data, xerr := s.getBlock(r.BlockIdentifier)
	if xerr != nil {
		return nil, xerr
	}
	txns := []*types.Transaction{}
	for _, txn := range data.Block.Transactions {
		txns = append(txns, s.convertTransaction(txn))
	}
	return &types.BlockResponse{
		Block: &types.Block{
			BlockIdentifier: &types.BlockIdentifier{
				Hash:  hex.EncodeToString(data.Hash),
				Index: int64(data.Height),
			},
			ParentBlockIdentifier: &types.BlockIdentifier{
				Hash:  hex.EncodeToString(data.ParentHash),
				Index: int64(data.ParentHeight),
			},
			Timestamp:    int64(data.Block.Timestamp / 1000000),
			Transactions: txns,
		},
	}, nil
}

// BlockTransaction implements the /block/transaction endpoint.
func (s *Server) BlockTransaction(ctx context.Context, r *types.BlockTransactionRequest) (*types.BlockTransactionResponse, *types.Error) {
	if s.Offline {
		return nil, errOfflineMode
	}
	if r.BlockIdentifier == nil {
		return nil, errInvalidBlockIdentifier
	}
	if r.TransactionIdentifier == nil {
		return nil, errInvalidTransactionHash
	}
	txhash, err := hex.DecodeString(r.TransactionIdentifier.Hash)
	if err != nil {
		return nil, wrapErr(errInvalidTransactionHash, err)
	}
	data, xerr := s.getBlock(&types.PartialBlockIdentifier{
		Hash:  types.String(r.BlockIdentifier.Hash),
		Index: types.Int64(r.BlockIdentifier.Index),
	})
	if xerr != nil {
		return nil, xerr
	}
	for _, txn := range data.Block.Transactions {
		if bytes.Equal(txn.Hash, txhash) {
			return &types.BlockTransactionResponse{
				Transaction: s.convertTransaction(txn),
			}, nil
		}
	}
	return nil, errUnknownTransactionHash
}

func (s *Server) convertTransaction(txn *model.IndexedTransaction) *types.Transaction {
	ops := []*types.Operation{}
	for _, src := range txn.Operations {
		switch src.Type {
		case model.OperationType_FEE:
			ops = append(ops, &types.Operation{
				Account: &types.AccountIdentifier{
					Address: "0x" + hex.EncodeToString(src.Account),
				},
				Amount: &types.Amount{
					Currency: flowCurrency,
					Value:    strconv.FormatInt(-int64(src.Amount), 10),
				},
				OperationIdentifier: &types.OperationIdentifier{
					Index: int64(len(ops)),
				},
				Status: types.String(statusSuccess),
				Type:   opFee,
			})
		case model.OperationType_TRANSFER, model.OperationType_PROXY_TRANSFER:
			typ := opTransfer
			if src.Type == model.OperationType_PROXY_TRANSFER {
				typ = opProxyTransfer
			}
			sender := false
			if len(src.Account) > 0 {
				ops = append(ops, &types.Operation{
					Account: &types.AccountIdentifier{
						Address: "0x" + hex.EncodeToString(src.Account),
					},
					Amount: &types.Amount{
						Currency: flowCurrency,
						Value:    strconv.FormatInt(-int64(src.Amount), 10),
					},
					OperationIdentifier: &types.OperationIdentifier{
						Index: int64(len(ops)),
					},
					Status: types.String(statusSuccess),
					Type:   typ,
				})
				sender = true
			}
			if len(src.Receiver) > 0 {
				op := &types.Operation{
					Account: &types.AccountIdentifier{
						Address: "0x" + hex.EncodeToString(src.Receiver),
					},
					Amount: &types.Amount{
						Currency: flowCurrency,
						Value:    strconv.FormatUint(src.Amount, 10),
					},
					OperationIdentifier: &types.OperationIdentifier{
						Index: int64(len(ops)),
					},
					Status: types.String(statusSuccess),
					Type:   typ,
				}
				if sender {
					op.RelatedOperations = []*types.OperationIdentifier{{
						Index: int64(len(ops) - 1),
					}}
				}
				ops = append(ops, op)
			}
		case model.OperationType_CREATE_ACCOUNT:
			op := &types.Operation{
				Account: &types.AccountIdentifier{
					Address: "0x" + hex.EncodeToString(src.Account),
				},
				OperationIdentifier: &types.OperationIdentifier{
					Index: int64(len(ops)),
				},
				Status: types.String(statusSuccess),
				Type:   opCreateAccount,
			}
			if len(src.ProxyPublicKey) > 0 {
				op.Metadata = map[string]interface{}{
					"proxy_public_key": hex.EncodeToString(src.ProxyPublicKey),
				}
				op.Type = opCreateProxyAccount
			}
			ops = append(ops, op)
		default:
			panic(fmt.Errorf("api: unknown indexed operation type: %d (%s)", src.Type, src.Type))
		}
	}
	md := map[string]interface{}{
		"error_message": txn.ErrorMessage,
		"failed":        txn.Failed,
	}
	if len(txn.Events) > 0 {
		md["events"] = encodeTransferEvents(txn.Events)
	} else {
		md["events"] = []*transferEvent{}
	}
	return &types.Transaction{
		Metadata:   md,
		Operations: ops,
		TransactionIdentifier: &types.TransactionIdentifier{
			Hash: hex.EncodeToString(txn.Hash),
		},
	}
}

func (s *Server) getBlock(id *types.PartialBlockIdentifier) (*indexdb.BlockData, *types.Error) {
	data, xerr := s.getBlockData(id)
	if xerr != nil {
		return nil, xerr
	}
	// NOTE(tav): We use the following logic to weed out duplicate transactions
	// within a block.
	//
	// Unlike most blockchains, Flow can have multiple transactions with the
	// same hash within the same block, or even across different blocks.
	//
	// Once a transaction successfully executes, duplicates will fail due to the
	// nonce having been used already. However, an earlier transaction may fail
	// while a later duplicate might succeed due to the nonce being invalid
	// earlier and then becoming valid later due to some other transaction.
	//
	// For the purposes of Rosetta, we can only have one transaction with a
	// particular hash in each block. The code below identifies any duplicate
	// transactions within a block, and uses the first one it sees or the last
	// one with any operations.
	seen := map[string]int{}
	txns := []*model.IndexedTransaction{}
	for _, txn := range data.Block.Transactions {
		idx, ok := seen[string(txn.Hash)]
		if ok {
			if len(txn.Operations) > 0 {
				txns[idx] = txn
			}
		} else {
			seen[string(txn.Hash)] = len(txns)
			txns = append(txns, txn)
		}
	}
	data.Block.Transactions = txns
	return data, nil
}

func (s *Server) getBlockData(id *types.PartialBlockIdentifier) (*indexdb.BlockData, *types.Error) {
	if id == nil {
		return nil, errInvalidBlockIdentifier
	}
	if id.Index != nil {
		height := *id.Index
		if height < int64(s.genesis.Height) {
			return nil, errInvalidBlockIndex
		}
		if height == int64(s.genesis.Height) {
			return s.genesisBlock()
		}
		data, err := s.Index.BlockByHeight(uint64(height))
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
		return data, nil
	}
	if id.Hash != nil {
		raw := *id.Hash
		if len(raw) != 64 {
			return nil, errInvalidBlockHash
		}
		hash, err := hex.DecodeString(raw)
		if err != nil {
			return nil, wrapErr(errInvalidBlockHash, err)
		}
		if bytes.Equal(hash, s.genesis.Hash) {
			return s.genesisBlock()
		}
		data, err := s.Index.BlockByHash(hash)
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
		return data, nil
	}
	return nil, errInvalidBlockIdentifier
}

func (s *Server) genesisBlock() (*indexdb.BlockData, *types.Error) {
	return &indexdb.BlockData{
		Block: &model.IndexedBlock{
			Timestamp: s.genesis.Timestamp,
		},
		Hash:         s.genesis.Hash,
		Height:       s.genesis.Height,
		ParentHash:   s.genesis.Hash,
		ParentHeight: s.genesis.Height,
	}, nil
}

func encodeTransferEvents(src []*model.TransferEvent) []*transferEvent {
	events := make([]*transferEvent, len(src))
	for i, event := range src {
		dst := &transferEvent{
			Amount: strconv.FormatUint(event.Amount, 10),
			Type:   event.Type.String(),
		}
		if len(event.Receiver) > 0 {
			dst.Receiver = "0x" + hex.EncodeToString(event.Receiver)
		}
		if len(event.Sender) > 0 {
			dst.Sender = "0x" + hex.EncodeToString(event.Sender)
		}
		events[i] = dst
	}
	return events
}
