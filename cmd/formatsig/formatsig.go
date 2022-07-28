// Command formatsig converts hex-encoded ECDSA signatures between Flow and
// DER-encoded formats.
package main

import (
	"fmt"
	"os"

	"github.com/onflow/rosetta/crypto"
	"github.com/onflow/rosetta/log"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Println("Usage: formatsig [from:flow | from:der] <hex-encoded-signature>")
		os.Exit(1)
	}
	var err error
	sig := os.Args[2]
	switch os.Args[1] {
	case "from:der":
		sig, err = crypto.ConvertDERSignature(sig)
	case "from:flow":
		sig, err = crypto.ConvertFlowSignature(sig)
	default:
		log.Fatalf(
			"Unknown convert option %q: needs to be either 'from:flow' or 'from:der'",
			os.Args[1],
		)
	}
	if err != nil {
		log.Fatalf("Failed to convert signature %q: %s", os.Args[2], err)
	}
	fmt.Printf("%s\n", sig)
}
