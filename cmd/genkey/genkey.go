// Command genkey generates a new public/private keypair for Flow accounts.
package main

import (
	"fmt"
	"log"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
)

func main() {
	privateKey, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		log.Fatalf("Failed to generate key: %s", err)
	}
	pub := privateKey.PubKey()
	fmt.Printf("Public Key (Flow Format): %x\n", pub.SerializeUncompressed()[1:])
	fmt.Printf("Public Key (Rosetta Format): %x\n", pub.SerializeCompressed())
	fmt.Printf("Private Key: %x\n", privateKey.Serialize())
}
