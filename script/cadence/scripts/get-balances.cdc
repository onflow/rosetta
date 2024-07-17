import FlowColdStorageProxy from 0xProxy
import FlowToken from 0x0ae53cb6e3f42a79
import FungibleToken from 0xee82856bf20e2aa6

access(all) struct AccountBalances {
    pub let default_balance: UFix64
    pub let is_proxy: Bool
    pub let proxy_balance: UFix64

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