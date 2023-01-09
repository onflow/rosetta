import FlowToken from 0x0ae53cb6e3f42a79
import FungibleToken from 0xee82856bf20e2aa6
import FlowIDTableStaking from 0x8624b52f9ddcd04a

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
pub contract FlowColdStorageProxy {

    pub let VaultCapabilityPublicPath: PublicPath
    pub let VaultCapabilityStoragePath: StoragePath

    // Created is emitted when a new FlowColdStorageProxy.Vault is set up on an
    // account.
    pub event Created(account: Address, publicKey: String)

    // Deposited is emitted when FLOW tokens are deposited to the
    // FlowColdStorageProxy.Vault.
    pub event Deposited(account: Address, amount: UFix64)

    // Transferred is emitted when a transfer takes place from a
    // FlowColdStorageProxy.Vault.
    pub event Transferred(from: Address?, to: Address, amount: UFix64)

    // Delegated is emitted when a delegation takes place from a
    // FlowColdStorageProxy.Vault.
    pub event Delegated(from: Address?, to: String, amount: UFix64)

    // RewardsDelegated is emitted when delegation rewards are re-delegated.
    pub event RewardsDelegated(from: Address?, to: String, amount: UFix64)

    // RequestedUndelegate is emitted when an undelgation request takes place.
    pub event RequestedUndelegate(from: Address?, to: String, amount: UFix64)

    // WithdrewUndelegated is emitted when previously delegated funds are
	// transfered back to the Vault.
    pub event WithdrewUndelegated(from: Address?, to: String, amount: UFix64)

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
    pub resource Vault: FungibleToken.Receiver {
        pub var lastNonce: Int64
        access(self) let flowVault: @FungibleToken.Vault
        access(self) let publicKey: [UInt8]
		access(self) var nodeDelegator: @FlowIDTableStaking.NodeDelegator

        init(publicKey: [UInt8], stakingNodeID: String) {
            self.flowVault <- FlowToken.createEmptyVault()
			self.nodeDelegator <- FlowIDTableStaking.registerNewDelegator(nodeID: stakingNodeID)
            self.lastNonce = -1
            self.publicKey = publicKey
            // NOTE(tav): We instantiate a PublicKey to make sure it is valid.
            let key = PublicKey(
                publicKey: self.publicKey,
                signatureAlgorithm: SignatureAlgorithm.ECDSA_secp256k1
            )
        }

        // deposit proxies the call to the underlying FlowToken.Vault resource.
        pub fun deposit(from: @FungibleToken.Vault) {
            emit Deposited(account: self.owner!.address, amount: from.balance)
            self.flowVault.deposit(from: <- from)
        }

        // getBalance returns the balance of the underlying FlowToken.Vault
        // resource.
        pub fun getBalance(): UFix64 {
            return self.flowVault.balance
        }

        // getPublicKey returns the raw public key for the Vault.
        pub fun getPublicKey(): [UInt8] {
            return self.publicKey
        }

        // getDelegatorInfo return a struct with info about the
        // delegated balances for the Vault.
        pub fun getDelegatorInfo(): FlowIDTableStaking.DelegatorInfo {
            return FlowIDTableStaking.DelegatorInfo(
                nodeID: self.nodeDelegator.id,
                delegatorID: self.nodeDelegator.nodeID
            )
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
        pub fun transfer(receiver: Address, amount: UFix64, nonce: Int64, sig: [UInt8]) {
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
            let acct = getAccount(receiver)
                .getCapability(/public/flowTokenReceiver)
                .borrow<&{FungibleToken.Receiver}>()
                ?? panic("Unable to borrow a reference to the receiver's default token vault")

            let xfer <- self.flowVault.withdraw(amount: amount)
            acct.deposit(from: <- xfer)

            // Increment the nonce.
            self.lastNonce = self.lastNonce + 1
            emit Transferred(from: self.owner!.address, to: receiver, amount: amount)
        }

		pub fun swapValidator(stakingNodeId: String) {
            let delegatorInfo = self.getDelegatorInfo()
			if delegatorInfo.totalTokensInRecord() > 0.0 {
				panic("All tokens must be withdrawn from NodeDelegator before swapping")
			}

			var swap: @FlowIDTableStaking.NodeDelegator <- FlowIDTableStaking.registerNewDelegator(nodeID: stakingNodeID)
			self.nodeDelegator <-> swap

			destroy swap
		}

        pub fun delegateVaultTokens(amount: UFix64, nonce: Int64, sig: [UInt8]) {
            // Ensure that the nonce follows on from the previous meta
            // transaction.
            if nonce != (self.lastNonce + 1) {
                panic("Invalid meta transaction nonce")
            }

            // Construct the message to sign.
            var data: [UInt8] = "delegate".toBytes()
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

            let xfer <- self.flowVault.withdraw(amount: amount)
            self.nodeDelegator.delegateNewTokens(from: <- xfer)

            // Increment the nonce.
            self.lastNonce = self.lastNonce + 1
            emit Delegated(from: self.owner!.address, to: self.nodeDelegator.nodeID, amount: amount)
        }

        pub fun delegateRewardsTokens(nonce: Int64, sig: [UInt8]) {
            // Ensure that the nonce follows on from the previous meta
            // transaction.
            if nonce != (self.lastNonce + 1) {
                panic("Invalid meta transaction nonce")
            }

            // Construct the message to sign.
            var data: [UInt8] = "delegate-rewards".toBytes()
            data = data.concat(nonce.toBigEndianBytes())

            // Verify the signature.
            let key = PublicKey(
                publicKey: self.publicKey,
                signatureAlgorithm: SignatureAlgorithm.ECDSA_secp256k1
            )
            if !key.verify(signature: sig, signedData: data, domainSeparationTag: "FLOW-V0.0-user", hashAlgorithm: HashAlgorithm.SHA3_256) {
                panic("Invalid meta transaction signature")
            }	

            let delegatorInfo = self.getDelegatorInfo()
            let amount = delegatorInfo.tokensRewarded
            self.nodeDelegator.delegateRewardedTokens(amount: amount)

            // Increment the nonce.
            self.lastNonce = self.lastNonce + 1
            emit RewardsDelegated(from: self.owner!.address, to: self.nodeDelegator.nodeID, amount: amount)
        }

        pub fun requestUndelegateTokens(amount: UFix64, nonce: Int64, sig: [UInt8]) {
            // Ensure that the nonce follows on from the previous meta
            // transaction.
            if nonce != (self.lastNonce + 1) {
                panic("Invalid meta transaction nonce")
            }

            // Construct the message to sign.
            var data: [UInt8] = "undelegate".toBytes()
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

            self.nodeDelegator.requestUnstaking(amount: amount)

            // Increment the nonce.
            self.lastNonce = self.lastNonce + 1
            emit RequestedUndelegate(from: self.owner!.address, to: self.nodeDelegator.nodeID, amount: amount)
        }

        pub fun withdrawUndelegatedTokensToVault(nonce: Int64, sig: [UInt8]) {
            // Ensure that the nonce follows on from the previous meta
            // transaction.
            if nonce != (self.lastNonce + 1) {
                panic("Invalid meta transaction nonce")
            }

            // Construct the message to sign.
            var data: [UInt8] = "delegate-rewards".toBytes()
            data = data.concat(nonce.toBigEndianBytes())

            // Verify the signature.
            let key = PublicKey(
                publicKey: self.publicKey,
                signatureAlgorithm: SignatureAlgorithm.ECDSA_secp256k1
            )
            if !key.verify(signature: sig, signedData: data, domainSeparationTag: "FLOW-V0.0-user", hashAlgorithm: HashAlgorithm.SHA3_256) {
                panic("Invalid meta transaction signature")
            }	

            let delegatorInfo = self.getDelegatorInfo()
            let totalAmount = delegatorInfo.tokensRewarded + delegatorInfo.tokensUnstaked

            if totalAmount == 0.0 {
                panic("No funds available for withdrawal")
            }

            if delegatorInfo.tokensRewarded > 0.0 {
                let xfer = self.nodeDelegator.withdrawRewardedTokens(amount: delegatorInfo.tokensRewarded)
                self.flowVault.deposit(from: <- xfer)
            }

            if delegatorInfo.tokensUnstaked > 0.0 {
                let xfer = self.nodeDelegator.withdrawUnstakedTokens(amount: delegatorInfo.tokensUnstaked)
                self.flowVault.deposit(from: <- xfer)
            }
						
            // Increment the nonce.
            self.lastNonce = self.lastNonce + 1
            emit WithdrewUndelegated(from: self.owner!.address, to: self.nodeDelegator.nodeID, amount: totalAmount)
        }

        destroy() {
            if self.flowVault.balance > 0.0 {
                panic("Cannot destroy a Vault without transferring the remaining balance")
            }

            if self.getDelegatorInfo().totalTokensInRecord() > 0.0 {
                panic("Cannot destroy a Vault with delegated funds remaining")
            }

			destroy self.nodeDelegator
            destroy self.flowVault
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
    pub fun setup(payer: AuthAccount, publicKey: [UInt8], stakingNodeID: String): Address {
        let acct = AuthAccount(payer: payer)
        acct.save(<- create Vault(publicKey: publicKey, stakingNodeID: stakingNodeID), to: self.VaultCapabilityStoragePath)
        acct.unlink(/public/flowTokenReceiver)
        acct.link<&{FungibleToken.Receiver}>(/public/defaultFlowTokenReceiver, target: /storage/flowTokenVault)!
		acct.link<&{FungibleToken.Receiver}>(/public/flowTokenReceiver, target: self.VaultCapabilityStoragePath)!
		acct.link<&Vault>(self.VaultCapabilityPublicPath, target: self.VaultCapabilityStoragePath)!
        emit Created(account: acct.address, publicKey: String.encodeHex(publicKey))
        return acct.address
    }

    init() {
        self.VaultCapabilityPublicPath = /public/flowColdStorageProxyVault
        self.VaultCapabilityStoragePath = /storage/flowColdStorageProxyVault
    }
}
