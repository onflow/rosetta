// Command genkey generates a new public/private keypair for Flow accounts.
package main

import (
	"flag"
	"fmt"
	"log"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
)

func main() {
	// Define a flag for output format
	csvOutput := flag.Bool("csv", false, "Output keys in CSV format")
	flag.Parse()

	// Generate private key
	priv, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		log.Fatalf("Failed to generate key: %s", err)
	}

	// Generate public keys
	pub := priv.PubKey()
	publicFlowKey := fmt.Sprintf("%x", pub.SerializeUncompressed()[1:])
	publicRosettaKey := fmt.Sprintf("%x", pub.SerializeCompressed())
	privateKey := fmt.Sprintf("%x", priv.Serialize())

	// Output based on the flag
	if *csvOutput {
		// Output as a single line in CSV format
		fmt.Printf("%s,%s,%s\n", publicFlowKey, publicRosettaKey, privateKey)
	} else {
		// Human-readable output
		fmt.Printf("Public Key (Flow Format): %s\n", publicFlowKey)
		fmt.Printf("Public Key (Rosetta Format): %s\n", publicRosettaKey)
		fmt.Printf("Private Key: %s\n", privateKey)
	}
}
