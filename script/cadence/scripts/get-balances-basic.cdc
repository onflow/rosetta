import "FlowToken"
import "FungibleToken"

access(all) struct AccountBalances {
    access(all) let default_balance: UFix64
    access(all) let is_proxy: Bool
    access(all) let proxy_balance: UFix64

    init(default_balance: UFix64, is_proxy: Bool, proxy_balance: UFix64) {
        self.default_balance = default_balance
        self.is_proxy = is_proxy
        self.proxy_balance = proxy_balance
    }
}

access(all) fun main(addr: Address): AccountBalances {
    let acct = getAccount(addr)
    var balance = 0.0
    if let balanceRef = acct.capabilities.borrow<&{FungibleToken.Balance}>(/public/flowTokenBalance) {
        balance = balanceRef.balance
    }
    return AccountBalances(
        default_balance: balance,
        is_proxy: false,
        proxy_balance: 0.0
    )
}