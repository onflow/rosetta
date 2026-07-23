package script

import (
	"context"
	"strings"
	"testing"

	"github.com/onflow/rosetta/config"
)

// TestCompile tests the Compile function
func TestCompileComputeFees(t *testing.T) {
	// Initialize the chain configuration from testnet.json
	chain := config.Init(context.Background(), "../testnet.json")

	// Call the Compile function
	result := Compile("compute_fees", ComputeFees, chain)

	// Expected output after template execution
	expected := "import FlowFees from 0x912d5440f7e3769e\n\naccess(all) fun main(inclusionEffort: UFix64, executionEffort: UFix64): UFix64 {\n    return FlowFees.computeFees(inclusionEffort: inclusionEffort, executionEffort: executionEffort)\n}"

	// Compare result with expected output
	if string(result) != expected {
		t.Errorf("Expected %q but got %q", expected, string(result))
	}
}

// TestCompileGetFeeReceivers tests that the FlowFees address is rendered into
// the get-fee-receivers script.
//
// NOTE: config.Init cannot be called a second time within the same test
// binary (it locks the Badger cache database), so the chain is constructed
// directly.
func TestCompileGetFeeReceivers(t *testing.T) {
	chain := &config.Chain{Contracts: &config.Contracts{FlowFees: "912d5440f7e3769e"}}

	result := string(Compile("get_fee_receivers", GetFeeReceivers, chain))

	for _, expected := range []string{
		"getAuthAccount<auth(Storage) &Account>(0x912d5440f7e3769e)",
		"let addresses: [Address] = [0x912d5440f7e3769e]",
		"from: /storage/ChildFeeAccounts",
	} {
		if !strings.Contains(result, expected) {
			t.Errorf("Expected compiled script to contain %q:\n%s", expected, result)
		}
	}
}
