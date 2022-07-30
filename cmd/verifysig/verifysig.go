// Command verifysig verifies the signature produced by the given key for a
// message.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"github.com/onflow/rosetta/log"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Println("Usage: verifysig <public-key> <message> <signature>")
		os.Exit(1)
	}
	publicKey, rawMessage, rawSignature := os.Args[1], os.Args[2], os.Args[3]
	raw, err := hex.DecodeString(publicKey)
	if err != nil {
		log.Errorf(
			"Failed to hex-decode secp256k1 key %q: %s",
			publicKey, err,
		)
	}
	pub, err := secp256k1.ParsePubKey(raw)
	if err != nil {
		log.Errorf(
			"Failed to parse secp256k1 key %q: %s",
			publicKey, err,
		)
	}
	raw, err = hex.DecodeString(rawSignature)
	if err != nil {
		log.Errorf(
			"Failed to hex-decode signature %q: %s",
			rawSignature, err,
		)
	}
	sig, err := ecdsa.ParseDERSignature(raw)
	if err != nil {
		log.Errorf(
			"Failed to DER-decode signature %q: %s",
			rawSignature, err,
		)
	}
	hash := sha256.Sum256([]byte(rawMessage))
	ok := sig.Verify(hash[:], pub)
	if ok {
		log.Infof("Valid signature found")
	} else {
		log.Fatalf("Invalid signature found")
	}
}
