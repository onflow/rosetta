package api

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/coinbase/rosetta-sdk-go/parser"
	"github.com/coinbase/rosetta-sdk-go/types"
	legacyProto "github.com/golang/protobuf/proto"
	"github.com/onflow/cadence"
	jsoncdc "github.com/onflow/cadence/encoding/json"
	"github.com/onflow/flow/protobuf/go/flow/entities"
	"github.com/onflow/rosetta/access"
	"github.com/onflow/rosetta/crypto"
	"github.com/onflow/rosetta/fees"
	"github.com/onflow/rosetta/log"
	"github.com/onflow/rosetta/model"
	"github.com/onflow/rosetta/trace"
	"golang.org/x/crypto/sha3"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const (
	txnTypeFlow  = 1
	txnTypeInner = 2
)

// ConstructionCombine implements the /construction/combine endpoint.
func (s *Server) ConstructionCombine(ctx context.Context, r *types.ConstructionCombineRequest) (*types.ConstructionCombineResponse, *types.Error) {
	if len(r.Signatures) != 1 || r.Signatures[0] == nil {
		return nil, errInvalidSignature
	}
	constructOpts := ""
	split := strings.Split(r.UnsignedTransaction, ":")
	if len(split) == 2 {
		constructOpts = split[1]
	}
	txn, inner, xerr := s.decodeTransaction(r.UnsignedTransaction, false)
	if xerr != nil {
		return nil, xerr
	}
	// NOTE(tav): We truncate the signature passed in by the caller, in case
	// they accidentally send us signatures in the ecdsa_recovery format.
	sig := r.Signatures[0].Bytes[:64]
	if txn != nil {
		txn.EnvelopeSignatures = []*entities.Transaction_Signature{{
			Address:   txn.Payer,
			KeyId:     txn.ProposalKey.KeyId,
			Signature: sig,
		}}
		enc, err := proto.Marshal(legacyProto.MessageV2(txn))
		if err != nil {
			return nil, wrapErr(errProtobuf, err)
		}
		enc = append([]byte{txnTypeFlow}, enc...)
		return &types.ConstructionCombineResponse{
			SignedTransaction: hex.EncodeToString(enc) + ":" + constructOpts,
		}, nil
	}
	enc := append(inner.raw, sig...)
	return &types.ConstructionCombineResponse{
		SignedTransaction: hex.EncodeToString(enc),
	}, nil
}

// ConstructionDerive implements the /construction/derive endpoint.
func (s *Server) ConstructionDerive(ctx context.Context, r *types.ConstructionDeriveRequest) (*types.ConstructionDeriveResponse, *types.Error) {
	return &types.ConstructionDeriveResponse{}, nil
}

// ConstructionHash implements the /construction/hash endpoint.
func (s *Server) ConstructionHash(ctx context.Context, r *types.ConstructionHashRequest) (*types.TransactionIdentifierResponse, *types.Error) {
	txn, inner, xerr := s.decodeTransaction(r.SignedTransaction, true)
	if xerr != nil {
		return nil, xerr
	}
	if txn != nil {
		hash, err := model.TransactionHash(txn)
		if err != nil {
			return nil, wrapErr(errInternal, err)
		}
		return &types.TransactionIdentifierResponse{
			TransactionIdentifier: &types.TransactionIdentifier{
				Hash: hex.EncodeToString(hash),
			},
		}, nil
	}
	// NOTE(tav): For inner transactions, we generate a synthetic transaction
	// hash.
	hasher := sha3.New256()
	_, _ = hasher.Write([]byte("inner-transaction-tag"))
	_, _ = hasher.Write(inner.raw)
	return &types.TransactionIdentifierResponse{
		TransactionIdentifier: &types.TransactionIdentifier{
			Hash: hex.EncodeToString(hasher.Sum(nil)),
		},
	}, nil
}

// ConstructionMetadata implements the /construction/metadata endpoint.
func (s *Server) ConstructionMetadata(ctx context.Context, r *types.ConstructionMetadataRequest) (*types.ConstructionMetadataResponse, *types.Error) {
	if s.Offline {
		return nil, errOfflineMode
	}
	_, opts, xerr := s.getConstructOpts(r.Options)
	if xerr != nil {
		return nil, xerr
	}
	height := uint64(0)
	client := s.ConstructionAccessNodes.Client()
	hdr, err := client.LatestBlockHeader(ctx)
	if err != nil {
		return nil, wrapErr(errInternal, err)
	}
	if opts.Inner {
		if opts.SequenceNumber == -1 {
			resp, err := client.Execute(
				ctx, hdr.Id, s.scriptGetProxyNonce,
				[]cadence.Value{cadence.BytesToAddress(opts.Payer)},
			)
			if err != nil {
				return nil, wrapErrorf(
					errFailedAccessAPICall,
					"failed to execute get_proxy_nonce: %s", err,
				)
			}
			nonce, ok := resp.(cadence.Int64)
			if !ok {
				return nil, wrapErrorf(
					errInternal,
					"expected type cadence.Int64 for nonce, got %T", resp,
				)
			}
			opts.SequenceNumber = int64(nonce)
		}
	} else {
		latest := s.Index.Latest()
		if hdr.Height > latest.Height {
			height = hdr.Height
			opts.BlockHash = hdr.Id
			opts.BlockHashFromRemote = true
			opts.BlockHashRemoteServer = client.ServerAddress()
		} else {
			height = latest.Height
			opts.BlockHash = latest.Hash
		}
		resp, err := client.Execute(
			ctx, hdr.Id, s.scriptComputeFees,
			[]cadence.Value{
				cadence.UFix64(opts.InclusionEffort),
				cadence.UFix64(opts.ExecutionEffort),
			},
		)
		if err != nil {
			return nil, wrapErrorf(
				errFailedAccessAPICall,
				"failed to execute compute_fees: %s", err,
			)
		}
		cost, ok := resp.(cadence.UFix64)
		if !ok {
			return nil, wrapErrorf(
				errInternal,
				"expected type cadence.UFix64 for cost, got %T", resp,
			)
		}
		opts.Fees = uint64(cost) + (opts.NewAccounts * fees.MinimumAccountBalance)
		trace.SetAttributes(
			ctx,
			trace.String("access_api_server", client.ServerAddress()),
			trace.Int64("latest_height_local", int64(latest.Height)),
			trace.Int64("latest_height_remote", int64(hdr.Height)),
		)
		acct, err := client.AccountAtHeight(ctx, opts.Payer, height)
		if err != nil {
			return nil, wrapErr(errInternal, err)
		}
		count := 0
		if opts.SequenceNumber == -1 {
			found := false
			for _, key := range acct.Keys {
				if key.Index == opts.KeyId {
					if key.Revoked {
						return nil, wrapErrorf(
							errInvalidKeyID,
							"key %d has been revoked",
							opts.KeyId,
						)
					}
					opts.SequenceNumber = int64(key.SequenceNumber)
					count++
					found = true
				} else if !key.Revoked {
					count++
				}
			}
			if !found {
				return nil, wrapErrorf(
					errInvalidKeyID,
					"could not find key ID %d out of %d keys",
					opts.KeyId, len(acct.Keys),
				)
			}
		} else {
			for _, key := range acct.Keys {
				if !key.Revoked {
					count++
				}
			}
		}
		if count != 1 {
			return nil, wrapErrorf(
				errInvalidNumberOfAccountKeys,
				"found %d valid keys out of total %d keys",
				count, len(acct.Keys),
			)
		}
	}
	data, err := proto.Marshal(opts)
	if err != nil {
		return nil, wrapErr(errProtobuf, err)
	}
	return &types.ConstructionMetadataResponse{
		Metadata: map[string]interface{}{
			"height":   strconv.FormatUint(height, 10),
			"protobuf": hex.EncodeToString(data),
		},
		SuggestedFee: []*types.Amount{
			{Currency: flowCurrency, Value: strconv.FormatUint(opts.Fees, 10)},
		},
	}, nil
}

