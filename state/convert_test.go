package state

import (
	"bytes"
	"context"
	"testing"

	"github.com/onflow/flow-go/engine/common/rpc/convert"
	"github.com/onflow/flow-go/model/flow"
	"github.com/onflow/flow-go/utils/unittest"
	"github.com/onflow/flow/protobuf/go/flow/entities"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/onflow/rosetta/access"
	"github.com/onflow/rosetta/config"
)

var accessAddr = "access-001.mainnet24.nodes.onflow.org:9000"
var startBlockHeight uint64 = 65264620
var endBlockHeight uint64 = 65264630

func TestVerifyBlockHash(t *testing.T) {
	// load mainnet config and get blocks exactly as state.go
	ctx := context.Background()
	spork, err := createSpork(ctx)
	if err != nil {
		require.Fail(t, err.Error())
	}
	client := spork.AccessNodes.Client()
	for blockHeight := startBlockHeight; blockHeight < endBlockHeight; blockHeight++ {
		block, err := client.BlockByHeight(ctx, blockHeight)
		if err != nil {
			require.Fail(t, err.Error())
		}
		blockHeader, err := client.BlockHeaderByHeight(ctx, blockHeight)
		if err != nil {
			require.Fail(t, err.Error())
		}

		require.True(t, verifyBlockHash(spork, block.Id, blockHeight, blockHeader, block))
	}
}

func TestVerifyExecutionResultHash(t *testing.T) {
	ctx := context.Background()
	spork, err := createSpork(ctx)
	if err != nil {
		require.Fail(t, err.Error())
	}
	client := spork.AccessNodes.Client()
	sealedResults := make(map[string]string)
	for blockHeight := startBlockHeight; blockHeight < endBlockHeight; blockHeight++ {
		block, err := client.BlockByHeight(ctx, blockHeight)
		if err != nil {
			require.Fail(t, err.Error())
		}
		execResult, err := client.ExecutionResultForBlockID(ctx, block.Id)
		if err != nil {
			require.Fail(t, err.Error())
		}
		for _, seal := range block.BlockSeals {
			sealedResults[string(seal.BlockId)] = string(seal.ResultId)
		}
		var resultID flow.Identifier
		exec, ok := convertExecutionResult(spork.Version, block.Id, blockHeight, execResult)
		if ok {
			resultID = deriveExecutionResult(spork, exec)
		}
		if !ok {
			require.Fail(t, "unable to covert from either hash")
		}
		sealedResult, foundOk := sealedResults[string(block.Id)]
		if foundOk {
			if string(resultID[:]) != sealedResult {
				require.Fail(t, "target error")
			}
		}
	}
}

func TestDeriveEventsHash(t *testing.T) {
	ctx := context.Background()
	spork, err := createSpork(ctx)
	if err != nil {
		assert.Fail(t, err.Error())
	}
	client := spork.AccessNodes.Client()
	for blockHeight := startBlockHeight; blockHeight < endBlockHeight; blockHeight++ {
		block, err := client.BlockByHeight(ctx, blockHeight)
		txns, err := client.TransactionsByBlockID(ctx, block.Id)
		require.NoError(t, err)
		txnResults, err := client.TransactionResultsByBlockID(ctx, block.Id)
		require.NoError(t, err)
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
		require.NoError(t, err)
		require.Equal(t, len(eventHashes), len(execResult.Chunks))
		for idx, eventHash := range eventHashes {
			chunk := execResult.Chunks[idx]
			require.Equal(t, eventHash[:], chunk.EventCollection)
		}
	}
}

// TestExecutionResultConsistency_ChunkServiceEventCountField tests that Rosetta computes
// a consistent entity ID for execution results for both Protocol State v1 and v2 formats.
// Rosetta re-implements core models from flow-go, rather than using the model types
// exported by flow-go, which introduces risk that changes in flow-go are not reflected in Rosetta.
// This test case validates consistency only for Protocol State version 1 and 2.
// See https://github.com/onflow/flow-go/pull/6744 for additional context.
func TestExecutionResultConsistency_ChunkServiceEventCountField(t *testing.T) {
	t.Run("Execution Result - Protocol State v1", func(t *testing.T) {
		psv1Result := unittest.ExecutionResultFixture(func(result *flow.ExecutionResult) {
			for _, chunk := range result.Chunks {
				chunk.ServiceEventCount = nil
			}
		})

		psv1ProtobufResult, err := convert.ExecutionResultToMessage(psv1Result)
		require.NoError(t, err)
		psv1RosettaResult, ok := convertExecutionResult(7, nil, 0, psv1ProtobufResult)
		require.True(t, ok)
		rosettaResultID := deriveExecutionResultV2(psv1RosettaResult)
		assert.Equal(t, psv1Result.ID(), rosettaResultID)
	})
	t.Run("Execution Result - Protocol State v2", func(t *testing.T) {
		psv2Result := unittest.ExecutionResultFixture(func(result *flow.ExecutionResult) {
			result.Chunks[0].ServiceEventCount = unittest.PtrTo[uint16](0)
		})

		psv2ProtobufResult, err := convert.ExecutionResultToMessage(psv2Result)
		require.NoError(t, err)
		psv2RosettaResult, ok := convertExecutionResult(7, nil, 0, psv2ProtobufResult)
		require.True(t, ok)
		rosettaResultID := deriveExecutionResultV2(psv2RosettaResult)
		assert.Equal(t, psv2Result.ID(), rosettaResultID)
	})
}

func createSpork(ctx context.Context) (*config.Spork, error) {
	addr := accessAddr
	pool := access.New(ctx, []access.NodeConfig{{Address: addr}}, nil)
	chain := &config.Chain{Network: "mainnet"}
	return &config.Spork{
		Version:     6,
		Chain:       chain,
		AccessNodes: pool,
		RootBlock:   65264619,
	}, nil
}
