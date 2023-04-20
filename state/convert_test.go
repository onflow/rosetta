package state

import (
	"context"
	"testing"

	"github.com/onflow/rosetta/config"
	"github.com/stretchr/testify/assert"
)

func TestVerifyBlockHash(t *testing.T) {
	// load mainnet config and get blocks exactly as state.go
	var notWorkingBlockHeight uint64 = 50767887
	var workingBlockHeight uint64 = 50767885
	ctx := context.Background()
	spork, err := createSpork()
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

func createSpork() (*config.Spork, error) {
	return nil, nil
}
