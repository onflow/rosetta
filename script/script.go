// Package script defines templates for various Cadence scripts.
package script

import (
	"bytes"
	"text/template"

	"github.com/onflow/rosetta/config"
	"github.com/onflow/rosetta/log"
)

// BasicTransfer defines the template for doing a standard transfer of FLOW from
// one account to another.
//
// Adapted from:
// https://github.com/onflow/flow-core-contracts/blob/master/transactions/flowToken/transfer_tokens.cdc
const BasicTransfer = `import FungibleToken from 0x{{.Contracts.FungibleToken}}
import FlowToken from 0x{{.Contracts.FlowToken}}

transaction(receiver: Address, amount: UFix64) {

    // The Vault resource that holds the tokens that are being transferred.
    let xfer: @FungibleToken.Vault

    prepare(sender: AuthAccount) {
        // Get a reference to the sender's FlowToken.Vault.
        let vault = sender.borrow<&FlowToken.Vault>(from: /storage/flowTokenVault)
            ?? panic("Could not borrow a reference to the sender's vault")

        // Withdraw tokens from the sender's FlowToken.Vault.
        self.xfer <- vault.withdraw(amount: amount)
    }

    execute {
        // Get a reference to the receiver's default FungibleToken.Receiver
        // for FLOW tokens.
        let receiver = getAccount(receiver)
            .getCapability(/public/flowTokenReceiver)
            .borrow<&{FungibleToken.Receiver}>()
            ?? panic("Could not borrow a reference to the receiver's vault")

        // Deposit the withdrawn tokens in the receiver's vault.
        receiver.deposit(from: <-self.xfer)
    }
}
`

// ComputeFees computes the transaction fees.
const ComputeFees = `import FlowFees from 0x{{.Contracts.FlowFees}}

pub fun main(inclusionEffort: UFix64, executionEffort: UFix64): UFix64 {
    return FlowFees.computeFees(inclusionEffort: inclusionEffort, executionEffort: executionEffort)
}
`

// CreateAccount defines the template for creating new Flow accounts.
const CreateAccount = `transaction(publicKeys: [String]) {
    prepare(payer: AuthAccount) {
        for key in publicKeys {
            // Create an account and set the account public key.
            let acct = AuthAccount(payer: payer)
            let publicKey = PublicKey(
                publicKey: key.decodeHex(),
                signatureAlgorithm: SignatureAlgorithm.ECDSA_secp256k1
            )
            acct.keys.add(
                publicKey: publicKey,
                hashAlgorithm: HashAlgorithm.SHA3_256,
                weight: 1000.0
            )
        }
    }
}
`

// CreateProxyAccount defines the template for creating a new Flow account with
// a Proxy Vault.
const CreateProxyAccount = `import FlowColdStorageProxy from 0x{{.Contracts.FlowColdStorageProxy}}

transaction(publicKey: String) {
    prepare(payer: AuthAccount) {
        // Create a new account with a FlowColdStorageProxy Vault.
        FlowColdStorageProxy.setup(payer: payer, publicKey: publicKey.decodeHex())
    }
}
`

// DeployContract defines the template for creating a new Flow account with the
// given public key and deploying the given contract on it.
const DeployContract = `transaction(publicKey: String, contractName: String, contractCode: String) {
    prepare(payer: AuthAccount) {
        let acct = AuthAccount(payer: payer)
        let key = PublicKey(
            publicKey: publicKey.decodeHex(),
            signatureAlgorithm: SignatureAlgorithm.ECDSA_secp256k1
        )
        acct.keys.add(
            publicKey: key,
            hashAlgorithm: HashAlgorithm.SHA3_256,
            weight: 1000.0
        )
        acct.contracts.add(
            name: contractName,
            code: contractCode.decodeHex()
        )
    }
}
`

// GetBalances defines the template for the read-only transaction script that
// returns an account's proxy balance if it is a proxy account.
const GetBalances = `import FlowColdStorageProxy from 0x{{.Contracts.FlowColdStorageProxy}}

pub struct AccountBalances {
    pub let is_proxy: Bool
    pub let proxy_balance: UFix64

    init(is_proxy: Bool, proxy_balance: UFix64) {
        self.is_proxy = is_proxy
        self.proxy_balance = proxy_balance
    }
}

pub fun main(addr: Address): AccountBalances {
    var is_proxy = false
    var proxy_balance = 0.0
    let ref = acct.getCapability(FlowColdStorageProxy.VaultCapabilityPublicPath).borrow<&FlowColdStorageProxy.Vault>()
    if let vault = ref {
        is_proxy = true
        proxy_balance = vault.getBalance()
    }
    return AccountBalances(
        is_proxy: is_proxy,
        proxy_balance: proxy_balance
    )
}
`

// GetProxyNonce defines the template for the read-only transaction script that
// returns a proxy account's sequence number, i.e. the next nonce value for its
// FlowColdStorageProxy Vault.
//
// If the account isn't a proxy account, i.e. doesn't have a
// FlowColdStorageProxy Vault, then it will return -1.
const GetProxyNonce = `import FlowColdStorageProxy from 0x{{.Contracts.FlowColdStorageProxy}}

pub fun main(addr: Address): Int64 {
    let acct = getAccount(addr)
    let ref = acct.getCapability(FlowColdStorageProxy.VaultCapabilityPublicPath).borrow<&FlowColdStorageProxy.Vault>()
    if let vault = ref {
        return vault.lastNonce + 1
    }
    return -1
}
`

// GetProxyPublicKey defines the template for the read-only transaction script
// that returns a proxy account's public key.
//
// If the account isn't a proxy account, i.e. doesn't have a
// FlowColdStorageProxy Vault, then it will return the empty string.
const GetProxyPublicKey = `import FlowColdStorageProxy from 0x{{.Contracts.FlowColdStorageProxy}}

pub fun main(addr: Address): String {
    let acct = getAccount(addr)
    let ref = acct.getCapability(FlowColdStorageProxy.VaultCapabilityPublicPath).borrow<&FlowColdStorageProxy.Vault>()
    if let vault = ref {
        return String.encodeHex(vault.getPublicKey())
    }
    return ""
}
`

// ProxyTransfer defines the template for doing a transfer of FLOW from a proxy
// account.
const ProxyTransfer = `import FlowColdStorageProxy from 0x{{.Contracts.FlowColdStorageProxy}}

transaction(sender: Address, receiver: Address, amount: UFix64, nonce: Int64, sig: String) {
    prepare(payer: AuthAccount) {
    }
    execute {
        // Get a reference to the sender's FlowColdStorageProxy.Vault.
        let acct = getAccount(sender)
        let vault = acct.getCapability(FlowColdStorageProxy.VaultCapabilityPublicPath).borrow<&FlowColdStorageProxy.Vault>()!

        // Transfer tokens to the receiver.
        vault.transfer(receiver: receiver, amount: amount, nonce: nonce, sig: sig.decodeHex())
    }
}
`

// UpdateContract updates the existing contract.
const UpdateContract = `transaction(contractName: String, contractCode: String) {
    prepare(payer: AuthAccount) {
        payer.contracts.update__experimental(
            name: contractName,
            code: contractCode.decodeHex()
        )
    }
}
`

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
