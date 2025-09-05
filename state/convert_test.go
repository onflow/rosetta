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

// sporkTemplate is used to construct a [config.Spork] for tests.
type sporkTemplate struct {
	AccessNodes []access.NodeConfig
	Chain       config.Chain
	// RootBlock doesn't need to be the actual root block of the spork during the convert tests, since
	// we directly provide the spork structure and don't use the indexer for these tests.
	RootBlock uint64
	// Version is a Rosetta-internal version number that tracks Rosetta compatibility with Flow network versions.
	// This version number is incremented each time there is a Flow network upgrades which includes a breaking change for Rosetta.
	// Not all Flow network upgrades cause breaking changes for Rosetta, so this version number is not incremented for every network upgrade.
	Version int
}

func (t *sporkTemplate) create(ctx context.Context) *config.Spork {
	return &config.Spork{
		AccessNodes: access.New(ctx, t.AccessNodes, nil),
		Chain:       &t.Chain,
		RootBlock:   t.RootBlock,
		Version:     t.Version,
	}
}

var Mainnet24_SporkVersion6 = sporkTemplate{
	AccessNodes: []access.NodeConfig{{Address: "access-001.mainnet24.nodes.onflow.org:9000"}},
	Chain:       config.Chain{Network: "mainnet"},
	RootBlock:   65264619,
	Version:     6,
}

var Mainnet26_SporkVersion7 = sporkTemplate{
	AccessNodes: []access.NodeConfig{{Address: "access-001.mainnet26.nodes.onflow.org:9000"}},
	Chain:       config.Chain{Network: "mainnet"},
	RootBlock:   125_000_000,
	Version:     7,
}

func TestVerifyBlockHash(t *testing.T) {
	t.Run("mainnet24 spork version 6", func(t *testing.T) {
		ctx := context.Background()
		spork := Mainnet24_SporkVersion6.create(ctx)
		VerifyBlocksForSpork(t, ctx, spork, 65264620, 65264630)
	})
	t.Run("mainnet26 spork version 7", func(t *testing.T) {
		ctx := context.Background()
		spork := Mainnet26_SporkVersion7.create(ctx)
		VerifyBlocksForSpork(t, ctx, spork, 125_000_001, 125_000_011)
	})
}

func VerifyBlocksForSpork(t *testing.T, ctx context.Context, spork *config.Spork, startHeight uint64, endHeight uint64) {
	client := spork.AccessNodes.Client()
	for blockHeight := startHeight; blockHeight < endHeight; blockHeight++ {
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
	t.Run("mainnet24 spork version 6", func(t *testing.T) {
		ctx := context.Background()
		spork := Mainnet24_SporkVersion6.create(ctx)
		VerifyExecutionResultsForSpork(t, ctx, spork, 65264620, 65264630)
	})
	t.Run("mainnet26 spork version 7", func(t *testing.T) {
		ctx := context.Background()
		spork := Mainnet26_SporkVersion7.create(ctx)
		VerifyExecutionResultsForSpork(t, ctx, spork, 125_000_001, 125_000_011)
	})
}

func VerifyExecutionResultsForSpork(t *testing.T, ctx context.Context, spork *config.Spork, startHeight uint64, endHeight uint64) {
	client := spork.AccessNodes.Client()
	sealedResults := make(map[string]string)
	for blockHeight := startHeight; blockHeight < endHeight; blockHeight++ {
		block, err := client.BlockByHeight(ctx, blockHeight)
		require.NoError(t, err)
		execResult, err := client.ExecutionResultForBlockID(ctx, block.Id)
		require.NoError(t, err)
		for _, seal := range block.BlockSeals {
			sealedResults[string(seal.BlockId)] = string(seal.ResultId)
		}
		exec, ok := convertExecutionResult(spork.Version, block.Id, blockHeight, execResult)
		require.True(t, ok, "unable to convert execution result")
		resultID := deriveExecutionResult(spork, exec)
		sealedResult, foundOk := sealedResults[string(block.Id)]
		if foundOk {
			require.Equal(t, string(resultID[:]), sealedResult, "target error")
		}
	}
}

func TestDeriveEventsHash(t *testing.T) {
	t.Run("mainnet24 / spork version 6", func(t *testing.T) {
		ctx := context.Background()
		spork := Mainnet24_SporkVersion6.create(ctx)
		VerifyEventsHashForSpork(t, ctx, spork, 65264620, 65264630)
	})
	t.Run("mainnet26 / spork version 7", func(t *testing.T) {
		ctx := context.Background()
		spork := Mainnet26_SporkVersion7.create(ctx)
		VerifyEventsHashForSpork(t, ctx, spork, 125_000_001, 125_000_011)
	})
}

func VerifyEventsHashForSpork(t *testing.T, ctx context.Context, spork *config.Spork, startHeight uint64, endHeight uint64) {
	client := spork.AccessNodes.Client()
	for blockHeight := startHeight; blockHeight < endHeight; blockHeight++ {
		block, err := client.BlockByHeight(ctx, blockHeight)
		require.NoError(t, err)
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
