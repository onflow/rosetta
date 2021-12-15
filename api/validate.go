package api

import (
	"context"
	"time"

	"github.cbhq.net/nodes/rosetta-flow/log"
)

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
			for {
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
				onchain, xerr := s.getOnchainData(ctx, acct[:], latest.Hash)
				if xerr != nil {
					log.Errorf(
						"Failed to get on-chain balances for %x: %s", acct[:], xerr,
					)
					continue
				}
				indexed, err := s.Index.BalanceByHash(acct[:], latest.Hash)
				if err != nil {
					log.Errorf(
						"Failed to get indexed balance for %x: %s", acct[:], xerr,
					)
					continue
				}
				if isProxy != onchain.IsProxy {
					log.Fatalf(
						"Mismatching proxy account status for account %x: indexed %v, got on-chain %v",
						acct[:], isProxy, onchain.IsProxy,
					)
				}
				if onchain.IsProxy {
					if indexed.Balance != onchain.ProxyBalance {
						log.Fatalf(
							"Mismatching proxy balance found for account %x at block %x: indexed %d, got on-chain %d",
							acct[:], latest.Hash, indexed.Balance, onchain.ProxyBalance,
						)
					}
				} else if indexed.Balance != onchain.DefaultBalance {
					log.Fatalf(
						"Mismatching balance found for account %x at block %x: indexed %d, got on-chain %d",
						acct[:], latest.Hash, indexed.Balance, onchain.DefaultBalance,
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
