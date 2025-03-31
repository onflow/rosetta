import FlowFees from 0x{{.Contracts.FlowFees}}

access(all) fun main(inclusionEffort: UFix64, executionEffort: UFix64): UFix64 {
    return FlowFees.computeFees(inclusionEffort: inclusionEffort, executionEffort: executionEffort)
}