// ConstructionParse implements the /construction/parse endpoint.
func (s *Server) ConstructionParse(ctx context.Context, r *types.ConstructionParseRequest) (*types.ConstructionParseResponse, *types.Error) {
	txn, inner, xerr := s.decodeTransaction(r.Transaction, r.Signed)
	if xerr != nil {
		return nil, xerr
	}
	if txn != nil {
		if bytes.Equal(txn.Script, s.scriptBasicTransfer) {
			return decodeTransferOps(txn, false, r.Signed)
		}
		if bytes.Equal(txn.Script, s.scriptProxyTransfer) {
			return decodeTransferOps(txn, true, r.Signed)
		}
		if bytes.Equal(txn.Script, s.scriptCreateAccount) {
			return decodeCreateAccountOps(txn, false, r.Signed)
		}
		if bytes.Equal(txn.Script, s.scriptCreateProxyAccount) {
			return decodeCreateAccountOps(txn, true, r.Signed)
		}
		if bytes.Equal(txn.Script, s.scriptSetContract) {
			return decodeContractOps(txn, r.Signed)
		}
		return nil, errInvalidTransactionPayload
	}
	ops := []*types.Operation{
		{
			Account: &types.AccountIdentifier{
				Address: "0x" + hex.EncodeToString(inner.sender),
			},
			Amount: &types.Amount{
				Currency: flowCurrency,
				Value:    "-" + strconv.FormatUint(inner.amount, 10),
			},
			OperationIdentifier: &types.OperationIdentifier{
				Index: 0,
			},
			Type: opProxyTransferInner,
		},
		{
			Account: &types.AccountIdentifier{
				Address: "0x" + hex.EncodeToString(inner.receiver),
			},
			Amount: &types.Amount{
				Currency: flowCurrency,
				Value:    strconv.FormatUint(inner.amount, 10),
			},
			OperationIdentifier: &types.OperationIdentifier{
				Index: 1,
			},
			RelatedOperations: []*types.OperationIdentifier{{
				Index: 0,
			}},
			Type: opProxyTransferInner,
		},
	}
	txn = &entities.Transaction{
		Payer: inner.sender,
	}
	return txnOps(txn, ops, r.Signed)
}

