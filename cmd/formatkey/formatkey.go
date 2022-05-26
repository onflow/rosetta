// Command formatkey converts hex-encoded secp256k1 public keys between Flow and
// Rosetta (SEC compressed) formats.
package main

import (
	"encoding/hex"
	"fmt"
	"os"

	"github.com/onflow/rosetta/crypto"
	"github.com/onflow/rosetta/log"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Println("Usage: formatkey [from:flow | from:rosetta] <hex-encoded-key>")
		os.Exit(1)
	}
	key, err := hex.DecodeString(os.Args[2])
	if err != nil {
		log.Errorf(
			"Failed to hex-decode secp256k1 key %q: %s",
			os.Args[2], err,
		)
	}
	switch os.Args[1] {
	case "from:flow":
		key, err = crypto.ConvertFlowPublicKey(key)
	case "from:rosetta":
		key, err = crypto.ConvertRosettaPublicKey(key)
	default:
		log.Fatalf(
			"Unknown convert option %q: needs to be either 'from:flow' or 'from:rosetta'",
			os.Args[1],
		)
	}
	if err != nil {
		log.Fatalf("Failed to convert key %q: %s", os.Args[2], err)
	}
	fmt.Printf("%x\n", key)
}
