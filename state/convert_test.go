package state

import (
	"bytes"
	"context"
	"testing"

	"github.com/onflow/flow-go/model/flow"
	"github.com/onflow/flow/protobuf/go/flow/entities"
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
	var startBlockHeight uint64 = 55114469
	var endBlockHeight uint64 = 55114478
	ctx := context.Background()
	spork, err := createSpork(ctx)
	if err != nil {
		assert.Fail(t, err.Error())
	}
	client := spork.AccessNodes.Client()
	for blockHeight := startBlockHeight; blockHeight < endBlockHeight; blockHeight++ {
		block, err := client.BlockByHeight(ctx, blockHeight)
		txns, err := client.TransactionsByBlockID(ctx, block.Id)
		assert.NoError(t, err)
		txnResults, err := client.TransactionResultsByBlockID(ctx, block.Id)
		assert.NoError(t, err)
		cols := []*collectionData{}
		col := &collectionData{}
		cols = append(cols, col)
		prev := []byte{}
		txnLen := len(txns)
		for idx, result := range txnResults {
			if idx != 0 {
				if !bytes.Equal(result.CollectionId, prev) {
					col = &collectionData{}
					cols = append(cols, col)
				}
			}
			if idx < txnLen {
				col.txns = append(col.txns, txns[idx])
			}
			col.txnResults = append(col.txnResults, result)
			prev = result.CollectionId
		}
		cols[len(cols)-1].system = true
		eventHashes := []flow.Identifier{}
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
				}
			}
			hash := deriveEventsHash(spork, colEvents)
			eventHashes = append(eventHashes, hash)
		}
		var execResult *entities.ExecutionResult
		execResult, err = client.ExecutionResultForBlockID(ctx, block.Id)
		assert.NoError(t, err)
		for idx, eventHash := range eventHashes {
			chunk := execResult.Chunks[idx]
			assert.Equal(t, eventHash[:], chunk.EventCollection)
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
