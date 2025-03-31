package script

import (
	"context"
	"github.com/onflow/rosetta/config"
	"github.com/onflow/rosetta/log"
	"testing"
)

// Mock Chain struct (replace with your actual config.Chain definition)
type MockChain struct {
	Network string
	ID      string
}

// TestCompile tests the Compile function
func TestCompileComputeFees(t *testing.T) {
	// Initialize the chain configuration from test.json
	chain := config.Init(context.Background(), "../testnet.json")
	log.Infof("sdffd %s", chain.Contracts.FlowFees)

	// Call the Compile function
	result := Compile("compute_fees", ComputeFees, chain)

	// Expected output after template execution
	expected := "import FlowFees from 0x912d5440f7e3769e\n\naccess(all) fun main(inclusionEffort: UFix64, executionEffort: UFix64): UFix64 {\n    return FlowFees.computeFees(inclusionEffort: inclusionEffort, executionEffort: executionEffort)\n}"

	// Compare result with expected output
	if string(result) != expected {
		t.Errorf("Expected %q but got %q", expected, string(result))
	}
}
