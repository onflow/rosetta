import FlowToken from 0x0ae53cb6e3f42a79
import FungibleToken from 0xee82856bf20e2aa6

pub struct AccountBalances {
    pub let default_balance: UFix64
    pub let is_proxy: Bool
    pub let proxy_balance: UFix64

    init(default_balance: UFix64, is_proxy: Bool, proxy_balance: UFix64) {
        self.default_balance = default_balance
        self.is_proxy = is_proxy
        self.proxy_balance = proxy_balance
    }
}

pub fun main(addr: Address): AccountBalances {
    let acct = getAccount(addr)
    let balanceRef = acct.getCapability(/public/flowTokenBalance)
                         .borrow<&FlowToken.Vault{FungibleToken.Balance}>()!
    return AccountBalances(
        default_balance: balanceRef.balance,
        is_proxy: false,
        proxy_balance: 0.0
    )
}