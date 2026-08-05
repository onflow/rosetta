import FlowFees from 0x{{.Contracts.FlowFees}}

// Returns the addresses of all accounts that may receive transaction fee
// deposits: the FlowFees contract account itself, plus any child fee accounts
// that FlowFees.deductTransactionFee rotates deposits across (see
// onflow/flow-core-contracts#575, "Enable concurrent fee collection").
access(all) fun main(): [Address] {
    return FlowFees.getFeeReceiverAddresses()
}
