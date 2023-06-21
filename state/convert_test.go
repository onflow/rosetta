package state

import (
	"context"
	"testing"

	"github.com/onflow/rosetta/access"
	"github.com/onflow/rosetta/config"
	"github.com/stretchr/testify/assert"
)

func TestVerifyBlockHash(t *testing.T) {
	// load mainnet config and get blocks exactly as state.go
	var startBlockHeight uint64 = 55114464
	var endBlockHeight uint64 = 55114466
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

func createSpork(ctx context.Context) (*config.Spork, error) {
	addr := "access-001.mainnet22.nodes.onflow.org:9000"
	pool := access.New(ctx, []access.NodeConfig{{Address: addr}}, nil)
	chain := &config.Chain{Network: "mainnet"}
	return &config.Spork{
		Version:     6,
		Chain:       chain,
		AccessNodes: pool,
		RootBlock:   47169687,
	}, nil
}
