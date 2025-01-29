package state

import (
	"bytes"
	"context"
	"encoding/hex"
	"strings"
	"time"

	"github.com/onflow/cadence"
	"github.com/onflow/cadence/stdlib"
	"github.com/onflow/flow-go/model/flow"
	flowaccess "github.com/onflow/flow/protobuf/go/flow/access"
	"github.com/onflow/flow/protobuf/go/flow/entities"
	"github.com/onflow/rosetta/access"
	"github.com/onflow/rosetta/cache"
	"github.com/onflow/rosetta/config"
	"github.com/onflow/rosetta/crypto"
	"github.com/onflow/rosetta/log"
	"github.com/onflow/rosetta/model"
	"github.com/onflow/rosetta/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	indexerBlockHeight = trace.Gauge("state", "indexer_block_height")
)

func (i *Indexer) processBlock(rctx context.Context, spork *config.Spork, height uint64, hash []byte) {
	// TODO(tav): Confirm that failed transactions do not emit invalid events.
	// indexerBlockHeight.Observe(int64(height))
	backoff := time.Second
	blockID := toFlowIdentifier(hash)
	ctx := rctx
	_, skip := i.Chain.SkipBlocks[blockID]
	initial := true
	skipCache := false
	skipCacheWarned := false
	slowPath := false
	unsealed := false
	var span trace.Span
outer:
	for {
		if span != nil {
			trace.EndSpanErrorf(span, "retrying")
		}
		select {
		case <-rctx.Done():
			return
		default:
		}
		if initial {
			initial = false
		} else {
			time.Sleep(backoff)
		}
		if skipCache {
			ctx = cache.Skip(rctx)
			if !skipCacheWarned {
				log.Warnf("Skipping cache for block %x at height %d", hash, height)
				skipCacheWarned = true
			}
		} else {
			ctx = rctx
		}
		ctx, span = trace.NewSpan(
			ctx, "flow.process_block",
			trace.Hexify("block_hash", hash),
			trace.Int64("block_height", int64(height)),
			trace.Bool("skip_integrity_checks", skip),
			trace.Bool("slow_path", slowPath),
		)
		client := spork.AccessNodes.Client()
		block, err := client.BlockByHeight(ctx, height)
		if err != nil {
			log.Errorf("Failed to fetch block %x at height %d: %s", hash, height, err)
			continue
		}
		var (
			txns       []*entities.Transaction
			txnResults []*flowaccess.TransactionResultResponse
		)
		if !slowPath {
			client := spork.AccessNodes.Client()
			txns, err = client.TransactionsByBlockID(ctx, hash)
			if err != nil {
				log.Errorf(
					"Failed to fetch transactions in block %x at height %d: %s",
					hash, height, err,
				)
				if useSlowPath(err) {
					slowPath = true
					span.SetAttributes(trace.Bool("slow_path", true))
				} else {
					if status.Code(err) == codes.NotFound {
						backoff = i.nextBackoff(backoff)
					}
					continue
				}
			}
		}
		if !slowPath {
			client := spork.AccessNodes.Client()
			txnResults, err = client.TransactionResultsByBlockID(ctx, hash)
			if err != nil {
				log.Errorf(
					"Failed to fetch transaction results in block %x at height %d: %s",
					hash, height, err,
				)
				if useSlowPath(err) {
					slowPath = true
					span.SetAttributes(trace.Bool("slow_path", true))
				} else {
					continue
				}
			}
		}
		cols := []*collectionData{}
		if slowPath {
			log.Infof(
				"Using the slow path to process block %x at height %d",
				hash, height,
			)
			txnIndex := -1
			for _, col := range block.CollectionGuarantees {
				colData := &collectionData{}
				cols = append(cols, colData)
				initialCol := true
				for {
					select {
					case <-ctx.Done():
						span.End()
						return
					default:
					}
					if initialCol {
						initialCol = false
					} else {
						time.Sleep(time.Second)
					}
					client := spork.AccessNodes.Client()
					info, err := client.CollectionByID(ctx, col.CollectionId)
					if err != nil {
						log.Errorf(
							"Failed to fetch collection %x in block %x at height %d: %s",
							col.CollectionId, hash, height, err,
						)
						continue
					}
					tctx := ctx
					for _, txnHash := range info.TransactionIds {
						ctx = tctx
						initialTxn := true
						txnIndex++
						for {
							select {
							case <-ctx.Done():
								span.End()
								return
							default:
							}
							if initialTxn {
								initialTxn = false
							} else {
								time.Sleep(time.Second)
							}
							client := spork.AccessNodes.Client()
							info, err := client.Transaction(ctx, txnHash)
							if err != nil {
								log.Errorf(
									"Failed to fetch transaction %x in block %x at height %d: %s",
									txnHash, hash, height, err,
								)
								continue
							}
							client = spork.AccessNodes.Client()
							txnResult, err := client.TransactionResult(ctx, hash, uint32(txnIndex))
							if err != nil {
								log.Errorf(
									"Failed to fetch transaction result for %x in block %x at height %d: %s",
									txnHash, hash, height, err,
								)
								continue
							}
							colData.txns = append(colData.txns, info)
							colData.txnResults = append(colData.txnResults, txnResult)
							break
						}
					}
					ctx = tctx
					break
				}
			}
			col := &collectionData{system: true}
			cols = append(cols, col)
			// NOTE(tav): We only look for the system transaction if this is a
			// block in the first spork (where we assume that the configured
			// root block is not the actual root block of the spork), or is not
			// the self-sealed root block of a spork (where a ZeroID is used for
			// the event collection hash).
			if !(spork.Prev != nil && height == spork.RootBlock) {
				// TODO(tav): We check for just one transaction in the system
				// collection. If this changes in the future, we will need to
				// update the logic here to speculatively fetch more transaction
				// results.
				txnIndex++
				for {
					select {
					case <-ctx.Done():
						return
					default:
					}
					client := spork.AccessNodes.Client()
					txnResult, err := client.TransactionResult(ctx, hash, uint32(txnIndex))
					if err != nil {
						log.Errorf(
							"Failed to fetch transaction result at index %d in block %x at height %d: %s",
							txnIndex, hash, height, err,
						)
						time.Sleep(time.Second)
						continue
					}
					col.txnResults = append(col.txnResults, txnResult)
					break
				}
			}
		} else {
			col := &collectionData{}
			cols = append(cols, col)
			prev := []byte{}
			txnLen := len(txns)
			for idx, result := range txnResults {
				if idx != 0 {
					if !bytes.Equal(result.CollectionId, prev) {
						col = &collectionData{}
						cols = append(cols, col)
					}
				}
				if idx < txnLen {
					col.txns = append(col.txns, txns[idx])
				}
				col.txnResults = append(col.txnResults, result)
				prev = result.CollectionId
			}
			cols[len(cols)-1].system = true
		}
		eventHashes := []flow.Identifier{}
		if spork.Prev != nil && height == spork.RootBlock {
			// NOTE(tav): Looks like we're in the self-sealed root block of a
			// spork.
			eventHashes = append(eventHashes, flow.ZeroID)
		} else {
			for _, col := range cols {
				colEvents := []flowEvent{}
				for _, txnResult := range col.txnResults {
					for _, evt := range txnResult.Events {
						colEvents = append(colEvents, flowEvent{
							EventIndex:       evt.EventIndex,
							Payload:          evt.Payload,
							TransactionID:    toFlowIdentifier(evt.TransactionId),
							TransactionIndex: evt.TransactionIndex,
							Type:             flow.EventType(evt.Type),
						})
					}
				}
				eventHashes = append(eventHashes, deriveEventsHash(spork, colEvents))
			}
		}
		var execResult *entities.ExecutionResult
		execBackoff := time.Second
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			client = spork.AccessNodes.Client()
			execResult, err = client.ExecutionResultForBlockID(ctx, hash)
			if err == nil {
				break
			}
			log.Errorf(
				"Failed to fetch execution result for block %x at height %d: %s",
				hash, height, err,
			)
			if status.Code(err) == codes.NotFound {
				execBackoff = i.nextBackoff(execBackoff)
			}
			time.Sleep(execBackoff)
		}
		if skip {
			log.Warnf(
				"Skipping data integrity checks for block %x at height %d",
				hash, height,
			)
		} else {
			// TODO(tav): Once Flow eventually implements the splitting up of
			// collections across multiple chunks, we would need to update the
			// events hash verification mechanism.
			if len(execResult.Chunks) != len(eventHashes) {
				log.Errorf(
					"Execution result for block %x at height %d contains %d chunks, expected %d",
					hash, height, len(execResult.Chunks), len(eventHashes),
				)
				skipCache = true
				continue
			}
			for idx, eventHash := range eventHashes {
				chunk := execResult.Chunks[idx]
				if chunk == nil {
					log.Errorf(
						"Got unexpected nil chunk at offset %d in the execution result for block %x at height %d",
						idx, hash, height,
					)
					skipCache = true
					continue outer
				}
				if !bytes.Equal(chunk.EventCollection, eventHash[:]) {
					log.Errorf(
						"Got mismatching event hash within chunk at offset %d of block %x at height %d: expected %x (from events), got %x (from execution result)",
						idx, hash, height, eventHash[:], chunk.EventCollection,
					)
					skipCache = true
					continue outer
				}
			}
			var resultID flow.Identifier
			exec, convOk := convertExecutionResult(hash, height, execResult)
			if convOk {
				resultID = deriveExecutionResult(spork, exec)
			}
			if !convOk {
				skipCache = true
				continue
			}
			sealedResult, foundOk := i.sealedResults[string(hash)]
			if spork.Prev != nil && height == spork.RootBlock {
				sealedResult, foundOk = string(resultID[:]), true
			}
			if foundOk {
				if string(resultID[:]) != sealedResult {
					log.Errorf(
						"Got mismatching execution result hash for block %x at height %d: expected %x, got %x",
						hash, height, sealedResult, resultID[:],
					)
					skipCache = true
					continue
				}
			} else if spork.Next == nil {
				log.Fatalf(
					"No sealed result found for block %x at height %d in the live spork",
					hash, height,
				)
			} else {
				// NOTE(tav): The last few blocks of a spork may not be sealed with
				// a result. Instead, they are implicitly sealed by the root
				// protocol state snapshot.
				if height >= spork.Next.RootBlock-i.Chain.SporkSealTolerance {
					log.Errorf(
						"No sealed result found for block %x at height %d (within spork seal tolerance)",
						hash, height,
					)
					unsealed = true
				} else {
					log.Fatalf(
						"No sealed result found for block %x at height %d",
						hash, height,
					)
				}
			}
		}
		data := &model.IndexedBlock{
			Timestamp: uint64(block.Timestamp.AsTime().UnixNano()),
		}
		newAccounts := map[string]bool{}
		newCounter := 0
		transfers := 0
		for _, col := range cols {
			// TODO(tav): We may want to index events from the system collection
			// at some point in the future.
			if col.system {
				continue
			}
			for idx, txnResult := range col.txnResults {
				select {
				case <-ctx.Done():
					span.End()
					return
				default:
				}
				payer := col.txns[idx].Payer
				txnHash := txnResult.TransactionId
				// NOTE(tav): Sanity check that the block hashes match in case
				// there's a regression in the Access API server implementation.
				if !bytes.Equal(txnResult.BlockId, hash) {
					log.Errorf(
						"Found unexpected block %x in the transaction result for %x in block %x at height %d",
						txnResult.BlockId, txnHash, hash, height,
					)
					if skip {
						continue
					} else {
						skipCache = true
						continue outer
					}
				}
				txn := &model.IndexedTransaction{
					Hash: txnHash,
				}
				if txnResult.StatusCode != 0 {
					txn.ErrorMessage = txnResult.ErrorMessage
					txn.Failed = true
				}
				data.Transactions = append(data.Transactions, txn)
				accountKeys := map[string][]byte{}
				addrs := [][]byte{}
				deposits := map[string]uint64{}
				fees := uint64(0)
				proxyDeposits := map[string]uint64{}
				proxyTransfers := []*proxyTransfer{}
				withdrawals := map[string]uint64{}
				// NOTE(tav): We split apart the event processing into two
				// subsets, as the second set of events might depend on
				// information from the first set.
			evtloop:
				for _, evt := range txnResult.Events {
					switch evt.Type {
					case "flow.AccountCreated":
						event, err := decodeEvent("flow.AccountCreated", evt, hash, height)
						if err != nil {
							skipCache = true
							continue outer
						}
						fields := event.FieldsMappedByName()
						if len(fields) != 1 {
							log.Errorf(
								"Found flow.AccountCreated event with %d fields in transaction %x in block %x at height %d",
								len(fields), txnHash, hash, height,
							)
							skipCache = true
							continue outer
						}
						// 'address' field
						addr, ok := cadence.SearchFieldByName(
							event,
							stdlib.AccountEventAddressParameter.Identifier,
						).(cadence.Address)
						if !ok {
							log.Errorf(
								"Unable to load address from flow.AccountCreated event in transaction %x in block %x at height %d",
								txnHash, hash, height,
							)
							skipCache = true
							continue outer
						}
						// NOTE(tav): We only care about new accounts if they
						// are one of our root originators, or are created by
						// one of our tracked accounts, i.e. have been created
						// by one of our originators, or an originated account.
						if i.originators[string(addr[:])] || i.isTracked(payer, newAccounts) {
							accountKeys[string(addr[:])] = nil
							addrs = append(addrs, addr[:])
							newAccounts[string(addr[:])] = false
							newCounter++
						}
					case i.typProxyCreated:
						// NOTE(tav): Since FlowColdStorageProxy.Created events
						// are emitted after flow.AccountCreated, we can process
						// it within the same loop.
						event, err := decodeEvent("FlowColdStorageProxy.Created", evt, hash, height)
						if err != nil {
							skipCache = true
							continue outer
						}
						fields := event.FieldsMappedByName()
						// NOTE(tav): We only care about new proxy accounts if
						// they are created by one of our tracked accounts.
						if !i.isTracked(payer, newAccounts) {
							continue evtloop
						}
						if len(accountKeys) == 0 {
							log.Errorf(
								"Found FlowColdStorageProxy.Created event without a corresponding AccountCreated event in transaction %x in block %x at height %d",
								txnHash, hash, height,
							)
							skipCache = true
							continue outer
						}
						if len(fields) != 2 {
							log.Errorf(
								"Found FlowColdStorageProxy.Created event with %d fields in transaction %x in block %x at height %d",
								len(fields), txnHash, hash, height,
							)
							skipCache = true
							continue outer
						}
						// account field
						addr, ok := cadence.SearchFieldByName(
							event,
							"account",
						).(cadence.Address)
						if !ok {
							log.Errorf(
								"Unable to load address from FlowColdStorageProxy.Created event in transaction %x in block %x at height %d",
								txnHash, hash, height,
							)
							skipCache = true
							continue outer
						}
						_, ok = accountKeys[string(addr[:])]
						if !ok {
							log.Errorf(
								"Missing AccountCreated event for address %x from FlowColdStorageProxy.Created event in transaction %x in block %x at height %d",
								addr, txnHash, hash, height,
							)
							skipCache = true
							continue outer
						}

						// publicKey field
						publicKey, ok := cadence.SearchFieldByName(
							event,
							stdlib.AccountEventPublicKeyIndexParameter.Identifier,
						).(cadence.String)
						if !ok {
							log.Errorf(
								"Unable to load public key from FlowColdStorageProxy.Created event in transaction %x in block %x at height %d",
								txnHash, hash, height,
							)
							skipCache = true
							continue outer
						}
						uncompressed, err := hex.DecodeString(string(publicKey))
						if err != nil {
							log.Errorf(
								"Unable to hex decode public key from FlowColdStorageProxy.Created event in transaction %x in block %x at height %d: %s",
								txnHash, hash, height, err,
							)
							skipCache = true
							continue outer
						}
						compressed, err := crypto.ConvertFlowPublicKey(uncompressed)
						if err != nil {
							log.Errorf(
								"Unable to compress public key from FlowColdStorageProxy.Created event in transaction %x in block %x at height %d: %s",
								txnHash, hash, height, err,
							)
							skipCache = true
							continue outer
						}
						accountKeys[string(addr[:])] = compressed
						newAccounts[string(addr[:])] = true
					case i.typProxyDeposited:
						// NOTE(tav): For all proxy accounts originated by us,
						// we will only ever make deposits to the proxy
						// account's vault once we've found the account address.
						// Therefore, we can process these events in the same
						// loop.
						event, err := decodeEvent("FlowColdStorageProxy.Deposited", evt, hash, height)
						if err != nil {
							skipCache = true
							continue outer
						}
						fields := event.FieldsMappedByName()
						if len(fields) != 2 {
							log.Errorf(
								"Found FlowColdStorageProxy.Deposited event with %d fields in transaction %x in block %x at height %d",
								len(fields), txnHash, hash, height,
							)
							skipCache = true
							continue outer
						}
						// account field
						addr, ok := cadence.SearchFieldByName(
							event,
							"account",
						).(cadence.Address)
						if !ok {
							log.Errorf(
								"Unable to load the `account` address from FlowColdStorageProxy.Deposited event in transaction %x in block %x at height %d",
								txnHash, hash, height,
							)
							skipCache = true
							continue outer
						}
						// amount field
						amountValue, ok := cadence.SearchFieldByName(
							event,
							"amount",
						).(cadence.UFix64)
						if !ok {
							log.Errorf(
								"Unable to load the `amount` from FlowColdStorageProxy.Deposited event in transaction %x in block %x at height %d",
								txnHash, hash, height,
							)
							skipCache = true
							continue outer
						}
						amount := uint64(amountValue)

						txn.Events = append(txn.Events, &model.TransferEvent{
							Amount:   amount,
							Receiver: addr[:],
							Type:     model.TransferType_PROXY_DEPOSIT,
						})
						// NOTE(tav): We only care about deposits to one of our
						// proxy accounts.
						if i.isProxy(addr[:], newAccounts) {
							proxyDeposits[string(addr[:])] += amount
						}
					case i.typProxyTransferred:
						// NOTE(tav): For all proxy accounts originated by us,
						// we will only ever make transfers once we've found the
						// account address. Therefore, we can process these
						// events in the same loop.
						event, err := decodeEvent("FlowColdStorageProxy.Transferred", evt, hash, height)
						if err != nil {
							skipCache = true
							continue outer
						}
						fields := event.FieldsMappedByName()
						if len(fields) != 3 {
							log.Errorf(
								"Found FlowColdStorageProxy.Transferred event with %d fields in transaction %x in block %x at height %d",
								len(fields), txnHash, hash, height,
							)
							skipCache = true
							continue outer
						}

						// 'to' field
						receiver, ok := cadence.SearchFieldByName(
							event,
							"to",
						).(cadence.Address)
						if !ok {
							log.Errorf(
								"Unable to load the `to` address from FlowColdStorageProxy.Transferred event in transaction %x in block %x at height %d",
								txnHash, hash, height,
							)
							skipCache = true
							continue outer
						}

						// 'from' field
						fromValue := cadence.SearchFieldByName(
							event,
							"from",
						).(cadence.Optional).Value
						var sender [8]byte

						if fromValue != nil {
							sender, ok = fromValue.(cadence.Address)
							if !ok {
								log.Errorf(
									"Unable to load the `from` address from FlowColdStorageProxy.Transferred event in transaction %x in block %x at height %d",
									txnHash, hash, height,
								)
								skipCache = true
								continue outer
							}
						}

						// 'amount' field
						amountValue, ok := cadence.SearchFieldByName(
							event,
							"amount",
						).(cadence.UFix64)
						if !ok {
							log.Errorf(
								"Unable to load the `amount` from FlowColdStorageProxy.Transferred event in transaction %x in block %x at height %d",
								txnHash, hash, height,
							)
							skipCache = true
							continue outer
						}
						amount := uint64(amountValue)
						txn.Events = append(txn.Events, &model.TransferEvent{
							Amount:   amount,
							Receiver: receiver[:],
							Sender:   sender[:],
							Type:     model.TransferType_PROXY_WITHDRAWAL,
						})
						// NOTE(tav): We only care about transfers from one of
						// our proxy accounts.
						if i.isProxy(sender[:], newAccounts) {
							if i.isTracked(receiver[:], newAccounts) {
								proxyTransfers = append(proxyTransfers, &proxyTransfer{
									amount:   amount,
									receiver: receiver[:],
									sender:   sender[:],
								})
							} else {
								proxyTransfers = append(proxyTransfers, &proxyTransfer{
									amount: amount,
									sender: sender[:],
								})
							}
						}
					}
				}
			evtloop2:
				for _, evt := range txnResult.Events {
					switch evt.Type {
					case i.typTokensDeposited:
						event, err := decodeEvent("FlowToken.TokensDeposited", evt, hash, height)
						if err != nil {
							skipCache = true
							continue outer
						}
						fields := event.FieldsMappedByName()
						if len(fields) != 2 {
							log.Errorf(
								"Found FlowToken.TokensDeposited event with %d fields in transaction %x in block %x at height %d",
								len(fields), txnHash, hash, height,
							)
							skipCache = true
							continue outer
						}

						// 'amount' field
						amountValue, ok := cadence.SearchFieldByName(
							event,
							"amount",
						).(cadence.UFix64)
						if !ok {
							log.Errorf(
								"Unable to load amount from FlowToken.TokensDeposited event in transaction %x in block %x at height %d",
								txnHash, hash, height,
							)
							skipCache = true
							continue outer
						}
						amount := uint64(amountValue)

						// 'to' field
						toValue := cadence.SearchFieldByName(
							event,
							"to",
						).(cadence.Optional).Value
						if toValue == nil {
							log.Warnf(
								"Ignoring FlowToken.TokensDeposited event with a nil address in transaction %x in block %x at height %d",
								txnHash, hash, height,
							)
							txn.Events = append(txn.Events, &model.TransferEvent{
								Amount: amount,
								Type:   model.TransferType_DEPOSIT,
							})
							continue evtloop2
						}
						receiver, ok := toValue.(cadence.Address)
						if !ok {
							log.Errorf(
								"Unable to load receiver address from FlowToken.TokensDeposited event in transaction %x in block %x at height %d",
								txnHash, hash, height,
							)
							skipCache = true
							continue outer
						}
						txn.Events = append(txn.Events, &model.TransferEvent{
							Amount:   amount,
							Receiver: receiver[:],
							Type:     model.TransferType_DEPOSIT,
						})
						if bytes.Equal(receiver[:], i.feeAddr) {
							// NOTE(tav): When the deposit is to the fee
							// address, just increment the fee amount.
							fees += amount
						} else if i.isTracked(receiver[:], newAccounts) && !i.isProxy(receiver[:], newAccounts) {
							// NOTE(tav): We only care about deposits to tracked
							// accounts, as long as they are not one of our
							// proxy accounts.
							//
							// In proxy accounts, deposits to the
							// FlowColdStorageProxy Vault, as opposed to the
							// default FlowToken Vault, should have already been
							// captured by FlowColdStorageProxy.Deposited
							// events.
							deposits[string(receiver[:])] += amount
						}
					case i.typTokensWithdrawn:
						event, err := decodeEvent("FlowToken.TokensWithdrawn", evt, hash, height)
						if err != nil {
							skipCache = true
							continue outer
						}
						fields := event.FieldsMappedByName()
						if len(fields) != 2 {
							log.Errorf(
								"Found FlowToken.TokensWithdrawn event with %d fields in transaction %x in block %x at height %d",
								len(fields), txnHash, hash, height,
							)
							skipCache = true
							continue outer
						}
						// 'amount' field
						amountValue, ok := cadence.SearchFieldByName(
							event,
							"amount",
						).(cadence.UFix64)
						if !ok {
							log.Errorf(
								"Unable to load amount from FlowToken.TokensWithdrawn event in transaction %x in block %x at height %d",
								txnHash, hash, height,
							)
							skipCache = true
							continue outer
						}
						amount := uint64(amountValue)

						// 'from' field
						fromValue := cadence.SearchFieldByName(
							event,
							"from",
						).(cadence.Optional).Value
						if fromValue == nil {
							log.Warnf(
								"Ignoring FlowToken.TokensWithdrawn event with a nil address in transaction %x in block %x at height %d",
								txnHash, hash, height,
							)
							txn.Events = append(txn.Events, &model.TransferEvent{
								Amount: amount,
								Type:   model.TransferType_WITHDRAWAL,
							})
							continue evtloop2
						}
						sender, ok := fromValue.(cadence.Address)
						if !ok {
							log.Errorf(
								"Unable to load sender address from FlowToken.TokensWithdrawn event in transaction %x in block %x at height %d",
								txnHash, hash, height,
							)
							skipCache = true
							continue outer
						}
						txn.Events = append(txn.Events, &model.TransferEvent{
							Amount: amount,
							Sender: sender[:],
							Type:   model.TransferType_WITHDRAWAL,
						})
						// NOTE(tav): We only care about withdrawals from our
						// tracked accounts, as long as they are not one of our
						// proxy accounts.
						//
						// In proxy accounts, transfers from the
						// FlowColdStorageProxy Vault should have already been
						// captured by FlowColdStorageProxy.Transferred events.
						if i.isTracked(sender[:], newAccounts) && !i.isProxy(sender[:], newAccounts) {
							withdrawals[string(sender[:])] += amount
						}
					}
				}
				// Create operations for the creation of new accounts, including
				// proxy accounts.
				for _, addr := range addrs {
					txn.Operations = append(txn.Operations, &model.Operation{
						Account:        addr,
						ProxyPublicKey: accountKeys[string(addr)],
						Type:           model.OperationType_CREATE_ACCOUNT,
					})
				}
				// Create a fee operation when the fee has been paid for by one
				// of our tracked accounts.
				if i.isTracked(payer, newAccounts) && fees > 0 {
					// NOTE(tav): This is theoretically possible if someone
					// manually deposits FLOW into the FlowFees contract.
					//
					// We explicitly disallow making direct transfers to the fee
					// address within transaction construction. But, just in
					// case, we add this additional check here which is
					// effectively a fatal error.
					if fees > withdrawals[string(payer)] {
						log.Errorf(
							"Amount taken from payer 0x%x (%d) is less than the fees (%d) in transaction %x in block %x at height %d",
							payer, withdrawals[string(payer)], fees, txnHash, hash, height,
						)
						skipCache = true
						continue outer
					}
					withdrawals[string(payer)] -= fees
					txn.Operations = append(txn.Operations, &model.Operation{
						Account: payer,
						Amount:  fees,
						Type:    model.OperationType_FEE,
					})
				}
				// For every proxy transfer where both the sender and receiver
				// are one of our accounts, match them up. Otherwise, generate a
				// single-sided operation.
				for _, transfer := range proxyTransfers {
					if transfer.amount == 0 {
						continue
					}
					if len(transfer.receiver) > 0 {
						if i.isProxy(transfer.receiver, newAccounts) {
							if transfer.amount > proxyDeposits[string(transfer.receiver)] {
								log.Errorf(
									"Amount deposited to proxy account 0x%x (%d) is less than the amount transferred from proxy account 0x%x (%d) in transaction %x in block %x at height %d",
									transfer.receiver, proxyDeposits[string(transfer.receiver)],
									transfer.sender, transfer.amount, txnHash, hash, height,
								)
								skipCache = true
								continue outer
							}
							proxyDeposits[string(transfer.receiver)] -= transfer.amount
						} else {
							if transfer.amount > deposits[string(transfer.receiver)] {
								log.Errorf(
									"Amount deposited to account 0x%x (%d) is less than the amount transferred from proxy account 0x%x (%d) in transaction %x in block %x at height %d",
									transfer.receiver, deposits[string(transfer.receiver)],
									transfer.sender, transfer.amount, txnHash, hash, height,
								)
								skipCache = true
								continue outer
							}
							deposits[string(transfer.receiver)] -= transfer.amount
						}
						txn.Operations = append(txn.Operations, &model.Operation{
							Account:  transfer.sender,
							Amount:   transfer.amount,
							Receiver: transfer.receiver,
							Type:     model.OperationType_PROXY_TRANSFER,
						})
					} else {
						txn.Operations = append(txn.Operations, &model.Operation{
							Account: transfer.sender,
							Amount:  transfer.amount,
							Type:    model.OperationType_PROXY_TRANSFER,
						})
					}
					transfers++
				}
				// We merge all remaining deposits and withdrawals with positive
				// amounts.
				receiver := ""
				remDeposits := map[string]uint64{}
				remWithdrawals := map[string]uint64{}
				sender := ""
				for acct, amount := range deposits {
					if amount > 0 {
						receiver = acct
						remDeposits[acct] = amount
					}
				}
				for acct, amount := range proxyDeposits {
					if amount > 0 {
						receiver = acct
						remDeposits[acct] = amount
					}
				}
				for acct, amount := range withdrawals {
					if amount > 0 {
						sender = acct
						remWithdrawals[acct] = amount
					}
				}
				// If there is a one-to-one send/receive of equal amounts, we
				// match them up. Otherwise, we generate a set of single-sided
				// operations.
				if len(remDeposits) == 1 && len(remWithdrawals) == 1 && remDeposits[receiver] == remWithdrawals[sender] {
					txn.Operations = append(txn.Operations, &model.Operation{
						Account:  []byte(sender),
						Amount:   remDeposits[receiver],
						Receiver: []byte(receiver),
						Type:     model.OperationType_TRANSFER,
					})
					transfers++
				} else {
					for acct, amount := range remDeposits {
						txn.Operations = append(txn.Operations, &model.Operation{
							Amount:   amount,
							Receiver: []byte(acct),
							Type:     model.OperationType_TRANSFER,
						})
						transfers++
					}
					for acct, amount := range remWithdrawals {
						txn.Operations = append(txn.Operations, &model.Operation{
							Account: []byte(acct),
							Amount:  amount,
							Type:    model.OperationType_TRANSFER,
						})
						transfers++
					}
				}
			}
		}
		if debug || skip || unsealed {
			typ := ""
			if skip {
				typ = "skipped "
			} else if unsealed {
				typ = "unsealed "
			}
			for _, txn := range data.Transactions {
				for _, op := range txn.Operations {
					log.Warnf(
						"Indexing op in transaction %x within %sblock %x at height %d: %s",
						txn.Hash, typ, hash, height, op.Pretty(),
					)
				}
			}
		}
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			err = i.Store.Index(ctx, height, hash, data)
			if err == nil {
				break
			}
			log.Errorf(
				"Failed to index block %x at height %d: %s",
				hash, height, err,
			)
			time.Sleep(10 * time.Millisecond)
		}
		if newCounter > 0 || transfers > 0 {
			log.Infof(
				"Indexed block %x at height %d (%d new accounts, %d transfers)",
				hash, height, newCounter, transfers,
			)
		} else {
			log.Infof("Indexed block %x at height %d", hash, height)
		}
		for acct, isProxy := range newAccounts {
			i.accts[acct] = isProxy
		}
		i.mu.Lock()
		i.lastIndexed.Hash = hash
		i.lastIndexed.Height = height
		i.mu.Unlock()
		delete(i.sealedResults, string(hash))
		trace.EndSpanOk(span)
		return
	}
}

type collectionData struct {
	system     bool
	txns       []*entities.Transaction
	txnResults []*flowaccess.TransactionResultResponse
}

type proxyTransfer struct {
	amount   uint64
	receiver []byte
	sender   []byte
}

func useSlowPath(err error) bool {
	if access.IsRateLimited(err) {
		return false
	}
	switch status.Code(err) {
	case codes.Internal:
		// NOTE(tav): Access API servers sometimes return an Internal error that
		// wraps around Unimplemented errors from upstream Execution Nodes.
		if strings.Contains(err.Error(), "ResourceExhausted") {
			return true
		}
		return strings.Contains(err.Error(), "Unimplemented")
	case codes.ResourceExhausted:
		// NOTE(tav): The response size for the batched APIs may sometimes
		// exceed the limit specified by Access API servers. Since we've already
		// ruled out rate limit errors, we assume that we've hit that case here.
		return true
	case codes.Unimplemented:
		return true
	default:
		return false
	}
}