// ConstructionPayloads implements the /construction/payloads endpoint.
func (s *Server) ConstructionPayloads(ctx context.Context, r *types.ConstructionPayloadsRequest) (*types.ConstructionPayloadsResponse, *types.Error) {
	rawOpts, opts, xerr := s.getConstructOpts(r.Metadata)
	if xerr != nil {
		return nil, xerr
	}
	payer := opts.Payer
	intent, xerr := s.parseIntent(r.Operations)
	if xerr != nil {
		return nil, xerr
	}
	var (
		args   [][]byte
		script []byte
	)
	if opts.SequenceNumber < 0 {
		return nil, wrapErrorf(
			errInvalidConstructOptions,
			"invalid sequence_number from construct options: %d",
			opts.SequenceNumber,
		)
	}
	if intent.inner {
		enc := make([]byte, 33)
		enc[0] = txnTypeInner
		binary.BigEndian.PutUint64(enc[1:], intent.amount)
		binary.BigEndian.PutUint64(enc[9:], uint64(opts.SequenceNumber))
		copy(enc[17:], intent.receiver)
		copy(enc[25:], intent.sender)
		hasher := sha3.New256()
		_, _ = hasher.Write(userTag)
		_, _ = hasher.Write(intent.receiver)
		_, _ = hasher.Write(enc[1:17])
		return &types.ConstructionPayloadsResponse{
			Payloads: []*types.SigningPayload{{
				AccountIdentifier: &types.AccountIdentifier{
					Address: "0x" + hex.EncodeToString(intent.sender),
				},
				Bytes:         hasher.Sum(nil),
				SignatureType: types.Ecdsa,
			}},
			UnsignedTransaction: hex.EncodeToString(enc),
		}, nil
	}
	if len(intent.keys) > 0 {
		if intent.proxy {
			script = s.scriptCreateProxyAccount
			arg, err := jsoncdc.Encode(cadence.String(intent.keys[0]))
			if err != nil {
				return nil, wrapErrorf(
					errInvalidOpsIntent, "unable to JSON encode public key: %s", err,
				)
			}
			args = append(args, arg)
		} else {
			script = s.scriptCreateAccount
			ckeys := make([]cadence.Value, len(intent.keys))
			for i, key := range intent.keys {
				ckeys[i] = cadence.String(key)
			}
			keys := cadence.NewArray(ckeys)
			arg, err := jsoncdc.Encode(keys)
			if err != nil {
				return nil, wrapErrorf(
					errInvalidOpsIntent, "unable to JSON encode public keys: %s", err,
				)
			}
			args = append(args, arg)
		}
	} else if intent.contractCode != "" {
		script = s.scriptSetContract
		arg, err := jsoncdc.Encode(cadence.Bool(intent.contractUpdate))
		if err != nil {
			return nil, wrapErrorf(
				errInvalidOpsIntent,
				"unable to JSON encode contract deploy/update flag: %s",
				err,
			)
		}
		args = append(args, arg)
		arg, err = jsoncdc.Encode(cadence.String(intent.contractName))
		if err != nil {
			return nil, wrapErrorf(
				errInvalidOpsIntent, "unable to JSON encode contract name: %s", err,
			)
		}
		args = append(args, arg)
		arg, err = jsoncdc.Encode(cadence.String(intent.contractCode))
		if err != nil {
			return nil, wrapErrorf(
				errInvalidOpsIntent, "unable to JSON encode contract code: %s", err,
			)
		}
		args = append(args, arg)
		arg, err = jsoncdc.Encode(cadence.NewInt(int(intent.prevKeyIndex)))
		if err != nil {
			return nil, wrapErrorf(
				errInvalidOpsIntent, "unable to JSON encode prev key index: %s", err,
			)
		}
		args = append(args, arg)
		arg, err = jsoncdc.Encode(cadence.String(intent.newKey))
		if err != nil {
			return nil, wrapErrorf(
				errInvalidOpsIntent, "unable to JSON encode new key: %s", err,
			)
		}
		args = append(args, arg)
		arg, err = jsoncdc.Encode(cadence.String(intent.keyMessage))
		if err != nil {
			return nil, wrapErrorf(
				errInvalidOpsIntent, "unable to JSON encode key message: %s", err,
			)
		}
		args = append(args, arg)
		arg, err = jsoncdc.Encode(cadence.String(intent.keySignature))
		if err != nil {
			return nil, wrapErrorf(
				errInvalidOpsIntent, "unable to JSON encode key signature: %s", err,
			)
		}
		args = append(args, arg)
		arg, err = jsoncdc.Encode(cadence.String(intent.keyMetadata))
		if err != nil {
			return nil, wrapErrorf(
				errInvalidOpsIntent, "unable to JSON encode key metadata: %s", err,
			)
		}
		args = append(args, arg)
	} else {
		if intent.proxy {
			script = s.scriptProxyTransfer
			arg, err := jsoncdc.Encode(cadence.BytesToAddress(intent.sender))
			if err != nil {
				return nil, wrapErrorf(
					errInvalidOpsIntent, "unable to JSON encode sender: %s", err,
				)
			}
			args = append(args, arg)
		} else {
			script = s.scriptBasicTransfer
		}
		arg, err := jsoncdc.Encode(cadence.BytesToAddress(intent.receiver))
		if err != nil {
			return nil, wrapErrorf(
				errInvalidOpsIntent, "unable to JSON encode receiver: %s", err,
			)
		}
		args = append(args, arg)
		arg, err = jsoncdc.Encode(cadence.UFix64(intent.amount))
		if err != nil {
			return nil, wrapErrorf(
				errInvalidOpsIntent, "unable to JSON encode amount: %s", err,
			)
		}
		args = append(args, arg)
		if intent.proxy {
			if len(opts.ProxyTransferPayload) == 0 {
				return nil, wrapErrorf(
					errInvalidConstructOptions,
					"proxy_transfer_payload was not specified in the /construction/preprocess metadata",
				)
			}
			_, inner, xerr := s.decodeTransaction(opts.ProxyTransferPayload, true)
			if xerr != nil {
				return nil, xerr
			}
			arg, err = jsoncdc.Encode(cadence.Int64(inner.nonce))
			if err != nil {
				return nil, wrapErrorf(
					errInvalidOpsIntent, "unable to JSON encode nonce: %s", err,
				)
			}
			args = append(args, arg)
			sig := hex.EncodeToString(inner.raw[33:])
			arg, err = jsoncdc.Encode(cadence.String(sig))
			if err != nil {
				return nil, wrapErrorf(
					errInvalidOpsIntent, "unable to JSON encode signature: %s", err,
				)
			}
			args = append(args, arg)
		}
	}
	txn := &entities.Transaction{
		Arguments:   args,
		Authorizers: [][]byte{payer},
		GasLimit:    9999,
		Payer:       payer,
		ProposalKey: &entities.Transaction_ProposalKey{
			Address:        payer,
			KeyId:          opts.KeyId,
			SequenceNumber: uint64(opts.SequenceNumber),
		},
		ReferenceBlockId: opts.BlockHash,
		Script:           script,
	}
	hash, err := model.TransactionEnvelopeHash(txn)
	if err != nil {
		return nil, wrapErr(errInternal, err)
	}
	enc, err := proto.Marshal(legacyProto.MessageV2(txn))
	if err != nil {
		return nil, wrapErr(errProtobuf, err)
	}
	enc = append([]byte{txnTypeFlow}, enc...)
	return &types.ConstructionPayloadsResponse{
		Payloads: []*types.SigningPayload{{
			AccountIdentifier: &types.AccountIdentifier{
				Address: "0x" + hex.EncodeToString(payer),
			},
			Bytes:         hash,
			SignatureType: types.Ecdsa,
		}},
		UnsignedTransaction: hex.EncodeToString(enc) + ":" + rawOpts,
	}, nil
}

