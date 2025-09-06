package state

import (
	"bytes"
	"context"
	"testing"

	"github.com/onflow/flow-go/model/flow"
	"github.com/onflow/flow/protobuf/go/flow/entities"
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
	sealedResults := make(map[flow.Identifier]flow.Identifier)
	computedResults := make(map[flow.Identifier]flow.Identifier)
	for blockHeight := startHeight; blockHeight < endHeight; blockHeight++ {
		block, err := client.BlockByHeight(ctx, blockHeight)
		require.NoError(t, err)
		execResult, err := client.ExecutionResultForBlockID(ctx, block.Id)
		require.NoError(t, err)
		// Store the Result ID for any seals of previous blocks
		for _, seal := range block.BlockSeals {
			sealedResults[toFlowIdentifier(seal.BlockId)] = toFlowIdentifier(seal.ResultId)
		}
		// Compute the result ID for the current block
		exec, ok := convertExecutionResult(spork.Version, block.Id, blockHeight, execResult)
		require.True(t, ok, "unable to convert execution result")
		resultID := deriveExecutionResult(spork, exec)
		computedResults[toFlowIdentifier(block.Id)] = resultID
	}
	checkedResults := 0
	for blockID := range computedResults {
		if sealedResult, ok := sealedResults[blockID]; ok {
			// we found a seal for the block with the canonical ResultID
			require.Equal(t, sealedResult.String(), computedResults[blockID].String(), "mismatched result ID")
			checkedResults++
		}
	}
	require.NotZero(t, checkedResults)
	t.Logf("checked results: %d", checkedResults)
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
