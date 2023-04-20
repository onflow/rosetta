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
	var notWorkingBlockHeight uint64 = 50767887
	var workingBlockHeight uint64 = 50767885
	ctx := context.Background()
	spork, err := createSpork(ctx)
	if err != nil {
		assert.Fail(t, err.Error())
	}
	client := spork.AccessNodes.Client()
	workingBlock, err := client.BlockByHeight(ctx, workingBlockHeight)
	if err != nil {
		assert.Fail(t, err.Error())
	}
	notWorkingBlock, err := client.BlockByHeight(ctx, notWorkingBlockHeight)
	if err != nil {
		assert.Fail(t, err.Error())
	}
	workingBlockhdr, err := client.BlockHeaderByHeight(ctx, workingBlockHeight)
	if err != nil {
		assert.Fail(t, err.Error())
	}
	notWorkingBlockhdr, err := client.BlockHeaderByHeight(ctx, notWorkingBlockHeight)
	if err != nil {
		assert.Fail(t, err.Error())
	}
	assert.True(t, verifyBlockHash(spork, workingBlock.Id, workingBlockHeight, workingBlockhdr, workingBlock))
	assert.True(t, verifyBlockHash(spork, notWorkingBlock.Id, notWorkingBlockHeight, notWorkingBlockhdr, notWorkingBlock))
}

func createSpork(ctx context.Context) (*config.Spork, error) {
	addr := "access-001.mainnet22.nodes.onflow.org:9000"
	pool := access.New(ctx, []access.NodeConfig{{Address: addr}}, nil)
	chain := &config.Chain{Network: "mainnet-22"}
	return &config.Spork{
		Version:     5,
		Chain:       chain,
		AccessNodes: pool,
		RootBlock:   47169687,
	}, nil
}