// ConstructionPreprocess implements the /construction/preprocess endpoint.
func (s *Server) ConstructionPreprocess(ctx context.Context, r *types.ConstructionPreprocessRequest) (*types.ConstructionPreprocessResponse, *types.Error) {
	intent, xerr := s.parseIntent(r.Operations)
	if xerr != nil {
		return nil, xerr
	}
	// NOTE(tav): We explicitly error on transfers to the fee address so as to
	// simplify our event processing logic.
	if bytes.Equal(intent.receiver, s.feeAddr) {
		return nil, wrapErrorf(
			errInvalidOpsIntent,
			"cannot make transfers to the fee address: 0x%s",
			s.Chain.Contracts.FlowFees,
		)
	}
	opts := &model.ConstructOpts{
		InclusionEffort: fees.InclusionEffort,
		Inner:           intent.inner,
		NewAccounts:     uint64(len(intent.keys)),
		SequenceNumber:  -1,
	}
	if intent.proxy && len(intent.keys) == 0 {
		val, ok := r.Metadata["proxy_transfer_payload"]
		if !ok {
			return nil, wrapErrorf(
				errInvalidMetadataField,
				"proxy_transfer_payload metadata field is missing",
			)
		}
		raw, ok := val.(string)
		if !ok {
			return nil, wrapErrorf(
				errInvalidMetadataField,
				"proxy_transfer_payload metadata field is not a string: %v", val,
			)
		}
		opts.ProxyTransferPayload = raw
	}
	if intent.contractCode != "" {
		opts.KeyId = intent.prevKeyIndex
		if intent.contractUpdate {
			opts.ExecutionEffort = fees.UpdateContractEffort
		} else {
			opts.ExecutionEffort = fees.DeployContractEffort
		}
	} else if len(intent.keys) > 0 {
		if intent.proxy {
			opts.ExecutionEffort = fees.CreateProxyAccountEffort
		} else {
			opts.ExecutionEffort = uint64(fees.CreateAccountEffort * len(intent.keys))
		}
	} else {
		if intent.proxy {
			opts.ExecutionEffort = fees.FlowTransferEffort
		} else {
			opts.ExecutionEffort = fees.ProxyTransferEffort
		}
	}
	if val, ok := r.Metadata["sequence_number"]; ok {
		raw, ok := val.(string)
		if !ok {
			return nil, wrapErrorf(
				errInvalidMetadataField,
				"sequence_number metadata field is not a string: %v", val,
			)
		}
		seq, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, wrapErrorf(
				errInvalidMetadataField,
				"invalid sequence_number value: %q: %s", val, err,
			)
		}
		if seq < 0 {
			return nil, wrapErrorf(
				errInvalidMetadataField,
				"invalid sequence_number value: %d", seq,
			)
		}
		opts.SequenceNumber = seq
	}
	// NOTE(tav): We override the metadata.payer with the sender account
	// address, if it is a proxy_transfer_inner operation.
	if opts.Inner {
		opts.Payer = intent.sender
	} else {
		val, ok := r.Metadata["payer"]
		if !ok {
			return nil, wrapErrorf(
				errInvalidMetadataField,
				"payer metadata field is missing",
			)
		}
		raw, ok := val.(string)
		if !ok {
			return nil, wrapErrorf(
				errInvalidMetadataField,
				"payer metadata field is not a string: %v", val,
			)
		}
		payer, xerr := s.getAccount(raw)
		if xerr != nil {
			return nil, wrapErrorf(
				errInvalidMetadataField,
				"invalid payer value: %s", formatErr(xerr),
			)
		}
		opts.Payer = payer
	}
	data, err := proto.Marshal(opts)
	if err != nil {
		return nil, wrapErr(errProtobuf, err)
	}
	return &types.ConstructionPreprocessResponse{
		Options: map[string]interface{}{
			"protobuf": hex.EncodeToString(data),
		},
	}, nil
}

// ConstructionSubmit implements the /construction/submit endpoint.
func (s *Server) ConstructionSubmit(ctx context.Context, r *types.ConstructionSubmitRequest) (*types.TransactionIdentifierResponse, *types.Error) {
	if s.Offline {
		return nil, errOfflineMode
	}
	txn, inner, xerr := s.decodeTransaction(r.SignedTransaction, true)
	if xerr != nil {
		return nil, xerr
	}
	if inner != nil {
		return nil, wrapErrorf(
			errInvalidTransactionPayload,
			"inner transactions cannot be submitted",
		)
	}
	client := s.ConstructionAccessNodes.Client()
	err := client.Ping(ctx)
	if err != nil {
		return nil, wrapErr(errAccessNodeInaccessible, err)
	}
	hash, err := client.SendTransaction(ctx, txn)
	if err != nil {
		txnHash := ""
		hash, herr := model.TransactionHash(txn)
		if herr == nil {
			txnHash = hex.EncodeToString(hash)
		} else {
			log.Errorf("Unable to derive transaction hash: %s", herr)
		}
		if access.IsRateLimited(err) {
			return nil, wrapErrorf(
				errRateLimited,
				"failed to submit txn %s: %s",
				txnHash, err,
			)
		}
		switch status.Code(err) {
		case codes.InvalidArgument:
			// NOTE(tav): The substring match below needs to be kept in sync
			// with upstream responses.
			if strings.Contains(err.Error(), "transaction is expired") {
				opts := decodeOptsFromTransaction(r.SignedTransaction)
				if opts != nil {
					src := "locally indexed data"
					if opts.BlockHashFromRemote {
						src = fmt.Sprintf("remote server %q", opts.BlockHashRemoteServer)
					}
					return nil, wrapErrorf(
						errTransactionExpired,
						"expired transaction %s: %s: reference block hash used from %s",
						txnHash, err, src,
					)
				}
				return nil, wrapErrorf(
					errTransactionExpired,
					"expired transaction %s: %s",
					txnHash, err,
				)
			}
		}
		// TODO(tav): Distinguish between other non-fatal and fatal errors.
		return nil, wrapErrorf(
			errBroadcastFailed,
			"failed to submit transaction %s: %s",
			txnHash, err,
		)
	}
	return &types.TransactionIdentifierResponse{
		TransactionIdentifier: &types.TransactionIdentifier{
			Hash: hex.EncodeToString(hash),
		},
	}, nil
}

