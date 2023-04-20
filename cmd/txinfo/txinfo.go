// Command txinfo prints information about a transaction.
package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	flowaccess "github.com/onflow/flow/protobuf/go/flow/access"
	"github.com/onflow/flow/protobuf/go/flow/entities"
	"github.com/onflow/rosetta/access"
	"github.com/onflow/rosetta/log"
)

var networks = map[string]string{
	"mainnet":  "access.mainnet.nodes.onflow.org:9000",
	"testnet":  "access.devnet.nodes.onflow.org:9000",
	"canary":   "access.canary.nodes.onflow.org:9000",
	"localnet": "127.0.0.1:3569",
}

func formatTransaction(txn *entities.Transaction) string {
	buf := &strings.Builder{}
	fmt.Fprintf(buf, "\n\n# Script\n\n")
	buf.Write(bytes.TrimSpace(txn.Script))
	fmt.Fprintf(buf, "\n\n# Arguments\n")
	for idx, arg := range txn.Arguments {
		fmt.Fprintf(buf, "\n> Arg %d\n\n", idx)
		buf.Write(arg)
	}
	fmt.Fprintf(buf, "\n# Authorizers: [")
	for idx, acct := range txn.Authorizers {
		if idx != 0 {
			fmt.Fprintf(buf, ", ")
		}
		fmt.Fprintf(buf, "0x%x", acct)
	}
	fmt.Fprintf(buf, "]\n")
	fmt.Fprintf(buf, "\n# Gas Limit: %d\n", txn.GasLimit)
	fmt.Fprintf(buf, "\n# Payer: 0x%x\n", txn.Payer)
	fmt.Fprintf(buf, "\n# Proposal Key:\n")
	fmt.Fprintf(buf, "\n    Address: 0x%x", txn.ProposalKey.Address)
	fmt.Fprintf(buf, "\n    Key ID: %d", txn.ProposalKey.KeyId)
	fmt.Fprintf(buf, "\n    Sequence Number: %d\n", txn.ProposalKey.SequenceNumber)
	fmt.Fprintf(buf, "\n# Reference Block Hash: %x\n", txn.ReferenceBlockId)
	return buf.String()
}

func formatTransactionResult(res *flowaccess.TransactionResultResponse) string {
	buf := &strings.Builder{}
	fmt.Fprintf(buf, "\n\n# Block Height: %d\n", res.BlockHeight)
	fmt.Fprintf(buf, "\n# Block ID: %x\n", res.BlockId)
	fmt.Fprintf(buf, "\n# Error Message: %q\n", res.ErrorMessage)
	fmt.Fprintf(buf, "\n# Events\n")
	for idx, evt := range res.Events {
		fmt.Fprintf(buf, "\n> %q (index %d)\n", evt.Type, idx)
		dst := &bytes.Buffer{}
		err := json.Indent(dst, evt.Payload, "", "  ")
		if err != nil {
			log.Fatalf("Failed to JSON-indent event payload: %s", err)
		}
		fmt.Fprintf(buf, "\n%s\n", bytes.TrimSpace(dst.Bytes()))
	}
	fmt.Fprintf(buf, "\n# Status: %d\n", res.Status)
	fmt.Fprintf(buf, "\n# Status Code: %d\n", res.StatusCode)
	return buf.String()
}

func main() {
	if len(os.Args) != 3 {
		fmt.Println("Usage: txinfo mainnet|testnet|canary <txhash>")
		os.Exit(1)
	}
	addr, ok := networks[os.Args[1]]
	if !ok {
		log.Fatalf("Unsupported network: %q", os.Args[1])
	}
	hash, err := hex.DecodeString(os.Args[2])
	if err != nil {
		log.Fatalf("Failed to hex-decode txhash %q: %s", os.Args[2], err)
	}
	ctx := context.Background()
	pool := access.New(ctx, []access.NodeConfig{{Address: addr}}, nil)
	client := pool.Client()
	txn, err := client.Transaction(ctx, hash)
	if err != nil {
		log.Fatalf("Failed to fetch transaction: %s", err)
	}
	log.Infof("Transaction %x:%s", hash, formatTransaction(txn))
	result, err := client.TransactionResultByHash(ctx, hash)
	if err != nil {
		log.Fatalf("Failed to fetch transaction result: %s", err)
	}
	log.Infof("Transaction result for %x:%s", hash, formatTransactionResult(result))
}
