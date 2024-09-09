// Package script defines templates for various Cadence scripts.
package script

import (
	"bytes"
	_ "embed"
	"text/template"

	"github.com/onflow/rosetta/config"
	"github.com/onflow/rosetta/log"
)

// BasicTransfer defines the template for doing a standard transfer of FLOW from
// one account to another.
//
// Adapted from:
// https://github.com/onflow/flow-core-contracts/blob/master/transactions/flowToken/transfer_tokens.cdc
// Confirmed in use: https://www.flowdiver.io/tx/5316f7b228370d2571a3f2ec5b060a142d3261d8e05b80010c204915843d69e7?tab=script
//
//go:embed cadence/transactions/basic-transfer.cdc
var BasicTransfer string

// ComputeFees computes the transaction fees.
//
//go:embed cadence/scripts/compute-fees.cdc
var ComputeFees string

// CreateAccount defines the template for creating new Flow accounts.
// Confirmed in use: https://www.flowdiver.io/tx/ff0a8d816fe4f73edee665454f26b5fc06f5a39758cb90c313a9c3372f45f6c7?tab=script
//
//go:embed cadence/transactions/create-account.cdc
var CreateAccount string

// CreateProxyAccount defines the template for creating a new Flow account with
// a FlowColdStorageProxy Vault.
//
//go:embed cadence/transactions/create-proxy-account.cdc
var CreateProxyAccount string

// GetBalances defines the template for the read-only transaction script that
// returns an account's balances.
//
// The returned balances include the value of the account's default FLOW vault,
// Jul 2024: The introduction of FlowColdStorageProxy contract was originally intended to be for the
// Coinbase Institution investment feature set. However, this feature was never completed or released to MN
// The current version of Rosetta assumes that all CB accounts contain the contract, which is not the case for
// any account on MN.
// As a result if these scripts/transactions include the import they will error on execution in MN
//
//go:embed cadence/scripts/get-balances.cdc
var GetBalances string

// GetBalancesBasic defines the template for the read-only transaction script
// that returns the balance of an account's default FLOW vault.
//
//go:embed cadence/scripts/get-balances-basic.cdc
var GetBalancesBasic string

// GetProxyNonce defines the template for the read-only transaction script that
// returns a proxy account's sequence number, i.e. the next nonce value for its
// FlowColdStorageProxy Vault.
//
// If the account isn't a proxy account, i.e. doesn't have a
// FlowColdStorageProxy Vault, then it will return -1.
//
//go:embed cadence/scripts/get-proxy-nonce.cdc
var GetProxyNonce string

// GetProxyPublicKey defines the template for the read-only transaction script
// that returns a proxy account's public key.
//
// If the account isn't a proxy account, i.e. doesn't have a
// FlowColdStorageProxy Vault, then it will return the empty string.
//
//go:embed cadence/scripts/get-proxy-public-key.cdc
var GetProxyPublicKey string

// ProxyTransfer defines the template for doing a transfer of FLOW from a proxy
// account.
//
//go:embed cadence/transactions/proxy-transfer.cdc
var ProxyTransfer string

// SetContract deploys/updates a contract on an account, while also updating the
// account's signing key.
//
//go:embed cadence/transactions/proxy-contract-update.cdc
var SetContract string

// Compile compiles the given script for a particular Flow chain.
func Compile(name string, src string, chain *config.Chain) []byte {
	tmpl, err := template.New(name).Parse(src)
	if err != nil {
		log.Fatalf("Failed to parse the %s script: %s", name, err)
	}
	buf := &bytes.Buffer{}
	if err := tmpl.Execute(buf, chain); err != nil {
		log.Fatalf("Failed to compile the %s script: %s", name, err)
	}
	return buf.Bytes()
}
