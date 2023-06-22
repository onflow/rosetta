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
	var startBlockHeight uint64 = 55114467
	var endBlockHeight uint64 = 55114568
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
	var startBlockHeight uint64 = 55114467
	var endBlockHeight uint64 = 55114568
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

func TestDeriveEventsHash(t *testing.T) {
	var startBlockHeight uint64 = 55114467
	var endBlockHeight uint64 = 55114468
	ctx := context.Background()
	spork, err := createSpork(ctx)
	if err != nil {
		assert.Fail(t, err.Error())
	}
	client := spork.AccessNodes.Client()
	for blockHeight := startBlockHeight; blockHeight < endBlockHeight; blockHeight++ {
		block, err := client.BlockByHeight(ctx, blockHeight)
		assert.NoError(t, err)
		cols := []*collectionData{}
		eventHashes := []flow.Identifier{}
		txnIndex := -1
		for _, col := range block.CollectionGuarantees {
			colData := &collectionData{}
			info, err := client.CollectionByID(ctx, col.CollectionId)
			assert.NoError(t, err)
			for _, txnHash := range info.TransactionIds {
				info, err := client.Transaction(ctx, txnHash)
				assert.NoError(t, err)
				txnResult, err := client.TransactionResult(ctx, block.Id, uint32(txnIndex))
				txnIndex++
				colData.txns = append(colData.txns, info)
				colData.txnResults = append(colData.txnResults, txnResult)
			}
			cols = append(cols, colData)
		}
		for _, col := range cols {
			colEvents := []flowEvent{}
			for _, txnResult := range col.txnResults {
				for _, evt := range txnResult.Events {
					event := flowEvent{
						EventIndex:       evt.EventIndex,
						Payload:          evt.Payload,
						TransactionID:    toFlowIdentifier(evt.TransactionId),
						TransactionIndex: evt.TransactionIndex,
						Type:             flow.EventType(evt.Type),
					}
					colEvents = append(colEvents, event)
					print(event.String())
				}
			}
			eventHashes = append(eventHashes, deriveEventsHash(spork, colEvents))
		}
	}
}

func createSpork(ctx context.Context) (*config.Spork, error) {
	addr := "access-001.mainnet23.nodes.onflow.org:9000"
	pool := access.New(ctx, []access.NodeConfig{{Address: addr}}, nil)
	chain := &config.Chain{Network: "mainnet"}
	return &config.Spork{
		Version:     5,
		Chain:       chain,
		AccessNodes: pool,
		RootBlock:   55114467,
	}, nil
}
