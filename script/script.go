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
// Confirmed in use: https://www.flowdiver.io/tx/5316f7b228370d2571a3f2ec5b060a142d3261d8e05b80010c204915843d69e7?tab=script
const BasicTransfer = `import FlowToken from 0x{{.Contracts.FlowToken}}
import FungibleToken from 0x{{.Contracts.FungibleToken}}

transaction(receiver: Address, amount: UFix64) {

    // The Vault resource that holds the tokens that are being transferred.
    let xfer:  @{FungibleToken.Vault}

    prepare(sender: auth(BorrowValue) &Account) {
        // Get a reference to the sender's FlowToken.Vault.
        let vault = sender.storage.borrow<auth(FungibleToken.Withdraw) &FlowToken.Vault>(from: /storage/flowTokenVault)
            ?? panic("Could not borrow a reference to the sender's vault")

        // Withdraw tokens from the sender's FlowToken.Vault.
        self.xfer <- vault.withdraw(amount: amount)
    }

    execute {
        // Get a reference to the receiver's default FungibleToken.Receiver
        // for FLOW tokens.
        let receiver = getAccount(receiver)
           .capabilities.borrow<&{FungibleToken.Receiver}>(/public/flowTokenReceiver)
            ?? panic("Could not borrow a reference to the receiver's vault")

        // Deposit the withdrawn tokens in the receiver's vault.
        receiver.deposit(from: <-self.xfer)
    }
}
`

// ComputeFees computes the transaction fees.
const ComputeFees = `import FlowFees from 0x{{.Contracts.FlowFees}}

access(all) fun main(inclusionEffort: UFix64, executionEffort: UFix64): UFix64 {
    return FlowFees.computeFees(inclusionEffort: inclusionEffort, executionEffort: executionEffort)
}
`

// CreateAccount defines the template for creating new Flow accounts.
// Confirmed in use: https://www.flowdiver.io/tx/ff0a8d816fe4f73edee665454f26b5fc06f5a39758cb90c313a9c3372f45f6c7?tab=script
const CreateAccount = `transaction(publicKeys: [String]) {
    prepare(payer: auth(AddKey, BorrowValue) &Account) {
        for key in publicKeys {
            // Create an account and set the account public key.
            let acct = Account(payer: payer)
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
// a FlowColdStorageProxy Vault.
const CreateProxyAccount = `import FlowColdStorageProxy from 0x{{.Contracts.FlowColdStorageProxy}}

transaction(publicKey: String) {
    prepare(payer: auth(BorrowValue) &Account) {
        // Create a new account with a FlowColdStorageProxy Vault.
        FlowColdStorageProxy.setup(payer: payer, publicKey: publicKey.decodeHex())
    }
}
`

// GetBalances defines the template for the read-only transaction script that
// returns an account's balances.
//
// The returned balances include the value of the account's default FLOW vault,
// as well as the optional FlowColdStorageProxy vault.
const GetBalances = `import FlowColdStorageProxy from 0x{{.Contracts.FlowColdStorageProxy}}
import FlowToken from 0x{{.Contracts.FlowToken}}
import FungibleToken from 0x{{.Contracts.FungibleToken}}

access(all) struct AccountBalances {
    access(all) let default_balance: UFix64
    access(all) let is_proxy: Bool
    access(all) let proxy_balance: UFix64

    init(default_balance: UFix64, is_proxy: Bool, proxy_balance: UFix64) {
        self.default_balance = default_balance
        self.is_proxy = is_proxy
        self.proxy_balance = proxy_balance
    }
}

