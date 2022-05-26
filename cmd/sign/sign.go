// Command sign can be used to generate signatures for the given key and
// payload.
package main

import (
	"encoding/hex"
	"fmt"
	"log"
	"os"

	"github.com/ethereum/go-ethereum/crypto/secp256k1"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Println("Usage: sign <hex-encoded-private-key> <hex-encoded-payload>")
		os.Exit(1)
	}
	raw, err := hex.DecodeString(os.Args[1])
	if err != nil {
		log.Fatalf("Could not hex decode private key: %s", err)
	}
	payload, err := hex.DecodeString(os.Args[2])
	if err != nil {
		log.Fatalf("Could not hex decode payload: %s", err)
	}
	sig, err := secp256k1.Sign(payload, raw)
	if err != nil {
		log.Fatalf("Failed to sign payload: %s", err)
	}
	fmt.Printf("%x\n", sig[:64])
}
