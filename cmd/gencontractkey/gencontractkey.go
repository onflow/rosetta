// Command gencontractkey generates a new key with signature proof.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"github.com/onflow/rosetta/crypto"
	"github.com/onflow/rosetta/log"
)

const (
	msg = "some message to sign"
)

func main() {
	key, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		log.Fatalf("Failed to generate key: %s", err)
	}
	hash := sha256.Sum256([]byte(msg))
	sig := ecdsa.Sign(key, hash[:])
	sigHex := hex.EncodeToString(sig.Serialize())
	sigFlow, err := crypto.ConvertDERSignature(sigHex)
	if err != nil {
		log.Fatalf("Failed to convert signature: %s", err)
	}
	fmt.Printf("Public Key:        %x\n", key.PubKey().SerializeCompressed())
	fmt.Printf("Public Key (Flow): %x\n", key.PubKey().SerializeUncompressed()[1:])
	fmt.Printf("Private Key:       %x\n", key.Serialize())
	fmt.Printf("Message:           %q\n", msg)
	fmt.Printf("Signature:         %s\n", sigHex)
	fmt.Printf("Signature (Flow):  %s\n", sigFlow)
}