access(all) fun main(addr: Address): AccountBalances {
    let acct = getAccount(addr)
    let balanceRef = acct.capabilities.borrow<&FlowToken.Vault}>(/public/flowTokenBalance)
    var is_proxy = false
    var proxy_balance = 0.0
    let ref = acct.capabilities.borrow<&{FlowColdStorageProxy.Vault}>(FlowColdStorageProxy.VaultCapabilityPublicPath)
    if let vault = ref {
        is_proxy = true
        proxy_balance = vault.getBalance()
    }
    return AccountBalances(
        default_balance: balanceRef.balance,
        is_proxy: is_proxy,
        proxy_balance: proxy_balance
    )
}
`

// GetBalancesBasic defines the template for the read-only transaction script
// that returns the balance of an account's default FLOW vault.
const GetBalancesBasic = `import FlowToken from 0x{{.Contracts.FlowToken}}
import FungibleToken from 0x{{.Contracts.FungibleToken}}

access(all) struct AccountBalances {
    access(all) let default_balance: UFix64
    access(all) let is_proxy: Bool
    access(all) let proxy_balance: UFix64

    init(default_balance: UFix64, is_proxy: Bool, proxy_balance: UFix64) {
        self.default_balance = default_balance
        self.is_proxy = is_proxy
        self.proxy_balance = proxy_balance
    }
}

access(all) fun main(addr: Address): AccountBalances {
	let acct = getAccount(addr)
    var balance = 0.0
    if let balanceRef = acct.capabilities.borrow<&{FungibleToken.Balance}>(/public/flowTokenBalance) {
        balance = balanceRef.balance
    }
    return AccountBalances(
        default_balance: balance,
        is_proxy: false,
        proxy_balance: 0.0
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

access(all) fun main(addr: Address): Int64 {
    let acct = getAccount(addr)
    let ref = acct.capabilities.borrow<&FlowColdStorageProxy.Vault>(FlowColdStorageProxy.VaultCapabilityPublicPath)
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
    let ref = acct.capabilities.borrow<&FlowColdStorageProxy.Vault>(FlowColdStorageProxy.VaultCapabilityPublicPath)
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
	prepare(payer: auth(BorrowValue) &Account) {
    }
    execute {
        // Get a reference to the sender's FlowColdStorageProxy.Vault.
        let acct = getAccount(sender)
        let vault = acct.capabilities.borrow<&FlowColdStorageProxy.Vault>(FlowColdStorageProxy.VaultCapabilityPublicPath)!

        // Transfer tokens to the receiver.
        vault.transfer(receiver: receiver, amount: amount, nonce: nonce, sig: sig.decodeHex())
    }
}
`

// SetContract deploys/updates a contract on an account, while also updating the
// account's signing key.
const SetContract = `transaction(update: Bool, contractName: String, contractCode: String, prevKeyIndex: Int, newKey: String, keyMessage: String, keySignature: String, keyMetadata: String) {
    prepare(payer: auth(AddKey) AuthAccount) {
        let key = PublicKey(
            publicKey: newKey.decodeHex(),
            signatureAlgorithm: SignatureAlgorithm.ECDSA_secp256k1
        )
        let verified = key.verify(
            signature: keySignature.decodeHex(),
            signedData: keyMessage.utf8,
            domainSeparationTag: "",
            hashAlgorithm: HashAlgorithm.SHA2_256
        )
        if !verified {
            panic("Key cannot be verified")
        }
        let prevKey = payer.keys.get(keyIndex: prevKeyIndex)
        if prevKey == nil {
            panic("Invalid prevKeyIndex, didn't find matching key")
        }
        let nextKey = payer.keys.get(keyIndex: prevKeyIndex + 1)
        if nextKey != nil {
            panic("Invalid prevKeyIndex, found key at next key index")
        }
        if update {
            payer.contracts.update__experimental(
                name: contractName,
                code: contractCode.decodeHex()
            )
        } else {
            payer.contracts.add(
                name: contractName,
                code: contractCode.decodeHex()
            )
        }
        payer.keys.add(
            publicKey: key,
            hashAlgorithm: HashAlgorithm.SHA3_256,
            weight: 1000.0
        )
        payer.keys.revoke(keyIndex: prevKeyIndex)
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
