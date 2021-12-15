package api

import (
	"context"
	"time"

	"github.com/onflow/rosetta/log"
)

// NOTE(tav): We exit with a fatal error if the on-chain state doesn't match
// what we expect. This assumes that we can trust the data returned to us by the
// Access API servers, which may not necessarily be true.
func (s *Server) validateBalances(ctx context.Context) {
	if s.Offline {
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
				onchain, xerr := s.getOnchainData(ctx, acct[:], latest.Hash, latest.Height)
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
				}
				if onchain.IsProxy {
					if indexed.Balance != onchain.ProxyBalance {
						s.setIndexedStateErr(
							"Mismatching proxy balance found for account %x at block %x (%d): indexed %d, got on-chain %d",
							acct[:], latest.Hash, latest.Height, indexed.Balance, onchain.ProxyBalance,
						)
					}
				} else if indexed.Balance != onchain.DefaultBalance {
					s.setIndexedStateErr(
						"Mismatching balance found for account %x at block %x (%d): indexed %d, got on-chain %d",
						acct[:], latest.Hash, latest.Height, indexed.Balance, onchain.DefaultBalance,
					)
				}
				break
			}
			time.Sleep(time.Second)
		}
		if len(accts) > 0 {
			log.Infof("Successfully validated all account balances: %d accounts", len(accts))
		}
		time.Sleep(time.Minute)
	}
}
