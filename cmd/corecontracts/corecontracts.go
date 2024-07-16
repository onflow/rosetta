// Command corecontracts spits out the contract addresses for various Flow
// chains.
package main

import (
	"fmt"

	"github.com/onflow/flow-go/model/flow"
	"github.com/onflow/rosetta/log"
)

func printAddr(chain flow.Chain, name string, idx uint64) {
	addr, err := chain.AddressAtIndex(idx)
	if err != nil {
		log.Fatalf("Failed to get address at index %d for %s: %s", idx, chain, err)
	}
	fmt.Printf("%16s Address: 0x%s\n", name, addr)
}

func main() {
	for _, network := range []flow.ChainID{
		flow.Mainnet,
		flow.Testnet,
		flow.Previewnet,
		flow.Localnet,
		flow.Emulator,
	} {
		chain := network.Chain()
		fmt.Printf("%s:\n\n", network)
		printAddr(chain, "Zero", 0)
		printAddr(chain, "Service", 1) // Same as chain.ServiceAddress()
		printAddr(chain, "Fungible Token", 2)
		printAddr(chain, "Flow Token", 3)
		printAddr(chain, "Flow Fees", 4)
		fmt.Printf("\n")
	}
}