func (s *Server) decodeTransaction(src string, signed bool) (*entities.Transaction, *innerTxn, *types.Error) {
	split := strings.Split(src, ":")
	if len(split) == 2 {
		src = split[0]
	}
	raw, err := hex.DecodeString(src)
	if err != nil {
		return nil, nil, wrapErr(errInvalidTransactionPayload, err)
	}
	if len(raw) == 0 {
		return nil, nil, wrapErrorf(errInvalidTransactionPayload, "missing transaction data")
	}
	switch raw[0] {
	case txnTypeFlow:
		txn := &entities.Transaction{}
		if err := proto.Unmarshal(raw[1:], legacyProto.MessageV2(txn)); err != nil {
			return nil, nil, wrapErr(errProtobuf, err)
		}
		if signed {
			if len(txn.EnvelopeSignatures) != 1 {
				return nil, nil, wrapErrorf(
					errInvalidTransactionPayload,
					"invalid number of envelope signatures for signed transaction: %d",
					len(txn.EnvelopeSignatures),
				)
			}
			if txn.EnvelopeSignatures[0] == nil {
				return nil, nil, wrapErrorf(
					errInvalidTransactionPayload,
					"transaction missing envelope signature",
				)
			}
		}
		return txn, nil, nil
	case txnTypeInner:
		txn := &innerTxn{
			raw: raw,
		}
		if signed {
			if len(raw) != 97 {
				return nil, nil, wrapErrorf(
					errInvalidTransactionPayload,
					"unexpected length for signed transaction payload: expected 97, got %d",
					len(raw),
				)
			}
		} else {
			if len(raw) != 33 {
				return nil, nil, wrapErrorf(
					errInvalidTransactionPayload,
					"unexpected length for transaction payload: expected 33, got %d",
					len(raw),
				)
			}
		}
		txn.amount = binary.BigEndian.Uint64(raw[1:])
		txn.nonce = binary.BigEndian.Uint64(raw[9:])
		txn.receiver = raw[17:25]
		txn.sender = raw[25:33]
		return nil, txn, nil
	default:
		return nil, nil, wrapErrorf(
			errInvalidTransactionPayload, "unknown transaction type: %d", raw[0],
		)
	}
}

func (s *Server) getConstructOpts(md map[string]interface{}) (string, *model.ConstructOpts, *types.Error) {
	val, ok := md["protobuf"]
	if !ok {
		return "", nil, wrapErrorf(
			errInvalidConstructOptions,
			"missing protobuf options field",
		)
	}
	raw, ok := val.(string)
	if !ok {
		return "", nil, wrapErrorf(
			errInvalidConstructOptions,
			"protobuf options field is not a string: %v", val,
		)
	}
	enc, err := hex.DecodeString(raw)
	if err != nil {
		return "", nil, wrapErrorf(
			errInvalidConstructOptions,
			"protobuf options field is not a string: %v", val,
		)
	}
	opts := &model.ConstructOpts{}
	err = proto.Unmarshal(enc, opts)
	if err != nil {
		return "", nil, wrapErr(errProtobuf, err)
	}
	return raw, opts, nil
}

