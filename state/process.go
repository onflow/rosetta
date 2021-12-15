package state

import (
	"bytes"
	"context"
	"encoding/hex"
	"strings"
	"time"

	"github.com/onflow/rosetta/cache"
	"github.com/onflow/rosetta/config"
	"github.com/onflow/rosetta/crypto"
	"github.com/onflow/rosetta/log"
	"github.com/onflow/rosetta/model"
	"github.com/onflow/rosetta/trace"
	"github.com/onflow/flow-go/model/flow"
	"github.com/onflow/flow/protobuf/go/flow/access"
	"github.com/onflow/flow/protobuf/go/flow/entities"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	indexerBlockHeight = trace.Gauge("state", "indexer_block_height")
)

func (i *Indexer) processBlock(rctx context.Context, spork *config.Spork, height uint64, hash []byte) {
	// TODO(tav): Confirm that failed transactions do not emit invalid events.
	indexerBlockHeight.Observe(rctx, int64(height))
	blockID := toFlowIdentifier(hash)
	ctx := rctx
	_, skip := i.Chain.SkipBlocks[blockID]
	initial := true
	skipCache := false
	skipCacheWarned := false
	slowPath := false
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
			time.Sleep(time.Second)
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
			txnResults []*access.TransactionResultResponse
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
			time.Sleep(time.Second)
		}
		// TODO(tav): Once Flow eventually implements the splitting up of
		// collections across multiple chunks, we would need to update the
		// events hash verification mechanism.
		if len(execResult.Chunks) != len(eventHashes) {
			log.Errorf(
				"Execution result for block %x at height %d contains %d chunks, expected %d",
				hash, height, len(execResult.Chunks), len(eventHashes),
			)
			if !skip {
				skipCache = true
				continue
			}
		}
		for idx, eventHash := range eventHashes {
			chunk := execResult.Chunks[idx]
			if chunk == nil {
				log.Errorf(
					"Got unexpected nil chunk at offset %d in the execution result for block %x at height %d",
					idx, hash, height,
				)
				if skip {
					continue
				}
				skipCache = true
				continue outer
			}
			if !bytes.Equal(chunk.EventCollection, eventHash[:]) {
				log.Errorf(
					"Got mismatching event hash within chunk at offset %d of block %x at height %d: expected %x (from events), got %x (from execution result)",
					idx, hash, height, eventHash[:], chunk.EventCollection,
				)
				if skip {
					continue
				}
				skipCache = true
				continue outer
			}
		}
		exec, ok := convertExecutionResult(hash, height, execResult)
		if !ok {
			skipCache = true
			continue
		}
		resultID := deriveExecutionResult(spork, exec)
		sealedResult, ok := i.sealedResults[string(hash)]
		// NOTE(tav): Skip execution result check for the root block of a spork
		// as it is self-sealed.
		if spork.Prev != nil && height == spork.RootBlock {
			sealedResult = string(resultID[:])
		}
		if ok {
			if string(resultID[:]) != sealedResult {
				log.Errorf(
					"Got mismatching execution result hash for block %x at height %d: expected %x, got %x",
					hash, height, sealedResult, resultID[:],
				)
				if !skip {
					skipCache = true
					continue
				}
			}
		} else if spork.Next == nil {
			if skip {
				log.Errorf(
					"No sealed result found for block %x at height %d in the live spork (skipping)",
					hash, height,
				)
			} else {
				log.Fatalf(
					"No sealed result found for block %x at height %d in the live spork",
					hash, height,
				)
			}
		} else {
			// NOTE(tav): The last few blocks of a spork may not be sealed with
			// a result. Instead, they are implicitly sealed by the root
			// protocol state snapshot.
			if skip {
				log.Errorf(
					"No sealed result found for block %x at height %d (skipping)",
					hash, height,
				)
			} else if height >= spork.Next.RootBlock-i.Chain.SporkSealTolerance {
				log.Errorf(
					"No sealed result found for block %x at height %d (within spork end tolerance)",
					hash, height,
				)
			} else {
				log.Fatalf(
					"No sealed result found for block %x at height %d",
					hash, height,
				)
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
		txnloop:
			for idx, txnResult := range col.txnResults {
				payer := col.txns[idx].Payer
				txnHash := txnResult.TransactionId
				// NOTE(tav): Sanity check that the block hashes match in case
				// there's a regression in the Access API server implementation.
				if !bytes.Equal(txnResult.BlockId, hash) {
					log.Errorf(
						"Found unexpected block %x in the transaction result for %x in block %x at height %d",
						txnResult.BlockId, txnHash, hash, height,
					)
					if !skip {
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
				deposits := []*transferEvent{}
				fees := uint64(0)
				feeDeposits := []*transferEvent{}
				matched := false
				proxyDeposits := []*proxyDeposit{}
				proxyTransfers := []*proxyTransfer{}
				withdrawals := []*transferEvent{}
			evtloop:
				for _, evt := range txnResult.Events {
					select {
					case <-ctx.Done():
						span.End()
						return
					default:
					}
					switch evt.Type {
					case "flow.AccountCreated":
						fields := decodeEvent("AccountCreated", evt, hash, height)
						if fields == nil {
							skipCache = true
							continue outer
						}
						if len(fields) != 1 {
							log.Errorf(
								"Found AccountCreated event with %d fields in transaction %x in block %x at height %d",
								len(fields), txnHash, hash, height,
							)
							skipCache = true
							continue outer
						}
						addr, ok := fields[0].([8]byte)
						if !ok {
							log.Errorf(
								"Unable to load address from AccountCreated event in transaction %x in block %x at height %d",
								txnHash, hash, height,
							)
							skipCache = true
							continue outer
						}
						if i.originators[string(addr[:])] || i.isTracked(payer, newAccounts) {
							accountKeys[string(addr[:])] = nil
							addrs = append(addrs, addr[:])
							newAccounts[string(addr[:])] = false
							newCounter++
						}

					case i.typProxyCreated:
						fields := decodeEvent("Proxy.Created", evt, hash, height)
						if fields == nil {
							skipCache = true
							continue outer
						}
						if !i.isTracked(payer, newAccounts) {
							continue evtloop
						}
						if len(accountKeys) == 0 {
							log.Errorf(
								"Found Proxy.Created event without a corresponding AccountCreated event in transaction %x in block %x at height %d",
								txnHash, hash, height,
							)
							skipCache = true
							continue outer
						}
						if len(fields) != 2 {
							log.Errorf(
								"Found Proxy.Created event with %d fields in transaction %x in block %x at height %d",
								len(fields), txnHash, hash, height,
							)
							skipCache = true
							continue outer
						}
						addr, ok := fields[0].([8]byte)
						if !ok {
							log.Errorf(
								"Unable to load address from Proxy.Created event in transaction %x in block %x at height %d",
								txnHash, hash, height,
							)
							skipCache = true
							continue outer
						}
						_, ok = accountKeys[string(addr[:])]
						if !ok {
							log.Errorf(
								"Missing AccountCreated event for address %x from Proxy.Created event in transaction %x in block %x at height %d",
								addr, txnHash, hash, height,
							)
							skipCache = true
							continue outer
						}
						publicKey, ok := fields[1].(string)
						if !ok {
							log.Errorf(
								"Unable to load public key from Proxy.Created event in transaction %x in block %x at height %d",
								txnHash, hash, height,
							)
							skipCache = true
							continue outer
						}
						uncompressed, err := hex.DecodeString(publicKey)
						if err != nil {
							log.Errorf(
								"Unable to hex decode public key from Proxy.Created event in transaction %x in block %x at height %d: %s",
								txnHash, hash, height, err,
							)
							skipCache = true
							continue outer
						}
						compressed, err := crypto.ConvertFlowPublicKey(uncompressed)
						if err != nil {
							log.Errorf(
								"Unable to compress public key from Proxy.Created event in transaction %x in block %x at height %d: %s",
								txnHash, hash, height, err,
							)
							skipCache = true
							continue outer
						}
						accountKeys[string(addr[:])] = compressed
						newAccounts[string(addr[:])] = true
					case i.typProxyDeposited:
						fields := decodeEvent("Proxy.Deposited", evt, hash, height)
						if fields == nil {
							skipCache = true
							continue outer
						}
						if len(fields) != 2 {
							log.Errorf(
								"Found Proxy.Deposited event with %d fields in transaction %x in block %x at height %d",
								len(fields), txnHash, hash, height,
							)
							skipCache = true
							continue outer
						}
						addr, ok := fields[0].([8]byte)
						if !ok {
							log.Errorf(
								"Unable to load the `account` address from Proxy.Deposited event in transaction %x in block %x at height %d",
								txnHash, hash, height,
							)
							skipCache = true
							continue outer
						}
						if !i.isProxy(addr[:], newAccounts) {
							continue evtloop
						}
						amount, ok := fields[1].(uint64)
						if !ok {
							log.Errorf(
								"Unable to load the `amount` from Proxy.Deposited event in transaction %x in block %x at height %d",
								txnHash, hash, height,
							)
							skipCache = true
							continue outer
						}
						proxyDeposits = append(proxyDeposits, &proxyDeposit{
							amount:   amount,
							receiver: addr,
						})
					case i.typProxyTransferred:
						fields := decodeEvent("Proxy.Transferred", evt, hash, height)
						if fields == nil {
							skipCache = true
							continue outer
						}
						if len(fields) != 3 {
							log.Errorf(
								"Found Proxy.Transferred event with %d fields in transaction %x in block %x at height %d",
								len(fields), txnHash, hash, height,
							)
							skipCache = true
							continue outer
						}
						sender, ok := fields[0].([8]byte)
						if !ok {
							log.Errorf(
								"Unable to load the `from` address from Proxy.Transferred event in transaction %x in block %x at height %d",
								txnHash, hash, height,
							)
							skipCache = true
							continue outer
						}
						if i.isTracked(sender[:], newAccounts) {
							matched = true
						}
						receiver, ok := fields[1].([8]byte)
						if !ok {
							log.Errorf(
								"Unable to load the `to` address from Proxy.Transferred event in transaction %x in block %x at height %d",
								txnHash, hash, height,
							)
							skipCache = true
							continue outer
						}
						if i.isTracked(receiver[:], newAccounts) {
							matched = true
						}
						amount, ok := fields[2].(uint64)
						if !ok {
							log.Errorf(
								"Unable to load the `amount` from Proxy.Transferred event in transaction %x in block %x at height %d",
								txnHash, hash, height,
							)
							skipCache = true
							continue outer
						}
						proxyTransfers = append(proxyTransfers, &proxyTransfer{
							amount:   amount,
							receiver: receiver[:],
							sender:   sender[:],
						})
					case i.typTokensDeposited:
						fields := decodeEvent("FlowToken.TokensDeposited", evt, hash, height)
						if fields == nil {
							skipCache = true
							continue outer
						}
						if len(fields) != 2 {
							log.Errorf(
								"Found FlowToken.TokensDeposited event with %d fields in transaction %x in block %x at height %d",
								len(fields), txnHash, hash, height,
							)
							skipCache = true
							continue outer
						}
						amount, ok := fields[0].(uint64)
						if !ok {
							log.Errorf(
								"Unable to load amount from FlowToken.TokensDeposited event in transaction %x in block %x at height %d",
								txnHash, hash, height,
							)
							skipCache = true
							continue outer
						}
						if fields[1] == nil {
							log.Warnf(
								"Ignoring FlowToken.TokensDeposited event with a nil address in transaction %x in block %x at height %d",
								txnHash, hash, height,
							)
							continue evtloop
						}
						receiver, ok := fields[1].([8]byte)
						if !ok {
							log.Errorf(
								"Unable to load receiver address from FlowToken.TokensDeposited event in transaction %x in block %x at height %d",
								txnHash, hash, height,
							)
							skipCache = true
							continue outer
						}
						if bytes.Equal(receiver[:], i.feeAddr) {
							fees += amount
							feeDeposits = append(feeDeposits, &transferEvent{
								account: receiver[:],
								amount:  amount,
							})
						} else {
							if i.isTracked(receiver[:], newAccounts) {
								// NOTE(tav): We handle this special case, as it's
								// possible for us to receive a deposit event to one of
								// our originated proxy accounts, but where the deposit
								// is to the default FLOW vault and not to the
								// Proxy.Vault, e.g. due to topping up the minimum
								// storage balance on an account.
								if i.isProxy(receiver[:], newAccounts) {
									for _, proxyDeposit := range proxyDeposits {
										if receiver == proxyDeposit.receiver && amount == proxyDeposit.amount {
											matched = true
											deposits = append(deposits, &transferEvent{
												account: receiver[:],
												amount:  amount,
											})
										}
									}
									continue evtloop
								}
								matched = true
							}
							deposits = append(deposits, &transferEvent{
								account: receiver[:],
								amount:  amount,
							})
						}
					case i.typTokensWithdrawn:
						fields := decodeEvent("FlowToken.TokensWithdrawn", evt, hash, height)
						if fields == nil {
							skipCache = true
							continue outer
						}
						if len(fields) != 2 {
							log.Errorf(
								"Found FlowToken.TokensWithdrawn event with %d fields in transaction %x in block %x at height %d",
								len(fields), txnHash, hash, height,
							)
							skipCache = true
							continue outer
						}
						amount, ok := fields[0].(uint64)
						if !ok {
							log.Errorf(
								"Unable to load amount from FlowToken.TokensWithdrawn event in transaction %x in block %x at height %d",
								txnHash, hash, height,
							)
							skipCache = true
							continue outer
						}
						if fields[1] == nil {
							log.Warnf(
								"Ignoring FlowToken.TokensWithdrawn event with a nil address in transaction %x in block %x at height %d",
								txnHash, hash, height,
							)
							continue evtloop
						}
						sender, ok := fields[1].([8]byte)
						if !ok {
							log.Errorf(
								"Unable to load sender address from FlowToken.TokensWithdrawn event in transaction %x in block %x at height %d",
								txnHash, hash, height,
							)
							skipCache = true
							continue outer
						}
						if i.isTracked(sender[:], newAccounts) {
							matched = true
						}
						withdrawals = append(withdrawals, &transferEvent{
							account: sender[:],
							amount:  amount,
						})
					}
				}
				for _, addr := range addrs {
					txn.Operations = append(txn.Operations, &model.Operation{
						Account:        addr,
						ProxyPublicKey: accountKeys[string(addr)],
						Type:           model.OperationType_CREATE_ACCOUNT,
					})
				}
				for _, deposit := range deposits {
					txn.Events = append(txn.Events, &model.TransferEvent{
						Account: deposit.account,
						Amount:  deposit.amount,
						Type:    model.TransferType_DEPOSIT,
					})
				}
				for _, deposit := range feeDeposits {
					txn.Events = append(txn.Events, &model.TransferEvent{
						Account: deposit.account,
						Amount:  deposit.amount,
						Type:    model.TransferType_DEPOSIT,
					})
				}
				for _, withdrawal := range withdrawals {
					txn.Events = append(txn.Events, &model.TransferEvent{
						Account: withdrawal.account,
						Amount:  withdrawal.amount,
						Type:    model.TransferType_WITHDRAWAL,
					})
				}
				if !matched {
					continue txnloop
				}
				external := !i.isTracked([]byte(payer), newAccounts)
				// NOTE(tav): We only care about deposits when the payer is
				// not one of the originated accounts.
				if external {
					for _, deposit := range deposits {
						if i.isTracked(deposit.account, newAccounts) {
							txn.Operations = append(txn.Operations, &model.Operation{
								Amount:   deposit.amount,
								Receiver: deposit.account,
								Type:     model.OperationType_TRANSFER,
							})
							transfers++
						}
					}
					continue txnloop
				}
				depositAmounts := map[string]uint64{}
				withdrawalAmounts := map[string]uint64{}
				for _, deposit := range deposits {
					depositAmounts[string(deposit.account)] += deposit.amount
				}
				for _, withdrawal := range withdrawals {
					withdrawalAmounts[string(withdrawal.account)] += withdrawal.amount
				}
				if fees > 0 {
					// NOTE(tav): This is theoretically possible if someone
					// manually deposits FLOW into the FlowFees contract.
					if fees > withdrawalAmounts[string(payer)] {
						log.Errorf(
							"Amount taken from payer %x (%d) is less than the fees (%d) in transaction %x in block %x at height %d",
							payer, withdrawalAmounts[string(payer)], fees, txnHash, hash, height,
						)
						fees = withdrawalAmounts[string(payer)]
					}
					withdrawalAmounts[string(payer)] -= fees
					txn.Operations = append(txn.Operations, &model.Operation{
						Account: []byte(payer),
						Amount:  fees,
						Type:    model.OperationType_FEE,
					})
				}
				receivers := [][]byte{}
				received := uint64(0)
				for acct, amount := range depositAmounts {
					if i.isTracked([]byte(acct), newAccounts) && amount > 0 {
						received += amount
						receivers = append(receivers, []byte(acct))
					}
				}
				senders := [][]byte{}
				sent := uint64(0)
				for acct, amount := range withdrawalAmounts {
					if i.isTracked([]byte(acct), newAccounts) && amount > 0 {
						sent += amount
						senders = append(senders, []byte(acct))
					}
				}
				if sent > 0 && sent == received && len(receivers) == 1 && len(senders) == 1 {
					typ := model.OperationType_TRANSFER
					switch len(proxyTransfers) {
					case 0:
					case 1:
						typ = model.OperationType_PROXY_TRANSFER
						proxy := proxyTransfers[0]
						if proxy.amount != sent {
							log.Errorf(
								"Proxy transfer amount of %d does not match amount inferred from FlowToken events of %d in transaction %x in block %x at height %d",
								proxy.amount, sent, txnHash, hash, height,
							)
							skipCache = true
							continue outer
						}
						if !bytes.Equal(senders[0], proxy.sender) {
							log.Errorf(
								"Proxy transfer sender (%x) does not match account inferred from FlowToken events (%x) in transaction %x in block %x at height %d",
								proxy.sender, senders[0], txnHash, hash, height,
							)
							skipCache = true
							continue outer
						}
						if !bytes.Equal(receivers[0], proxy.receiver) {
							log.Errorf(
								"Proxy transfer receiver (%x) does not match account inferred from FlowToken events (%x) in transaction %x in block %x at height %d",
								proxy.receiver, receivers[0], txnHash, hash, height,
							)
							skipCache = true
							continue outer
						}
					default:
						log.Errorf(
							"Found multiple proxy transfers within transaction %x in block %x at height %d from one of our tracked accounts",
							txnHash, hash, height,
						)
						skipCache = true
						continue outer
					}
					txn.Operations = append(txn.Operations, &model.Operation{
						Account:  senders[0],
						Amount:   sent,
						Receiver: receivers[0],
						Type:     typ,
					})
					transfers++
					continue txnloop
				}
				for acct, amount := range depositAmounts {
					if i.isTracked([]byte(acct), newAccounts) && amount > 0 {
						txn.Operations = append(txn.Operations, &model.Operation{
							Amount:   amount,
							Receiver: []byte(acct),
							Type:     model.OperationType_TRANSFER,
						})
						transfers++
					}
				}
				for acct, amount := range withdrawalAmounts {
					if i.isTracked([]byte(acct), newAccounts) && amount > 0 {
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
		if debug {
			for _, txn := range data.Transactions {
				for _, op := range txn.Operations {
					log.Infof("Indexing op: %s", op)
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
	txnResults []*access.TransactionResultResponse
}

type proxyDeposit struct {
	amount   uint64
	receiver [8]byte
}

type proxyTransfer struct {
	amount   uint64
	receiver []byte
	sender   []byte
}

type transferEvent struct {
	account []byte
	amount  uint64
}

func useSlowPath(err error) bool {
	switch status.Code(err) {
	case codes.ResourceExhausted:
		msg := err.Error()

		return !strings.Contains(msg, "rate limit exceeded")
	case codes.Unimplemented:
		return true
	default:
		return false
	}
}
