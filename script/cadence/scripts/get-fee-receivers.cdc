// Returns the addresses of all accounts that may receive transaction fee
// deposits: the FlowFees contract account itself, plus any child fee accounts
// that FlowFees.collectFeesOnChildAccounts rotates deposits across (see
// onflow/flow-core-contracts#575, "Enable concurrent fee collection").
//
// The FlowFees contract exposes no getter for these addresses, so this script
// reads the capability list the contract keeps at /storage/ChildFeeAccounts.
// The borrowed type must spell the capability's entitlements exactly as the
// contract issues them; if a contract upgrade changes them, the borrow
// returns nil and this script degrades to just the FlowFees account.
access(all) fun main(): [Address] {
    let acct = getAuthAccount<auth(Storage) &Account>(0x{{.Contracts.FlowFees}})
    let addresses: [Address] = [0x{{.Contracts.FlowFees}}]
    if let childFeeAccounts = acct.storage.borrow<&[Capability<auth(Storage, Contracts, Keys, Inbox, Capabilities) &Account>]>(from: /storage/ChildFeeAccounts) {
        for cap in childFeeAccounts {
            addresses.append(cap.address)
        }
    }
    return addresses
}
