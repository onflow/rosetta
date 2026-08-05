package api

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/onflow/cadence"
	"github.com/onflow/rosetta/log"
)

// validateFeeReceivers checks the configured fee addresses (the FlowFees
// contract account plus .contracts.fee_receivers) against the fee receiver
// accounts the FlowFees contract rotates deposits across on chain. If an
// on-chain receiver is missing from the config, fee deposits to it would be
// misclassified as ordinary transfers, so we exit with a fatal error.
// Configured addresses that are no longer on chain are fine — they may be
// needed to classify fees in historical blocks.
func (s *Server) validateFeeReceivers(ctx context.Context) {
	if s.Offline {
		return
	}
	const attempts = 5
	for attempt := 1; attempt <= attempts; attempt++ {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if attempt > 1 {
			time.Sleep(time.Duration(attempt) * time.Second)
		}
		// Pick a client on each attempt so a retry can land on a different
		// access node if the previously selected one is unavailable.
		client := s.DataAccessNodes.Client()
		latest, err := client.LatestBlockHeader(ctx)
		if err != nil {
			log.Errorf("Failed to get the latest block header to validate fee receivers: %s", err)
			continue
		}
		resp, err := client.Execute(ctx, latest.Id, s.scriptGetFeeReceivers, nil)
		if err != nil {
			log.Errorf("Failed to execute the get_fee_receivers script: %s", err)
			continue
		}
		arr, ok := resp.(cadence.Array)
		if !ok {
			log.Errorf("Failed to convert get_fee_receivers result to an array: got %T", resp)
			return
		}
		onchain := []string{}
		missing := []string{}
		for _, val := range arr.Values {
			addr, ok := val.(cadence.Address)
			if !ok {
				log.Errorf("Failed to convert get_fee_receivers element to an address: got %T", val)
				return
			}
			onchain = append(onchain, addr.String())
			if !s.feeAddrs[string(addr.Bytes())] {
				missing = append(missing, addr.String())
			}
		}
		if len(missing) > 0 {
			log.Fatalf(
				"On-chain fee receiver account(s) %s are missing from the configured fee addresses: "+
					"fee deposits to them would be misclassified as transfers; add them to .contracts.fee_receivers",
				strings.Join(missing, ", "),
			)
		}
		log.Infof(
			"Validated the configured fee addresses against the on-chain fee receivers: %s",
			strings.Join(onchain, ", "),
		)
		return
	}
	log.Errorf("Giving up on fee receiver validation after %d attempts", attempts)
}

// NOTE(tav): We exit with a fatal error if the on-chain state doesn't match
// what we expect. This assumes that we can trust the data returned to us by the
// Access API servers, which may not necessarily be true.
func (s *Server) validateBalances(ctx context.Context) {
	if s.Offline {
		return
	}
	switch os.Getenv("DISABLE_BALANCE_VALIDATION") {
	case "false", "off", "0", "":
		// Continue on false-y values.
	default:
		return
	}
	log.Infof("Running background loop to validate account balances")
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		accts, err := s.Index.Accounts()
		if err != nil {
			log.Errorf("Failed to get accounts from the index: %s", err)
			time.Sleep(time.Second)
			continue
		}
		done := 0
		failed := 0
		wait := time.Duration(0)
		for acct, isProxy := range accts {
			// NOTE(tav): We skip validation of proxy accounts if the current
			// process has not been configured with a proxy contract address.
			//
			// We can end up with proxy accounts if we had configured a proxy
			// contract address previously and then removed the config at some
			// point.
			if isProxy && !s.Chain.IsProxyContractDeployed() {
				continue
			}
			initial := true
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
				for !s.Indexer.Synced() {
					time.Sleep(10 * time.Millisecond)
					wait += 10 * time.Millisecond
					if wait == time.Minute {
						wait = 0
						log.Errorf(
							"Balance validation not run due to indexer not being synced",
						)
					}
				}
				wait = 0
				latest := s.Index.Latest()
				indexed, err := s.Index.BalanceByHash(acct[:], latest.Hash)
				if err != nil {
					log.Errorf(
						"Failed to get indexed balance for %x at block %x (%d): %s",
						acct[:], latest.Hash, latest.Height, err,
					)
					continue
				}
				onchain, xerr := s.getOnchainData(ctx, acct[:], latest.Hash)
				if xerr != nil {
					log.Errorf(
						"Failed to get on-chain balances for %x at block %x (%d): %s",
						acct[:], latest.Hash, latest.Height, formatErr(xerr),
					)
					continue
				}
				if isProxy != onchain.IsProxy {
					s.setIndexedStateErr(
						"Mismatching proxy account status for account %x at block %x (%d): indexed %v, got on-chain %v",
						acct[:], latest.Hash, latest.Height, isProxy, onchain.IsProxy,
					)
					failed++
				}
				if onchain.IsProxy {
					if indexed.Balance != onchain.ProxyBalance {
						s.setIndexedStateErr(
							"Mismatching proxy balance found for account %x at block %x (%d): indexed %d, got on-chain %d",
							acct[:], latest.Hash, latest.Height, indexed.Balance, onchain.ProxyBalance,
						)
						failed++
					}
				} else if indexed.Balance != onchain.DefaultBalance {
					s.setIndexedStateErr(
						"Mismatching balance found for account %x at block %x (%d): indexed %d, got on-chain %d",
						acct[:], latest.Hash, latest.Height, indexed.Balance, onchain.DefaultBalance,
					)
					failed++
				}
				break
			}
			done++
			if failed == 0 {
				s.setValidationProgress(len(accts), done)
			}
			if done%1000 == 0 {
				if failed > 0 {
					log.Errorf(
						"Checked account balances for %d of %d accounts (%d failed)",
						done, len(accts), failed,
					)
				} else {
					// NOTE(tav): We use WARN-level logs in order to avoid being
					// sampled by certain logging infrastructure.
					log.Warnf(
						"Checked account balances for %d of %d accounts",
						done, len(accts),
					)
				}
			}
			time.Sleep(s.Chain.BalanceValidationInterval)
		}
		if len(accts) > 0 {
			if failed > 0 {
				log.Errorf(
					"Checked all account balances: %d accounts (%d failed)",
					len(accts), failed,
				)
			} else {
				// NOTE(tav): We use WARN-level logs in order to avoid being
				// sampled by certain logging infrastructure.
				log.Warnf(
					"Checked all account balances: %d accounts",
					len(accts),
				)
				s.setValidationSuccess(len(accts))
			}
		}
		time.Sleep(time.Minute)
	}
}