func (s *Server) parseIntent(ops []*types.Operation) (*txnIntent, *types.Error) {
	if len(ops) == 0 {
		return nil, wrapErrorf(errInvalidOpsIntent, "missing operations")
	}
	if ops[0] == nil {
		return nil, wrapErrorf(errInvalidOpsIntent, "null operation value encountered")
	}
	typ := ops[0].Type
	for _, op := range ops {
		if op == nil {
			return nil, wrapErrorf(errInvalidOpsIntent, "null operation value encountered")
		}
		if op.Type != typ {
			return nil, wrapErrorf(
				errInvalidOpsIntent,
				"mismatching operation types encountered: %q and %q",
				typ,
				op.Type,
			)
		}
	}
	intent := &txnIntent{}
	switch typ {
	case opCreateAccount, opCreateProxyAccount:
		for _, op := range ops {
			val, ok := op.Metadata["public_key"]
			if !ok {
				return nil, wrapErrorf(
					errInvalidOpsIntent, "public_key metadata field missing on operation",
				)
			}
			raw, ok := val.(string)
			if !ok {
				return nil, wrapErrorf(
					errInvalidOpsIntent,
					"public_key metadata field on operation is not a string: %v",
					val,
				)
			}
			compressed, err := hex.DecodeString(raw)
			if err != nil {
				return nil, wrapErrorf(
					errInvalidOpsIntent,
					"invalid public_key metadata field on operation: %s",
					err,
				)
			}
			pub, err := crypto.ConvertRosettaPublicKey(compressed)
			if err != nil {
				return nil, wrapErr(errInvalidOpsIntent, err)
			}
			intent.keys = append(intent.keys, hex.EncodeToString(pub))
		}
		if typ == opCreateProxyAccount {
			if len(ops) > 1 {
				return nil, wrapErrorf(
					errInvalidOpsIntent,
					"multiple (%d) %s operations found",
					len(ops), typ,
				)
			}
			intent.proxy = true
		}
	case opDeployContract, opUpdateContract:
		if len(ops) > 1 {
			return nil, wrapErrorf(
				errInvalidOpsIntent,
				"multiple (%d) %s operations found",
				len(ops), typ,
			)
		}
		op := ops[0]
		val, ok := op.Metadata["contract_name"]
		if !ok {
			return nil, wrapErrorf(
				errInvalidOpsIntent, "contract_name metadata field missing on operation",
			)
		}
		name, ok := val.(string)
		if !ok {
			return nil, wrapErrorf(
				errInvalidOpsIntent,
				"contract_name metadata field on operation is not a string: %v",
				val,
			)
		}
		if len(name) == 0 {
			return nil, wrapErrorf(
				errInvalidOpsIntent,
				"contract_name metadata field on operation is empty",
			)
		}
		val, ok = op.Metadata["contract_code"]
		if !ok {
			return nil, wrapErrorf(
				errInvalidOpsIntent, "contract_code metadata field missing on operation",
			)
		}
		code, ok := val.(string)
		if !ok {
			return nil, wrapErrorf(
				errInvalidOpsIntent,
				"contract_code metadata field on operation is not a string: %v",
				val,
			)
		}
		_, err := hex.DecodeString(code)
		if err != nil {
			return nil, wrapErrorf(
				errInvalidOpsIntent,
				"failed to hex-decode contract_code metadata field on operation: %s",
				err,
			)
		}
		if len(code) == 0 {
			return nil, wrapErrorf(
				errInvalidOpsIntent,
				"contract_code metadata field on operation is empty",
			)
		}
		val, ok = op.Metadata["prev_key_index"]
		if !ok {
			return nil, wrapErrorf(
				errInvalidOpsIntent, "prev_key_index metadata field missing on operation",
			)
		}
		prevKeyIndex, ok := val.(float64)
		if !ok {
			return nil, wrapErrorf(
				errInvalidOpsIntent,
				"prev_key_index metadata field on operation is not a number: %v",
				val,
			)
		}
		val, ok = op.Metadata["new_key"]
		if !ok {
			return nil, wrapErrorf(
				errInvalidOpsIntent, "new_key metadata field missing on operation",
			)
		}
		rawKey, ok := val.(string)
		if !ok {
			return nil, wrapErrorf(
				errInvalidOpsIntent,
				"new_key metadata field on operation is not a string: %v",
				val,
			)
		}
		srcKey, err := hex.DecodeString(rawKey)
		if err != nil {
			return nil, wrapErrorf(
				errInvalidOpsIntent,
				"failed to hex-decode new_key metadata field on operation: %s",
				err,
			)
		}
		if len(srcKey) == 0 {
			return nil, wrapErrorf(
				errInvalidOpsIntent,
				"new_key metadata field on operation is empty",
			)
		}
		newKey, err := crypto.ConvertRosettaPublicKey(srcKey)
		if err != nil {
			return nil, wrapErrorf(
				errInvalidOpsIntent,
				"could not convert new_key value from operation: %s",
				err,
			)
		}
		val, ok = op.Metadata["key_message"]
		if !ok {
			return nil, wrapErrorf(
				errInvalidOpsIntent, "key_message metadata field missing on operation",
			)
		}
		keyMessage, ok := val.(string)
		if !ok {
			return nil, wrapErrorf(
				errInvalidOpsIntent,
				"key_message metadata field on operation is not a string: %v",
				val,
			)
		}
		if len(keyMessage) == 0 {
			return nil, wrapErrorf(
				errInvalidOpsIntent,
				"key_message metadata field on operation is empty",
			)
		}
		val, ok = op.Metadata["key_signature"]
		if !ok {
			return nil, wrapErrorf(
				errInvalidOpsIntent, "key_signature metadata field missing on operation",
			)
		}
		keySignature, ok := val.(string)
		if !ok {
			return nil, wrapErrorf(
				errInvalidOpsIntent,
				"key_signature metadata field on operation is not a string: %v",
				val,
			)
		}
		if len(keySignature) == 0 {
			return nil, wrapErrorf(
				errInvalidOpsIntent,
				"key_signature metadata field on operation is empty",
			)
		}
		keySignature, err = crypto.ConvertDERSignature(keySignature)
		if err != nil {
			return nil, wrapErrorf(
				errInvalidOpsIntent,
				"could not convert key_signature value from operation: %s",
				err,
			)
		}
		val, ok = op.Metadata["key_metadata"]
		if !ok {
			return nil, wrapErrorf(
				errInvalidOpsIntent, "key_metadata metadata field missing on operation",
			)
		}
		keyMetadata, ok := val.(string)
		if !ok {
			return nil, wrapErrorf(
				errInvalidOpsIntent,
				"key_metadata metadata field on operation is not a string: %v",
				val,
			)
		}
		if len(keyMetadata) == 0 {
			return nil, wrapErrorf(
				errInvalidOpsIntent,
				"key_metadata metadata field on operation is empty",
			)
		}
		intent.contractCode = code
		intent.contractName = name
		intent.keyMessage = keyMessage
		intent.keyMetadata = keyMetadata
		intent.keySignature = keySignature
		intent.newKey = hex.EncodeToString(newKey)
		intent.prevKeyIndex = uint32(prevKeyIndex)
		if typ == opUpdateContract {
			intent.contractUpdate = true
		}
	case opProxyTransfer, opProxyTransferInner, opTransfer:
		// NOTE(tav): We may want to add an additional validation step to make
		// sure that the sender is one of our originated accounts — but this
		// would require for the indexer to be fully synced.
		descriptions := &parser.Descriptions{
			OperationDescriptions: []*parser.OperationDescription{
				{
					Account: &parser.AccountDescription{
						Exists: true,
					},
					Amount: &parser.AmountDescription{
						Currency: flowCurrency,
						Exists:   true,
						Sign:     parser.NegativeAmountSign,
					},
					Type: typ,
				},
				{
					Account: &parser.AccountDescription{
						Exists: true,
					},
					Amount: &parser.AmountDescription{
						Currency: flowCurrency,
						Exists:   true,
						Sign:     parser.PositiveAmountSign,
					},
					Type: typ,
				},
			},
			ErrUnmatched: true,
		}
		matches, err := parser.MatchOperations(descriptions, ops)
		if err != nil {
			return nil, wrapErrorf(
				errInvalidOpsIntent,
				"unable to match operations: %s",
				err,
			)
		}
		var xerr *types.Error
		senderOp, _ := matches[0].First()
		intent.sender, xerr = s.getAccount(senderOp.Account.Address)
		if xerr != nil {
			return nil, xerr
		}
		receiverOp, amount := matches[1].First()
		intent.amount = amount.Uint64()
		intent.receiver, xerr = s.getAccount(receiverOp.Account.Address)
		if xerr != nil {
			return nil, xerr
		}
		switch typ {
		case opProxyTransfer:
			intent.proxy = true
		case opProxyTransferInner:
			intent.inner = true
		}
	default:
		return nil, wrapErrorf(
			errInvalidOpsIntent,
			"unknown operation type encountered: %q",
			typ,
		)
	}
	return intent, nil
}

