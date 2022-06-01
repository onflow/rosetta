// Command reconciliation helps debug rosetta-cli reconciliation errors.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/coinbase/rosetta-sdk-go/asserter"
	"github.com/coinbase/rosetta-sdk-go/fetcher"
	"github.com/coinbase/rosetta-sdk-go/types"
	"github.com/onflow/rosetta/log"
)

func main() {
	if len(os.Args) < 4 {
		fmt.Println("Usage: reconciliation <testnet|mainnet> <accounts-file> <flow-account> [<start-height>] [<end-height>]")
		os.Exit(0)
	}
	network := &types.NetworkIdentifier{
		Blockchain: "flow",
		Network:    os.Args[1],
	}
	filename := os.Args[2]
	data, err := os.ReadFile(filepath.Clean(filename))
	if err != nil {
		log.Fatalf("Failed to read %s: %s", filename, err)
	}
	accounts := map[string][]uint64{}
	err = json.Unmarshal(data, &accounts)
	if err != nil {
		log.Fatalf("Failed to JSON-decode %s: %s", filename, err)
	}
	addr := os.Args[3]
	acct := &types.AccountIdentifier{
		Address: addr,
	}
	blocks, ok := accounts[addr[2:]]
	if !ok {
		log.Fatalf("Failed to find account %q in %s", addr, filename)
	}
	ctx := context.Background()
	client := fetcher.New("http://localhost:8080")
	status, xerr := client.NetworkStatus(ctx, network, nil)
	if xerr != nil {
		log.Fatalf("Failed to get network status: %s", xerr)
	}
	options, xerr := client.NetworkOptions(ctx, network, nil)
	if xerr != nil {
		log.Fatalf("Failed to get network options: %s", xerr)
	}
	client.Asserter, err = asserter.NewClientWithResponses(network, status, options, "")
	if err != nil {
		log.Fatalf("Failed to instantiate the asserter: %s", err)
	}
	start := blocks[0]
	inferred := int64(0)
	blockID := &types.PartialBlockIdentifier{
		Index: types.Int64(0),
	}
	if len(os.Args) >= 5 {
		start, err = strconv.ParseUint(os.Args[4], 10, 64)
		if err != nil {
			log.Fatalf("Unable to decode start height %q: %s", os.Args[4], err)
		}
		if len(os.Args) >= 6 {
			end, err := strconv.ParseUint(os.Args[5], 10, 64)
			if err != nil {
				log.Fatalf("Unable to decode end height %q: %s", os.Args[4], err)
			}
			blocks = []uint64{}
			for height := start; height <= end; height++ {
				blocks = append(blocks, height)
			}
		} else {
			count := len(blocks)
			idx := sort.Search(count, func(i int) bool {
				return blocks[i] >= start
			})
			if idx == count {
				log.Fatalf("Unable to find any blocks for start height %d", start)
			}
			blocks = blocks[idx:]
		}
		if len(blocks) == 0 {
			log.Fatalf("Found no blocks to process")
		}
		blockID.Index = types.Int64(int64(blocks[0] - 1))
		_, amounts, _, xerr := client.AccountBalance(ctx, network, acct, blockID, nil)
		if xerr != nil {
			log.Fatalf("Failed to get account balance at height %d: %s", *blockID.Index, xerr)
		}
		inferred, err = strconv.ParseInt(amounts[0].Value, 10, 64)
		if err != nil {
			log.Fatalf("Failed to parse balance amount at height %d: %s", *blockID.Index, err)
		}
	}
	log.Infof("Starting balance at height %d: %d", *blockID.Index, inferred)
	for _, height := range blocks {
		blockID.Index = types.Int64(int64(height))
		block, xerr := client.Block(ctx, network, blockID)
		if xerr != nil {
			log.Fatalf("Failed to get block at height %d: %s", height, xerr)
		}
		for _, txn := range block.Transactions {
			changes := []int64{}
			for _, op := range txn.Operations {
				if op.Account.Address != addr {
					continue
				}
				if op.Amount == nil || op.Amount.Value == "" {
					continue
				}
				amount, err := strconv.ParseInt(op.Amount.Value, 10, 64)
				if err != nil {
					log.Fatalf("Failed to parse operation amount at height %d: %s", height, err)
				}
				inferred += amount
				changes = append(changes, amount)
			}
			log.Infof(
				"Balance changes in txn %s of block %d: %v",
				txn.TransactionIdentifier.Hash, height, changes,
			)
		}
		_, amounts, _, xerr := client.AccountBalance(ctx, network, acct, blockID, nil)
		if xerr != nil {
			log.Fatalf("Failed to get account balance at height %d: %s", height, xerr)
		}
		balance, err := strconv.ParseInt(amounts[0].Value, 10, 64)
		if err != nil {
			log.Fatalf("Failed to parse balance amount at height %d: %s", height, err)
		}
		log.Infof("Inferred account balance at block %d: %d", height, inferred)
		log.Infof(" Rosetta account balance at block %d: %d", height, balance)
		if inferred != balance {
			log.Fatalf("Mismatching account balances")
		}
	}
}
