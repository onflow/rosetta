import FlowColdStorageProxy from 0xProxy

access(all) fun main(addr: Address): String {
    let acct = getAccount(addr)
    let ref = acct.getCapability(FlowColdStorageProxy.VaultCapabilityPublicPath).borrow<&FlowColdStorageProxy.Vault>()
    if let vault = ref {
        return vault.newMethod()
    }
    return ""
}