func decodeContractOps(txn *entities.Transaction, signed bool) (*types.ConstructionParseResponse, *types.Error) {
	if len(txn.Arguments) != 8 {
		return nil, wrapErrorf(
			errInvalidTransactionPayload,
			"invalid number of transaction args: %d", len(txn.Arguments),
		)
	}
	raw, err := jsoncdc.Decode(access.NoopMemoryGauge, txn.Arguments[0])
	if err != nil {
		return nil, wrapErrorf(
			errInvalidTransactionPayload, "unable to decode transaction arg: %s", err,
		)
	}
	update, ok := raw.(cadence.Bool)
	if !ok {
		return nil, wrapErrorf(
			errInvalidTransactionPayload, "unable to convert transaction arg to string",
		)
	}
	typ := opDeployContract
	if update {
		typ = opUpdateContract
	}
	raw, err = jsoncdc.Decode(access.NoopMemoryGauge, txn.Arguments[1])
	if err != nil {
		return nil, wrapErrorf(
			errInvalidTransactionPayload, "unable to decode transaction arg: %s", err,
		)
	}
	name, ok := raw.(cadence.String)
	if !ok {
		return nil, wrapErrorf(
			errInvalidTransactionPayload, "unable to convert transaction arg to string",
		)
	}
	raw, err = jsoncdc.Decode(access.NoopMemoryGauge, txn.Arguments[2])
	if err != nil {
		return nil, wrapErrorf(
			errInvalidTransactionPayload, "unable to decode transaction arg: %s", err,
		)
	}
	code, ok := raw.(cadence.String)
	if !ok {
		return nil, wrapErrorf(
			errInvalidTransactionPayload, "unable to convert transaction arg to string",
		)
	}
	raw, err = jsoncdc.Decode(access.NoopMemoryGauge, txn.Arguments[3])
	if err != nil {
		return nil, wrapErrorf(
			errInvalidTransactionPayload, "unable to decode transaction arg: %s", err,
		)
	}
	prevKeyIndex, ok := raw.(cadence.Int)
	if !ok {
		return nil, wrapErrorf(
			errInvalidTransactionPayload, "unable to convert transaction arg to *big.Int",
		)
	}
	raw, err = jsoncdc.Decode(access.NoopMemoryGauge, txn.Arguments[4])
	if err != nil {
		return nil, wrapErrorf(
			errInvalidTransactionPayload, "unable to decode transaction arg: %s", err,
		)
	}
	rawKey, ok := raw.(cadence.String)
	if !ok {
		return nil, wrapErrorf(
			errInvalidTransactionPayload, "unable to convert transaction arg to string",
		)
	}
	flowKey, err := hex.DecodeString(string(rawKey))
	if err != nil {
		return nil, wrapErrorf(
			errInvalidTransactionPayload,
			"unable to hex-decode new key transaction arg: %s",
			err,
		)
	}
	newKey, err := crypto.ConvertFlowPublicKey(flowKey)
	if err != nil {
		return nil, wrapErrorf(
			errInvalidTransactionPayload,
			"unable to convert new key transaction arg: %s",
			err,
		)
	}
	raw, err = jsoncdc.Decode(access.NoopMemoryGauge, txn.Arguments[5])
	if err != nil {
		return nil, wrapErrorf(
			errInvalidTransactionPayload, "unable to decode transaction arg: %s", err,
		)
	}
	keyMessage, ok := raw.(cadence.String)
	if !ok {
		return nil, wrapErrorf(
			errInvalidTransactionPayload, "unable to convert transaction arg to string",
		)
	}
	raw, err = jsoncdc.Decode(access.NoopMemoryGauge, txn.Arguments[6])
	if err != nil {
		return nil, wrapErrorf(
			errInvalidTransactionPayload, "unable to decode transaction arg: %s", err,
		)
	}
	rawSignature, ok := raw.(cadence.String)
	if !ok {
		return nil, wrapErrorf(
			errInvalidTransactionPayload, "unable to convert transaction arg to string",
		)
	}
	keySignature, err := crypto.ConvertFlowSignature(string(rawSignature))
	if err != nil {
		return nil, wrapErrorf(
			errInvalidTransactionPayload,
			"unable to convert key signature transaction arg: %s",
			err,
		)
	}
	raw, err = jsoncdc.Decode(access.NoopMemoryGauge, txn.Arguments[7])
	if err != nil {
		return nil, wrapErrorf(
			errInvalidTransactionPayload, "unable to decode transaction arg: %s", err,
		)
	}
	keyMetadata, ok := raw.(cadence.String)
	if !ok {
		return nil, wrapErrorf(
			errInvalidTransactionPayload, "unable to convert transaction arg to string",
		)
	}
	return txnOps(txn, []*types.Operation{
		{
			Metadata: map[string]interface{}{
				"contract_code":  code,
				"contract_name":  name,
				"key_message":    keyMessage,
				"key_metadata":   keyMetadata,
				"key_signature":  keySignature,
				"new_key":        hex.EncodeToString(newKey),
				"prev_key_index": prevKeyIndex.Value.Int64(),
			},
			OperationIdentifier: &types.OperationIdentifier{
				Index: 0,
			},
			Type: typ,
		},
	}, signed)
}

func decodeCreateAccountOps(txn *entities.Transaction, proxy bool, signed bool) (*types.ConstructionParseResponse, *types.Error) {
	ops := []*types.Operation{}
	if len(txn.Arguments) != 1 {
		return nil, wrapErrorf(
			errInvalidTransactionPayload,
			"invalid number of transaction args: %d", len(txn.Arguments),
		)
	}
	raw, err := jsoncdc.Decode(access.NoopMemoryGauge, txn.Arguments[0])
	if err != nil {
		return nil, wrapErrorf(
			errInvalidTransactionPayload, "unable to decode transaction arg: %s", err,
		)
	}
	if proxy {
		key, ok := raw.(cadence.String)
		if !ok {
			return nil, wrapErrorf(
				errInvalidTransactionPayload, "unable to convert transaction arg to string",
			)
		}
		pub, err := hex.DecodeString(string(key))
		if err != nil {
			return nil, wrapErrorf(
				errInvalidTransactionPayload,
				"unable to hex decode transaction arg: %s", err,
			)
		}
		pub, err = crypto.ConvertFlowPublicKey(pub)
		if err != nil {
			return nil, wrapErr(errInvalidTransactionPayload, err)
		}
		ops = append(ops, &types.Operation{
			Metadata: map[string]interface{}{
				"public_key": hex.EncodeToString(pub),
			},
			OperationIdentifier: &types.OperationIdentifier{
				Index: int64(len(ops)),
			},
			Type: opCreateProxyAccount,
		})
	} else {
		xs, ok := raw.(cadence.Array)
		if !ok {
			return nil, wrapErrorf(
				errInvalidTransactionPayload, "unable to convert transaction arg to array",
			)
		}
		for _, val := range xs.Values {
			key, ok := val.(cadence.String)
			if !ok {
				return nil, wrapErrorf(
					errInvalidTransactionPayload, "unable to convert transaction arg elem to string",
				)
			}
			pub, err := hex.DecodeString(string(key))
			if err != nil {
				return nil, wrapErrorf(
					errInvalidTransactionPayload,
					"unable to hex decode transaction arg: %s", err,
				)
			}
			pub, err = crypto.ConvertFlowPublicKey(pub)
			if err != nil {
				return nil, wrapErr(errInvalidTransactionPayload, err)
			}
			ops = append(ops, &types.Operation{
				Metadata: map[string]interface{}{
					"public_key": hex.EncodeToString(pub),
				},
				OperationIdentifier: &types.OperationIdentifier{
					Index: int64(len(ops)),
				},
				Type: opCreateAccount,
			})
		}
	}
	return txnOps(txn, ops, signed)
}

