import FlowColdStorageProxy from 0xProxy
import "FlowToken"
import "FungibleToken"

transaction(sender: Address, receiver: Address, amount: UFix64) {

    // The Vault resource that holds the tokens that are being transferred.
    let xfer: @FungibleToken.Vault

    prepare(sender: auth(BorrowValue) &Account) {
        // Get a reference to the sender's FlowToken.Vault.
        let vault = sender.storage.borrow<&FlowToken.Vault>(from: /storage/flowTokenVault)
            ?? panic("Could not borrow a reference to the sender's vault")

        // Withdraw tokens from the sender's FlowToken.Vault.
        self.xfer <- vault.withdraw(amount: amount)
    }

    execute {
        // Get a reference to the receiver's default FungibleToken.Receiver
        // for FLOW tokens.
        let receiver = getAccount(receiver).capabilities.borrow<&{FungibleToken.Receiver}>()
            ?? panic("Could not borrow a reference to the receiver's vault")

        // Deposit the withdrawn tokens in the receiver's vault.
        receiver.deposit(from: <-self.xfer)
    }
}