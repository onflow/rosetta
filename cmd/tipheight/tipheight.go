// Command tipheight emits the latest block height on Flow mainnet/testnet.
package main

import (
	"context"

	"github.com/onflow/rosetta/access"
	"github.com/onflow/rosetta/log"
)

func main() {
	ctx := context.Background()
	for _, network := range []struct {
		name string
		addr string
	}{
		{"mainnet", "access.mainnet.nodes.onflow.org:9000"},
		{"testnet", "access.devnet.nodes.onflow.org:9000"},
		{"canary", "access.canary.nodes.onflow.org:9000"},
		{"localnet", "127.0.0.1:3569"},
	} {
		pool := access.New(ctx, []access.NodeConfig{{Address: network.addr}}, nil)
		client := pool.Client()
		latest, err := client.LatestBlockHeader(ctx)
		if err != nil {
			log.Errorf(
				"Failed to fetch latest block on %s: %s",
				network.name, err,
			)
			continue
		}
		log.Infof(
			"Latest block on %s is %x at height %d",
			network.name, latest.Id, latest.Height,
		)
	}
}
