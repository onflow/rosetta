import FlowManager from 0xFlow

transaction(name: String, address: Address, manager: Address) {
    prepare(payer: AuthAccount) {
        let linkPath = FlowManager.contractManagerPath
        let contractManager = getAccount(manager)
                            .getCapability(linkPath)!
                            .borrow<&FlowManager.Mapper>()!

        // Create a record in contract database
        contractManager.setAddress(name, address: address)
    }
}