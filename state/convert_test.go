package state

import (
	"context"
	"testing"

	"github.com/onflow/flow-go/model/flow"
	"github.com/onflow/rosetta/access"
	"github.com/onflow/rosetta/config"
	"github.com/stretchr/testify/assert"
)

func TestVerifyBlockHash(t *testing.T) {
	// load mainnet config and get blocks exactly as state.go
	var startBlockHeight uint64 = 50767887
	var endBlockHeight uint64 = 50767897
	ctx := context.Background()
	spork, err := createSpork(ctx)
	if err != nil {
		assert.Fail(t, err.Error())
	}
	client := spork.AccessNodes.Client()
	for blockHeight := startBlockHeight; blockHeight < endBlockHeight; blockHeight++ {
		block, err := client.BlockByHeight(ctx, blockHeight)
		if err != nil {
			assert.Fail(t, err.Error())
		}
		blockHeader, err := client.BlockHeaderByHeight(ctx, blockHeight)
		if err != nil {
			assert.Fail(t, err.Error())
		}
		assert.True(t, verifyBlockHash(spork, block.Id, blockHeight, blockHeader, block))
	}
}

func TestVerifyExecutionResultHash(t *testing.T) {
	var startBlockHeight uint64 = 55114468
	var endBlockHeight uint64 = 55114477
	ctx := context.Background()
	spork, err := createSpork(ctx)
	if err != nil {
		assert.Fail(t, err.Error())
	}
	client := spork.AccessNodes.Client()
	var sealedResults map[string]string
	for blockHeight := startBlockHeight; blockHeight < endBlockHeight; blockHeight++ {
		block, err := client.BlockByHeight(ctx, blockHeight)
		if err != nil {
			assert.Fail(t, err.Error())
		}
		execResult, err := client.ExecutionResultForBlockID(ctx, block.Id)
		if err != nil {
			assert.Fail(t, err.Error())
		}
		for _, seal := range block.BlockSeals {
			sealedResults[string(seal.BlockId)] = string(seal.ResultId)
		}
		var resultID flow.Identifier
		var resultIDV5 flow.Identifier
		exec, ok := convertExecutionResult(block.Id, blockHeight, execResult)
		if ok {
			resultID = deriveExecutionResult(spork, exec)
		}
		execV5, okV5 := convertExecutionResultV5(block.Id, blockHeight, execResult)
		if okV5 {
			resultIDV5 = deriveExecutionResultV5(execV5)
		}
		if !ok && !okV5 {
			assert.Fail(t, "unable to covert from either hash")
		}
		sealedResult, foundOk := sealedResults[string(block.Id)]
		if foundOk {
			if string(resultID[:]) != sealedResult && string(resultIDV5[:]) != sealedResult {
				assert.Fail(t, "target error")
			}
		}
	}
}

func createSpork(ctx context.Context) (*config.Spork, error) {
	addr := "access-001.mainnet23.nodes.onflow.org:9000"
	pool := access.New(ctx, []access.NodeConfig{{Address: addr}}, nil)
	chain := &config.Chain{Network: "mainnet"}
	return &config.Spork{
		Version:     6,
		Chain:       chain,
		AccessNodes: pool,
		RootBlock:   55114467,
	}, nil
}
