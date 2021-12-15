// Package fees defines constants for calculating Flow transaction fees.
package fees

// NOTE(siva): These constants need to be updated in line with changes in Flow.
const (
	CreateAccountEffort      = 70
	CreateProxyAccountEffort = 110
	DeployContractEffort     = 550
	FlowTransferEffort       = 30
	InclusionEffort          = 100000000
	MinimumAccountBalance    = 100000
	ProxyTransferEffort      = 80
	UpdateContractEffort     = 480
)
