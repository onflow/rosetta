// Command txinfo prints information about a transaction.
package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/onflow/rosetta/access"
	"github.com/onflow/rosetta/log"
)

var networks = map[string]string{
	"mainnet": "access.mainnet.nodes.onflow.org:9000",
	"testnet": "access.devnet.nodes.onflow.org:9000",
	"canary":  "access.canary.nodes.onflow.org:9000",
}

func main() {
	if len(os.Args) != 3 {
		fmt.Println("Usage: txinfo mainnet|testnet|canary <txhash>")
		os.Exit(1)
	}
	addr, ok := networks[os.Args[1]]
	if !ok {
		log.Fatalf("Unsupported network: %q", os.Args[1])
	}
	hash, err := hex.DecodeString(os.Args[2])
	if err != nil {
		log.Fatalf("Failed to hex-decode txhash %q: %s", os.Args[2], err)
	}
	ctx := context.Background()
	pool := access.New(ctx, []access.NodeConfig{{Address: addr}}, nil)
	client := pool.Client()
	tx, err := client.TransactionResultByHash(ctx, hash)
	if err != nil {
		log.Fatalf("Failed to find txinfo: %s", err)
	}
	log.Infof("Transaction %x: %#v", hash, tx)
}
