import FlowManager from 0xFlow

transaction(name: String, address: Address, manager: Address) {
    prepare(payer: AuthAccount) {
        let contractManager = getAccount(manager).capabilities.borrow<&FlowManager.Mapper>()!

        // Create a record in contract database
        contractManager.setAddress(name, address: address)
    }
}