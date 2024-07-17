import FlowToken from 0x0ae53cb6e3f42a79
import FungibleToken from 0xee82856bf20e2aa6
import Burner from 0xf8d6e0586b0a20c7

// FlowColdStorageProxy provides support for the transfer of FLOW tokens within
// cold storage transactions.
//
// Since cold storage transactions are signed offline, the time it takes for the
// transaction construction could potentially exceed Flow's expiry window. We
// work around this constraint by signing a meta transaction for transfers,
// which can then be submitted by the owner account.
//
// Combined with an immutable owner account, i.e. an account without any
// registered keys, this can also be used to enable a more "locked down" account
// for doing sends/receives of FLOW tokens.
access(all) contract FlowColdStorageProxy {

    access(all) let VaultCapabilityPublicPath: PublicPath
    access(all) let VaultCapabilityStoragePath: StoragePath

    // Created is emitted when a new FlowColdStorageProxy.Vault is set up on an
    // account.
    access(all) event Created(account: Address, publicKey: String)

    // Deposited is emitted when FLOW tokens are deposited to the
    // FlowColdStorageProxy.Vault.
    access(all) event Deposited(account: Address, amount: UFix64)

    // Transferred is emitted when a transfer takes place from a
    // FlowColdStorageProxy.Vault.
    access(all) event Transferred(from: Address?, to: Address, amount: UFix64)

    // Vault implements FungibleToken.Receiver and some additional methods for
    // making transfers and getting the balance.
    //
    // It wraps a FlowToken.Vault resource so that its funds cannot be
    // transferred elsewhere without having a meta transaction signed by the
    // associated public key.
    //
    // To ensure that an owner account can't move the resource, and thus make
    // funds inaccessible, the account should be made immutable after the
    // FlowColdStorageProxy.setup call by ensuring that no keys are registered
    // on the account itself.
    access(all) resource Vault: FungibleToken.Receiver, Burner.Burnable {
        access(all) var lastNonce: Int64
        access(self) let flowVault: @{FungibleToken.Vault}
        access(self) let publicKey: [UInt8]

        init(publicKey: [UInt8]) {
            self.flowVault <- FlowToken.createEmptyVault(vaultType: Type<@FlowToken.Vault>())
            self.lastNonce = -1
            self.publicKey = publicKey
            // NOTE(tav): We instantiate a PublicKey to make sure it is valid.
            let key = PublicKey(
                publicKey: self.publicKey,
                signatureAlgorithm: SignatureAlgorithm.ECDSA_secp256k1
            )
        }

        // deposit proxies the call to the underlying FlowToken.Vault resource.
        access(all) fun deposit(from: @{FungibleToken.Vault}) {
            emit Deposited(account: self.owner!.address, amount: from.balance)
            self.flowVault.deposit(from: <- from)
        }

        // getBalance returns the balance of the underlying FlowToken.Vault
        // resource.
        access(all) view fun getBalance(): UFix64 {
            return self.flowVault.balance
        }

        // getPublicKey returns the raw public key for the Vault.
        access(all) view fun getPublicKey(): [UInt8] {
            return self.publicKey
        }

        access(all) view fun getSupportedVaultTypes(): {Type: Bool} {
            return {self.flowVault.getType(): true}
        }

        access(all) view fun isSupportedVaultType(type: Type): Bool {
            return self.getSupportedVaultTypes()[type] ?? false
        }

        // LEAVE THIS COMMENT AS IS. IT IS USED BY THE AUTOMATED TESTS.

        // transfer acts as a meta transaction to transfer FLOW from the
        // underlying FlowToken.Vault to a receiver.
        //
        // While during execution, the transaction parameters are passed in as
        // Cadence values, for signing purposes, the raw data is first encoded
        // into the following binary layout:
        //
        //   <domain-tag><receiver-address><uint64-amount><uint64-nonce>
        //
        // Where all uint64 values are encoded using big endian format, and the
        // domain separation tag is 32 bytes long and made up of the string
        // "FLOW-V0.0-user" followed by a padding of null bytes.
        //
        // The resulting data is then hashed with SHA3-256 before being signed.
        access(all) fun transfer(receiver: Address, amount: UFix64, nonce: Int64, sig: [UInt8]) {
            // Ensure that the nonce follows on from the previous meta
            // transaction.
            if nonce != (self.lastNonce + 1) {
                panic("Invalid meta transaction nonce")
            }

            // Construct the message to sign.
            var data: [UInt8] = receiver.toBytes()
            data = data.concat(amount.toBigEndianBytes())
            data = data.concat(nonce.toBigEndianBytes())

            // Verify the signature.
            let key = PublicKey(
                publicKey: self.publicKey,
                signatureAlgorithm: SignatureAlgorithm.ECDSA_secp256k1
            )
            if !key.verify(signature: sig, signedData: data, domainSeparationTag: "FLOW-V0.0-user", hashAlgorithm: HashAlgorithm.SHA3_256) {
                panic("Invalid meta transaction signature")
            }

            // Do the actual transfer.
            let acct = getAccount(receiver).capabilities.borrow<&{FungibleToken.Receiver}>(/public/flowTokenReceiver)
                ?? panic("Unable to borrow a reference to the receiver's default token vault")

            let xfer <- self.flowVault.withdraw(amount: amount)
            acct.deposit(from: <- xfer)

            // Increment the nonce.
            self.lastNonce = self.lastNonce + 1
            emit Transferred(from: self.owner!.address, to: receiver, amount: amount)
        }

        // Called when a fungible token is burned via the `Burner.burn()` method
        access(contract) fun burnCallback() {
            if self.flowVault.balance > 0.0 {
                panic("Cannot destroy a Vault without transferring the remaining balance")
            }
        }
    }

    // setup creates a new FlowColdStorageProxy.Vault with the given public key,
    // and stores it within /storage/proxyVault on the given account.
    //
    // This function also moves the default /public/flowTokenReceiver to
    // /public/defaultFlowTokenReceiver, and replaces /public/flowTokenReceiver
    // with the FlowColdStorageProxy.Vault.
    //
    // And, finally, the FlowColdStorageProxy.Vault itself is made directly
    // accessible via /public/flowColdStorageProxyVault.
    access(all) fun setup(payer: auth(BorrowValue) &Account, publicKey: [UInt8]): Address {
        let acct = Account(payer: payer)
        acct.storage.save(<- create Vault(publicKey: publicKey), to: self.VaultCapabilityStoragePath)

        acct.capabilities.unpublish(/public/flowTokenReceiver)
        let localVaultCap = acct.capabilities.storage.issue<&{FungibleToken.Receiver}>(/storage/flowTokenVault)
        acct.capabilities.publish(localVaultCap, at: /public/defaultFlowTokenReceiver)

        let ftReceiverCap = acct.capabilities.storage.issue<&{FungibleToken.Receiver}>(self.VaultCapabilityStoragePath)
        acct.capabilities.publish(ftReceiverCap, at: /public/flowTokenReceiver)

        let vaultReceiverCap = acct.capabilities.storage.issue<&Vault>(self.VaultCapabilityStoragePath)
        acct.capabilities.publish(vaultReceiverCap, at: self.VaultCapabilityPublicPath)

        emit Created(account: acct.address, publicKey: String.encodeHex(publicKey))
        return acct.address
    }

    init() {
        self.VaultCapabilityPublicPath = /public/flowColdStorageProxyVault
        self.VaultCapabilityStoragePath = /storage/flowColdStorageProxyVault
    }
}
