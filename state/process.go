package state

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"time"

	"github.cbhq.net/nodes/rosetta-flow/cache"
	"github.cbhq.net/nodes/rosetta-flow/config"
	"github.cbhq.net/nodes/rosetta-flow/crypto"
	"github.cbhq.net/nodes/rosetta-flow/log"
	"github.cbhq.net/nodes/rosetta-flow/model"
	"github.com/onflow/flow-go/model/flow"
)

func (i *Indexer) processBlock(ctx context.Context, spork *config.Spork, height uint64, hash []byte) {
	// TODO(tav): Confirm that failed transactions do not emit invalid events.
	blockID := toFlowIdentifier(hash)
	_, skip := i.Chain.SkipBlocks[blockID]
	initial := true
outer:
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if initial {
			initial = false
		} else {
			time.Sleep(time.Second)
		}
		client := spork.AccessNodes.Client()
		block, err := client.BlockByHash(ctx, hash)
		if err != nil {
			log.Errorf("Failed to fetch block %x at height %d: %s", hash, height, err)
			continue
		}
		data := &model.IndexedBlock{
			Timestamp: uint64(block.Timestamp.AsTime().UnixNano()),
		}
		client = spork.AccessNodes.Client()
		eventHashes := []flow.Identifier{}
		newAccounts := map[string]bool{}
		newCounter := 0
		transfers := 0
		txnIndex := -1
		for _, col := range block.CollectionGuarantees {
			initialCol := true
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}
				if initialCol {
					initialCol = false
				} else {
					time.Sleep(time.Second)
				}
				client := spork.AccessNodes.Client()
				info, err := client.Collection(ctx, col.CollectionId)
				if err != nil {
					log.Errorf(
						"Failed to fetch collection %x in block %x at height %d: %s",
						col.CollectionId, hash, height, err,
					)
					continue
				}
				colEvents := []flowEvent{}
			txnloop:
				for _, txnHash := range info.TransactionIds {
					initialTxn := true
					txnIndex++
					for {
						select {
						case <-ctx.Done():
							return
						default:
						}
						if initialTxn {
							initialTxn = false
						} else {
							time.Sleep(time.Second)
						}
						txn := &model.IndexedTransaction{
							Hash: txnHash,
						}
						data.Transactions = append(data.Transactions, txn)
						client := spork.AccessNodes.Client()
						info, err := client.Transaction(ctx, txnHash)
						if err != nil {
							log.Errorf(
								"Failed to fetch transaction %x in block %x at height %d: %s",
								txnHash, hash, height, err,
							)
							continue
						}
						payer := info.Payer
						txnResult, err := client.TransactionResult(ctx, hash, uint32(txnIndex))
						if err != nil {
							log.Errorf(
								"Failed to fetch transaction result for %x in block %x at height %d: %s",
								txnHash, hash, height, err,
							)
							continue
						}
						// NOTE(tav): Sanity check that the block hashes match
						// in case there's a regression in the implementation of
						// the API.
						if !bytes.Equal(txnResult.BlockId, hash) {
							log.Errorf(
								"Found unexpected block %x in the transaction result for %x in block %x at height %d",
								txnResult.BlockId, txnHash, hash, height,
							)
							if !skip {
								continue
							}
						}
						if txnResult.StatusCode != 0 {
							txn.ErrorMessage = txnResult.ErrorMessage
							txn.Failed = true
						}
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
								return
							default:
							}
							colEvents = append(colEvents, flowEvent{
								EventIndex:       evt.EventIndex,
								Payload:          evt.Payload,
								TransactionID:    toFlowIdentifier(txnHash),
								TransactionIndex: evt.TransactionIndex,
								Type:             flow.EventType(evt.Type),
							})
							switch evt.Type {
							case "flow.AccountCreated":
								fields := decodeEvent("AccountCreated", evt, hash, height)
								if fields == nil {
									continue outer
								}
								if len(fields) != 1 {
									log.Errorf(
										"Found AccountCreated event with %d fields in transaction %x in block %x at height %d",
										len(fields), txnHash, hash, height,
									)
									continue outer
								}
								addr, ok := fields[0].([8]byte)
								if !ok {
									log.Errorf(
										"Unable to load address from AccountCreated event in transaction %x in block %x at height %d",
										txnHash, hash, height,
									)
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
									continue outer
								}
								if len(fields) != 2 {
									log.Errorf(
										"Found Proxy.Created event with %d fields in transaction %x in block %x at height %d",
										len(fields), txnHash, hash, height,
									)
									continue outer
								}
								addr, ok := fields[0].([8]byte)
								if !ok {
									log.Errorf(
										"Unable to load address from Proxy.Created event in transaction %x in block %x at height %d",
										txnHash, hash, height,
									)
									continue outer
								}
								_, ok = accountKeys[string(addr[:])]
								if !ok {
									log.Errorf(
										"Missing AccountCreated event for address %x from Proxy.Created event in transaction %x in block %x at height %d",
										addr, txnHash, hash, height,
									)
									continue outer
								}
								publicKey, ok := fields[1].(string)
								if !ok {
									log.Errorf(
										"Unable to load public key from Proxy.Created event in transaction %x in block %x at height %d",
										txnHash, hash, height,
									)
									continue outer
								}
								uncompressed, err := hex.DecodeString(publicKey)
								if err != nil {
									log.Errorf(
										"Unable to hex decode public key from Proxy.Created event in transaction %x in block %x at height %d: %s",
										txnHash, hash, height, err,
									)
									continue outer
								}
								compressed, err := crypto.ConvertFlowPublicKey(uncompressed)
								if err != nil {
									log.Errorf(
										"Unable to compress public key from Proxy.Created event in transaction %x in block %x at height %d: %s",
										txnHash, hash, height, err,
									)
									continue outer
								}
								accountKeys[string(addr[:])] = compressed
								newAccounts[string(addr[:])] = true
							case i.typProxyDeposited:
								fields := decodeEvent("Proxy.Deposited", evt, hash, height)
								if fields == nil {
									continue outer
								}
								if len(fields) != 2 {
									log.Errorf(
										"Found Proxy.Deposited event with %d fields in transaction %x in block %x at height %d",
										len(fields), txnHash, hash, height,
									)
									continue outer
								}
								addr, ok := fields[0].([8]byte)
								if !ok {
									log.Errorf(
										"Unable to load the `account` address from Proxy.Deposited event in transaction %x in block %x at height %d",
										txnHash, hash, height,
									)
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
									continue outer
								}
								proxyDeposits = append(proxyDeposits, &proxyDeposit{
									amount:   amount,
									receiver: addr,
								})
							case i.typProxyTransferred:
								fields := decodeEvent("Proxy.Transferred", evt, hash, height)
								if fields == nil {
									continue outer
								}
								if len(fields) != 3 {
									log.Errorf(
										"Found Proxy.Transferred event with %d fields in transaction %x in block %x at height %d",
										len(fields), txnHash, hash, height,
									)
									continue outer
								}
								sender, ok := fields[0].([8]byte)
								if !ok {
									log.Errorf(
										"Unable to load the `from` address from Proxy.Transferred event in transaction %x in block %x at height %d",
										txnHash, hash, height,
									)
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
									continue outer
								}
								if len(fields) != 2 {
									log.Errorf(
										"Found FlowToken.TokensDeposited event with %d fields in transaction %x in block %x at height %d",
										len(fields), txnHash, hash, height,
									)
									continue outer
								}
								amount, ok := fields[0].(uint64)
								if !ok {
									log.Errorf(
										"Unable to load amount from FlowToken.TokensDeposited event in transaction %x in block %x at height %d",
										txnHash, hash, height,
									)
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
									continue outer
								}
								if len(fields) != 2 {
									log.Errorf(
										"Found FlowToken.TokensWithdrawn event with %d fields in transaction %x in block %x at height %d",
										len(fields), txnHash, hash, height,
									)
									continue outer
								}
								amount, ok := fields[0].(uint64)
								if !ok {
									log.Errorf(
										"Unable to load amount from FlowToken.TokensWithdrawn event in transaction %x in block %x at height %d",
										txnHash, hash, height,
									)
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
									continue outer
								}
								if !bytes.Equal(senders[0], proxy.sender) {
									log.Errorf(
										"Proxy transfer sender (%x) does not match account inferred from FlowToken events (%x) in transaction %x in block %x at height %d",
										proxy.sender, senders[0], txnHash, hash, height,
									)
									continue outer
								}
								if !bytes.Equal(receivers[0], proxy.receiver) {
									log.Errorf(
										"Proxy transfer receiver (%x) does not match account inferred from FlowToken events (%x) in transaction %x in block %x at height %d",
										proxy.receiver, receivers[0], txnHash, hash, height,
									)
									continue outer
								}
							default:
								log.Errorf(
									"Found multiple proxy transfers within transaction %x in block %x at height %d from one of our tracked accounts",
									txnHash, hash, height,
								)
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
						break
					}
				}
				eventHashes = append(eventHashes, deriveEventsHash(spork, colEvents))
				break
			}
		}
		txnIndex++
		txnResult, err := client.TransactionResult(ctx, hash, uint32(txnIndex))
		if err != nil {
			log.Errorf(
				"Failed to fetch transaction result at index %d in block %x at height %d: %s",
				txnIndex, hash, height, err,
			)
			continue
		}
		colEvents := []flowEvent{}
		for _, evt := range txnResult.Events {
			colEvents = append(colEvents, flowEvent{
				EventIndex:       evt.EventIndex,
				Payload:          evt.Payload,
				TransactionID:    toFlowIdentifier(evt.TransactionId),
				TransactionIndex: evt.TransactionIndex,
				Type:             flow.EventType(evt.Type),
			})
		}
		eventHashes = append(eventHashes, deriveEventsHash(spork, colEvents))
		client = spork.AccessNodes.Client()
		execResult, err := client.ExecutionResultForBlockHash(ctx, hash)
		if err != nil {
			log.Errorf(
				"Failed to fetch execution result for block %x at height %d: %s",
				hash, height, err,
			)
			time.Sleep(time.Second)
			continue
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
				time.Sleep(time.Second)
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
				time.Sleep(time.Second)
				continue outer
			}
			if !bytes.Equal(chunk.EventCollection, eventHash[:]) {
				log.Errorf(
					"Got mismatching event hash within chunk at offset %d of block %x at height %d: expected %x, got %x",
					idx, hash, height, eventHash[:], chunk.EventCollection,
				)
				if skip {
					continue
				}
				continue outer
			}
		}
		exec, ok := convertExecutionResult(hash, height, execResult)
		if !ok {
			time.Sleep(time.Second)
			continue
		}
		resultID := deriveExecutionResult(spork, exec)
		sealedResult, ok := i.sealedResults[string(hash)]
		if ok {
			if string(resultID[:]) != sealedResult {
				log.Errorf(
					"Got mismatching execution result hash for block %x at height %d: expected %x, got %x",
					hash, height, sealedResult, resultID[:],
				)
				if !skip {
					time.Sleep(time.Second)
					continue
				}
			}
		} else if spork.Next == nil {
			if !skip {
				log.Fatalf(
					"No sealed result found for block %x at height %d in the live spork",
					hash, height,
				)
			}
		} else {
			// NOTE(tav): The last few blocks of a spork may not be sealed with
			// a result. Instead, they are implicitly sealed by the root
			// protocol state snapshot.
			log.Errorf(
				"No sealed result found for block %x at height %d",
				hash, height,
			)
		}
		if debug {
			for _, txn := range data.Transactions {
				for _, op := range txn.Operations {
					log.Infof("Indexing op: %s", op)
				}
			}
		}
		err = i.Store.Index(height, hash, data)
		if err != nil {
			log.Errorf(
				"Failed to index block %x at height %d: %s",
				hash, height, err,
			)
			time.Sleep(time.Second)
			continue
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
		return
	}
}

