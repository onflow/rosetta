import FlowColdStorageProxy from 0xProxy
import FlowManager from 0xFlow

transaction(name: String, publicKey: String, manager: Address) {
    prepare(payer: auth(BorrowValue) &Account) {
        // Create a new account with a FlowColdStorageProxy Vault.
        let address = FlowColdStorageProxy.setup(payer: payer, publicKey: publicKey.decodeHex())

        let accountManager = getAccount(manager).capabilities.borrow<&FlowManager.Mapper>()!

        // Create a record in account database
        accountManager.setAddress(name, address: address)
    }
}