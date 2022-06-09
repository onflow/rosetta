// Command genkey generates a new public/private keypair for Flow accounts.
package main

import (
	"fmt"
	"log"

	"github.com/btcsuite/btcd/btcec/v2"
)

func main() {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		log.Fatalf("Failed to generate key: %s", err)
	}
	pub := priv.PubKey()
	fmt.Printf("Public Key (Flow Format): %x\n", pub.SerializeUncompressed()[1:])
	fmt.Printf("Public Key (Rosetta Format): %x\n", pub.SerializeCompressed())
	fmt.Printf("Private Key: %x\n", priv.Serialize())
}