func decodeOptsFromTransaction(src string) *model.ConstructOpts {
	split := strings.Split(src, ":")
	if len(split) != 2 || split[1] == "" {
		log.Warnf("Failed to find construct opts in transaction data: %q", src)
		return nil
	}
	raw, err := hex.DecodeString(split[1])
	if err != nil {
		log.Warnf(
			"Failed to hex-decode construct opts from transaction data: %q: %s",
			src, err,
		)
		return nil
	}
	opts := &model.ConstructOpts{}
	err = proto.Unmarshal(raw, opts)
	if err != nil {
		log.Warnf(
			"Failed to decode protobuf-encoded construct opts from transaction data: %q: %s",
			src, err,
		)
		return nil
	}
	return opts
}

func decodeTransferOps(txn *entities.Transaction, proxy bool, signed bool) (*types.ConstructionParseResponse, *types.Error) {
	expectedArgs := 2
	if proxy {
		expectedArgs = 5
	}
	if len(txn.Arguments) != expectedArgs {
		return nil, wrapErrorf(
			errInvalidTransactionPayload,
			"invalid number of transaction args: %d", len(txn.Arguments),
		)
	}
	idx := 0
	sender := txn.Payer
	typ := opTransfer
	if proxy {
		typ = opProxyTransfer
		raw, err := jsoncdc.Decode(access.NoopMemoryGauge, txn.Arguments[0])
		if err != nil {
			return nil, wrapErrorf(
				errInvalidTransactionPayload, "unable to decode sender transaction arg: %s", err,
			)
		}
		addr, ok := raw.(cadence.Address)
		if !ok {
			return nil, wrapErrorf(
				errInvalidTransactionPayload, "unable to convert sender arg to address",
			)
		}
		sender = addr[:]
		idx++
	}
	raw, err := jsoncdc.Decode(access.NoopMemoryGauge, txn.Arguments[idx])
	if err != nil {
		return nil, wrapErrorf(
			errInvalidTransactionPayload, "unable to decode receiver transaction arg: %s", err,
		)
	}
	receiver, ok := raw.(cadence.Address)
	if !ok {
		return nil, wrapErrorf(
			errInvalidTransactionPayload, "unable to convert receiver arg to address",
		)
	}
	raw, err = jsoncdc.Decode(access.NoopMemoryGauge, txn.Arguments[idx+1])
	if err != nil {
		return nil, wrapErrorf(
			errInvalidTransactionPayload, "unable to decode amount transaction arg: %s", err,
		)
	}
	amount, ok := raw.(cadence.UInt64)
	if !ok {
		return nil, wrapErrorf(
			errInvalidTransactionPayload, "unable to convert amount transaction arg to uint64",
		)
	}
	if proxy {
		// NOTE(tav): We only decode the nonce and sig args to sanity check the
		// argument types.
		raw, err = jsoncdc.Decode(access.NoopMemoryGauge, txn.Arguments[3])
		if err != nil {
			return nil, wrapErrorf(
				errInvalidTransactionPayload, "unable to decode nonce transaction arg: %s", err,
			)
		}
		_, ok := raw.(cadence.Int64)
		if !ok {
			return nil, wrapErrorf(
				errInvalidTransactionPayload, "unable to convert nonce transaction arg to int64",
			)
		}
		raw, err = jsoncdc.Decode(access.NoopMemoryGauge, txn.Arguments[4])
		if err != nil {
			return nil, wrapErrorf(
				errInvalidTransactionPayload, "unable to decode sig transaction arg: %s", err,
			)
		}
		_, ok = raw.(cadence.String)
		if !ok {
			return nil, wrapErrorf(
				errInvalidTransactionPayload, "unable to convert sig transaction arg to string",
			)
		}
	}
	return txnOps(txn, []*types.Operation{
		{
			Account: &types.AccountIdentifier{
				Address: "0x" + hex.EncodeToString(sender),
			},
			Amount: &types.Amount{
				Currency: flowCurrency,
				Value:    "-" + strconv.FormatUint(uint64(amount), 10),
			},
			OperationIdentifier: &types.OperationIdentifier{
				Index: 0,
			},
			Type: typ,
		},
		{
			Account: &types.AccountIdentifier{
				Address: "0x" + hex.EncodeToString(receiver[:]),
			},
			Amount: &types.Amount{
				Currency: flowCurrency,
				Value:    strconv.FormatUint(uint64(amount), 10),
			},
			OperationIdentifier: &types.OperationIdentifier{
				Index: 1,
			},
			RelatedOperations: []*types.OperationIdentifier{{
				Index: 0,
			}},
			Type: typ,
		},
	}, signed)
}

func txnOps(txn *entities.Transaction, ops []*types.Operation, signed bool) (*types.ConstructionParseResponse, *types.Error) {
	if signed {
		return &types.ConstructionParseResponse{
			AccountIdentifierSigners: []*types.AccountIdentifier{{
				Address: "0x" + hex.EncodeToString(txn.Payer),
			}},
			Operations: ops,
		}, nil
	}
	return &types.ConstructionParseResponse{
		Operations: ops,
	}, nil
}
