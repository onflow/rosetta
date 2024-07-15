import FlowColdStorageProxy from 0xProxy

transaction(sender: Address, receiver: Address, amount: UFix64, nonce: Int64, sig: String) {
    prepare(payer: &Account) {
    }
    execute {
        // Get a reference to the sender's FlowColdStorageProxy.Vault.
        let acct = getAccount(sender)
        let vault = acct.capabilities.borrow<&FlowColdStorageProxy.Vault>(FlowColdStorageProxy.VaultCapabilityPublicPath)!

        // Transfer tokens to the receiver.
        vault.transfer(receiver: receiver, amount: amount, nonce: nonce, sig: sig.decodeHex())
    }
}