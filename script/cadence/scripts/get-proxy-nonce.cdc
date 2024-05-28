import FlowColdStorageProxy from 0xProxy

access(all) fun main(addr: Address): Int64 {
    let acct = getAccount(addr)
    let ref = acct.getCapability(FlowColdStorageProxy.VaultCapabilityPublicPath).borrow<&FlowColdStorageProxy.Vault>()
    if let vault = ref {
        return vault.lastNonce + 1
    }
    return -1
}