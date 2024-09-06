import FlowFees from 0xe5a8b7f23e8b548f

access(all) fun main(inclusionEffort: UFix64, executionEffort: UFix64): UFix64 {
    return FlowFees.computeFees(inclusionEffort: inclusionEffort, executionEffort: executionEffort)
}