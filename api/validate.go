package api

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/onflow/cadence"
	"github.com/onflow/rosetta/log"
)

const (
	feeValidateQuickAttempts   = 5                // short-backoff attempts before the slow poll
	feeValidateSlowInterval    = time.Minute      // retry interval after the quick attempts
	feeValidateRecheckInterval = 10 * time.Minute // re-check interval after a definitive result
)

// validateFeeReceivers runs a background loop that checks the fee addresses
// used to classify fee deposits (the configured fee addresses, overridden by
// the most recent indexed FlowFees.ChildFeeAccountsChanged event, if any)
// against the fee receiver accounts the FlowFees contract rotates deposits
// across on chain. If an on-chain receiver is missing from that set, fee
// deposits to it would be misclassified as ordinary transfers, so we log an
// error and surface the failure via the fee_receiver_validation_status /call
// method. Configured addresses that are no longer on chain are fine — they
// may be needed to classify fees in historical blocks.
//
// Transient failures are retried forever, and the check re-runs periodically
// to catch receivers added on chain at runtime.
func (s *Server) validateFeeReceivers(ctx context.Context) {
	if s.Offline {
		return
	}
	attempt := 0
	for {
		var delay time.Duration
		if s.checkFeeReceivers(ctx) {
			attempt = 0
			delay = feeValidateRecheckInterval
		} else {
			attempt++
			delay = time.Duration(attempt) * time.Second
			if attempt >= feeValidateQuickAttempts {
				delay = feeValidateSlowInterval
			}
		}
		if !sleepCtx(ctx, delay) {
			return
		}
	}
}

// sleepCtx sleeps for the given duration, returning early with false if the
// context is cancelled first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// checkFeeReceivers makes a single attempt at validating the fee addresses
// used for classification against the on-chain fee receivers, and records the
// outcome in the server's fee validation state. It returns false if the
// attempt failed and should be retried.
func (s *Server) checkFeeReceivers(ctx context.Context) bool {
	// We validate at the latest indexed block (the genesis block if nothing
	// has been indexed yet), rather than the latest block available on the
	// Access API: the fee addresses only matter for the blocks the indexer is
	// currently classifying, and this keeps the check consistent with the
	// indexed fee receiver overrides.
	latest := s.Index.Latest()
	if latest == nil {
		s.setFeeValidationRetrying(
			"Failed to validate fee receivers: no block has been indexed yet",
		)
		return false
	}
	// The latest indexed block may belong to a past spork while the indexer
	// is catching up, so we use the access nodes of the spork containing it —
	// and re-select a client on each attempt so a retry can land on a
	// different access node if the previously selected one is unavailable.
	spork := s.Chain.SporkFor(latest.Height)
	if spork == nil {
		s.setFeeValidationRetrying(
			"Failed to validate fee receivers: the latest indexed block at height %d cannot be associated with any sporks in the config",
			latest.Height,
		)
		return false
	}
	client := spork.AccessNodes.Client()
	resp, err := client.Execute(ctx, latest.Hash, s.scriptGetFeeReceivers, nil)
	if err != nil {
		if isMissingFeeReceiverFunc(err) {
			// The FlowFees contract predates the concurrent fee collection
			// upgrade (onflow/flow-core-contracts#575): the FlowFees account
			// is the only fee receiver until the contract is upgraded. We
			// record this as a successful validation and keep polling so
			// that a later upgrade is detected.
			s.setFeeValidationFallback()
			return true
		}
		s.setFeeValidationRetrying(
			"Failed to execute the get_fee_receivers script at the latest indexed block %x (%d): %s",
			latest.Hash, latest.Height, err,
		)
		return false
	}
	arr, ok := resp.(cadence.Array)
	if !ok {
		s.setFeeValidationRetrying(
			"Failed to convert get_fee_receivers result to an array: got %T", resp,
		)
		return false
	}
	feeAddrs := s.currentFeeAddrs(latest.Height)
	onchain := []string{}
	missing := []string{}
	for _, val := range arr.Values {
		addr, ok := val.(cadence.Address)
		if !ok {
			s.setFeeValidationRetrying(
				"Failed to convert get_fee_receivers element to an address: got %T", val,
			)
			return false
		}
		onchain = append(onchain, addr.String())
		if !feeAddrs[string(addr.Bytes())] {
			missing = append(missing, addr.String())
		}
	}
	if len(missing) > 0 {
		s.setFeeValidationFailure(onchain, missing)
	} else {
		s.setFeeValidationSuccess(onchain)
	}
	return true
}

// isMissingFeeReceiverFunc returns whether the script execution error
// indicates that the FlowFees contract predates the concurrent fee collection
// upgrade (onflow/flow-core-contracts#575), i.e. it does not define
// getFeeReceiverAddresses. This is a deterministic script type-checking
// failure, so retrying it would never succeed — unlike transient access node
// errors.
func isMissingFeeReceiverFunc(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "getFeeReceiverAddresses") &&
		(strings.Contains(msg, "has no member") || strings.Contains(msg, "cannot find"))
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