func (i *Indexer) runWorker(ctx context.Context, id int) {
	const retries = 5
	ctx = cache.Context(ctx, fmt.Sprintf("worker %d", id))
	for {
		select {
		case <-ctx.Done():
			return
		case height := <-i.jobs:
			var lastErr error
			spork := i.Chain.SporkFor(height)
		retry:
			for attempt := 0; attempt < retries; attempt++ {
				select {
				case <-ctx.Done():
					return
				default:
				}
				if attempt > 0 {
					time.Sleep(time.Second)
				}
				client := spork.AccessNodes.Client()
				hdr, err := client.BlockHeaderByHeight(ctx, height)
				if err != nil {
					lastErr = err
					continue
				}
				_, err = client.BlockHeaderByHash(ctx, hdr.Id)
				if err != nil {
					lastErr = err
					continue
				}
				block, err := client.BlockByHash(ctx, hdr.Id)
				if err != nil {
					lastErr = err
					continue
				}
				txnIndex := -1
				for _, col := range block.CollectionGuarantees {
					info, err := client.Collection(ctx, col.CollectionId)
					if err != nil {
						lastErr = err
						continue retry
					}
					for _, txn := range info.TransactionIds {
						txnIndex++
						_, err := client.Transaction(ctx, txn)
						if err != nil {
							lastErr = err
							continue retry
						}
						_, err = client.TransactionResult(ctx, hdr.Id, uint32(txnIndex))
						if err != nil {
							lastErr = err
							continue retry
						}
					}
				}
				txnIndex++
				_, err = client.TransactionResult(ctx, hdr.Id, uint32(txnIndex))
				if err != nil {
					lastErr = err
					continue
				}
				_, err = client.ExecutionResultForBlockHash(ctx, block.Id)
				if err != nil {
					lastErr = err
					continue
				}
				lastErr = nil
				if debug {
					log.Infof(
						"Successfully prefetched Access API calls for height %d (worker %d)",
						height, id,
					)
				}
				break
			}
			if lastErr != nil {
				log.Errorf(
					"Failed to prefetch Access API calls for height %d: %s (worker %d)",
					height, lastErr, id,
				)
			}
		}
	}
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
