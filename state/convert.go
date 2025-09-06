package state

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/onflow/cadence"
	"github.com/onflow/cadence/encoding/ccf"
	jsoncdc "github.com/onflow/cadence/encoding/json"
	_ "github.com/onflow/cadence/stdlib" // imported for side-effects only
	"github.com/onflow/crypto"
	"github.com/onflow/crypto/hash"
	"github.com/onflow/flow-go/engine/common/rpc/convert"
	"github.com/onflow/flow-go/model/fingerprint"
	"github.com/onflow/flow-go/model/flow"
	"github.com/onflow/flow-go/storage/merkle"
	"github.com/onflow/flow/protobuf/go/flow/entities"
	"github.com/onflow/rosetta/access"
	"github.com/onflow/rosetta/config"
	"github.com/onflow/rosetta/log"
)

func convertExecutionResult(sporkVersion int, hash []byte, height uint64, result *entities.ExecutionResult) (flowExecutionResult, bool) {
	// todo: add V6 version branching here directly after mainnet23 spork
	exec := flowExecutionResult{
		BlockID:          toFlowIdentifier(result.BlockId),
		ExecutionDataID:  toFlowIdentifier(result.ExecutionDataId),
		PreviousResultID: toFlowIdentifier(result.PreviousResultId),
	}
	for _, chunk := range result.Chunks {
		convertedChunk, err := convertChunk(sporkVersion, chunk)
		if err != nil {
			log.Errorf("Failed to convert chunk in block %x at height %d:  %v", hash, height, err)
			return flowExecutionResult{}, false
		}
		exec.Chunks = append(exec.Chunks, convertedChunk)
	}
	for _, ev := range result.ServiceEvents {
		eventType := flow.ServiceEventType(ev.Type)
		serviceEvent, err := flow.ServiceEventJSONMarshaller.UnmarshalWithType(ev.Payload, eventType)
		if err != nil {
			log.Errorf(
				"Failed to decode %q service event in block %x at height %d: %s",
				ev.Type, hash, height, err,
			)
			return flowExecutionResult{}, false
		}
		exec.ServiceEvents = append(exec.ServiceEvents, serviceEvent)
	}
	return exec, true
}

// convertChunk corresponds to flow-go convert.MessageToChunk, with
// additional checks to support previous spork versions.
func convertChunk(sporkVersion int, protobufChunk *entities.Chunk) (*flowChunk, error) {
	startState, err := flow.ToStateCommitment(protobufChunk.StartState)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Message start state to Chunk: %w", err)
	}
	endState, err := flow.ToStateCommitment(protobufChunk.EndState)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Message end state to Chunk: %w", err)
	}
	var serviceEventCount *uint16
	switch sporkVersion {
	case 1, 2, 3, 4, 5, 6:
		// Protocol State v1: ServiceEventCount field not yet added. When querying a historical access node,
		// the Protobuf field will be automatically set to 0.
		serviceEventCount = nil
	case 7:
		// Protocol State v2+
		if (protobufChunk.ServiceEventCount & 0xffff0000) > 0 {
			// A high-order bit is set: this was used to encode `nil` during spork version 7.
			// The resulting Chunk is interpreted the same way as during previous spork versions.
			// See https://github.com/onflow/flow-go/commit/caa8d13bd9f7f18ff3ee51a23e749374f3170d23
			serviceEventCount = nil
		} else {
			c := uint16(protobufChunk.ServiceEventCount)
			serviceEventCount = &c
		}
	case 8:
		if protobufChunk.ServiceEventCount > 0xffff {
			return nil, fmt.Errorf("invalid Service Event Count (max allowed is 65535): %d", protobufChunk.ServiceEventCount)
		}
		c := uint16(protobufChunk.ServiceEventCount)
		serviceEventCount = &c
	}
	chunk := flowChunk{
		CollectionIndex:      uint(protobufChunk.CollectionIndex),
		StartState:           startState,
		EventCollection:      convert.MessageToIdentifier(protobufChunk.EventCollection),
		ServiceEventCount:    serviceEventCount,
		BlockID:              convert.MessageToIdentifier(protobufChunk.BlockId),
		TotalComputationUsed: protobufChunk.TotalComputationUsed,
		NumberOfTransactions: uint64(protobufChunk.NumberOfTransactions),
		Index:                protobufChunk.Index,
		EndState:             endState,
	}
	return &chunk, nil
}

func decodeEvent(typ string, evt *entities.Event, hash []byte, height uint64) (cadence.Event, error) {
	val, err := decodePayload(evt.Payload)
	if err != nil {
		log.Errorf(
			"Failed to decode %s event payload in transaction %x in block %x at height %d: %s",
			typ, evt.TransactionId, hash, height, err,
		)
		time.Sleep(time.Second)
		return cadence.Event{}, err
	}
	event, ok := val.(cadence.Event)
	if !ok {
		log.Errorf(
			"Failed to convert %s event payload in transaction %x in block %x at height %d to event",
			typ, evt.TransactionId, hash, height,
		)
		time.Sleep(time.Second)
		return cadence.Event{}, err
	}

	return event, nil
}

func decodePayload(payload []byte) (cadence.Value, error) {
	if ccf.HasMsgPrefix(payload) {
		// modern Access nodes support encoding events in CCF format
		return ccf.Decode(access.NoopMemoryGauge, payload)
	}

	return jsoncdc.Decode(access.NoopMemoryGauge, payload)
}

func deriveBlockHash(spork *config.Spork, hdr flowHeader) flow.Identifier {
	switch spork.Version {
	case 1, 2:
		return deriveBlockHashV1(hdr)
	case 3, 4:
		return deriveBlockHashV3(hdr)
	case 5, 6, 7:
		return deriveBlockHashV5(hdr)
	case 8:
		return deriveBlockHashV8(hdr)
	}
	panic("unreachable code")
}

func deriveTimeoutCertificateHashV5(tc *flow.TimeoutCertificate) flow.Identifier {
	if tc == nil {
		return flow.ZeroID
	}
	return flow.MakeID(struct {
		View          uint64
		NewestQCViews []uint64
		NewestQCID    flow.Identifier
		SignerIndices []byte
		SigData       crypto.Signature
	}{
		View:          tc.View,
		NewestQCViews: tc.NewestQCViews,
		NewestQCID:    tc.NewestQC.ID(),
		SignerIndices: tc.SignerIndices,
		SigData:       tc.SigData,
	})
}

func deriveBlockHashV1(hdr flowHeader) flow.Identifier {
	dst := struct {
		ChainID            flow.ChainID
		ParentID           flow.Identifier
		Height             uint64
		PayloadHash        flow.Identifier
		Timestamp          uint64
		View               uint64
		ParentVoterIDs     []flow.Identifier
		ParentVoterSigData []byte
		ProposerID         flow.Identifier
	}{
		ChainID:            hdr.ChainID,
		ParentID:           hdr.ParentID,
		Height:             hdr.Height,
		PayloadHash:        hdr.PayloadHash,
		Timestamp:          uint64(hdr.Timestamp.UnixNano()),
		View:               hdr.View,
		ParentVoterIDs:     hdr.ParentVoterIDs,
		ParentVoterSigData: hdr.ParentVoterSigData,
		ProposerID:         hdr.ProposerID,
	}
	return flow.MakeID(dst)
}

func deriveBlockHashV3(hdr flowHeader) flow.Identifier {
	dst := struct {
		ChainID            flow.ChainID
		ParentID           flow.Identifier
		Height             uint64
		PayloadHash        flow.Identifier
		Timestamp          uint64
		View               uint64
		ParentVoterIndices []byte
		ParentVoterSigData []byte
		ProposerID         flow.Identifier
	}{
		ChainID:            hdr.ChainID,
		ParentID:           hdr.ParentID,
		Height:             hdr.Height,
		PayloadHash:        hdr.PayloadHash,
		Timestamp:          uint64(hdr.Timestamp.UnixNano()),
		View:               hdr.View,
		ParentVoterIndices: hdr.ParentVoterIndices,
		ParentVoterSigData: hdr.ParentVoterSigData,
		ProposerID:         hdr.ProposerID,
	}
	return flow.MakeID(dst)
}

func deriveBlockHashV5(hdr flowHeader) flow.Identifier {
	dst := struct {
		ChainID            flow.ChainID
		ParentID           flow.Identifier
		Height             uint64
		PayloadHash        flow.Identifier
		Timestamp          uint64
		View               uint64
		ParentView         uint64
		ParentVoterIndices []byte
		ParentVoterSigData []byte
		ProposerID         flow.Identifier
		LastViewTCID       flow.Identifier
	}{
		ChainID:            hdr.ChainID,
		ParentID:           hdr.ParentID,
		Height:             hdr.Height,
		PayloadHash:        hdr.PayloadHash,
		Timestamp:          uint64(hdr.Timestamp.UnixNano()),
		View:               hdr.View,
		ParentView:         hdr.ParentView,
		ParentVoterIndices: hdr.ParentVoterIndices,
		ParentVoterSigData: hdr.ParentVoterSigData,
		ProposerID:         hdr.ProposerID,
		LastViewTCID:       deriveTimeoutCertificateHashV5(hdr.LastViewTC),
	}
	return flow.MakeID(dst)
}

func deriveBlockHashV8(hdr flowHeader) flow.Identifier {
	dst := struct {
		ChainID            flow.ChainID
		ParentID           flow.Identifier
		Height             uint64
		PayloadHash        flow.Identifier
		Timestamp          uint64
		View               uint64
		ParentView         uint64
		ParentVoterIndices []byte
		ParentVoterSigData []byte
		ProposerID         flow.Identifier
		LastViewTCID       flow.Identifier
	}{
		ChainID:            hdr.ChainID,
		ParentID:           hdr.ParentID,
		Height:             hdr.Height,
		PayloadHash:        hdr.PayloadHash,
		Timestamp:          uint64(hdr.Timestamp.UnixMilli()),
		View:               hdr.View,
		ParentView:         hdr.ParentView,
		ParentVoterIndices: hdr.ParentVoterIndices,
		ParentVoterSigData: hdr.ParentVoterSigData,
		ProposerID:         hdr.ProposerID,
		LastViewTCID:       hdr.LastViewTC.ID(),
	}
	return flow.MakeID(dst)
}

func deriveEventsHash(spork *config.Spork, events []flowEvent) flow.Identifier {
	switch spork.Version {
	case 1:
		return deriveEventsHashV1(events)
	case 2, 3:
		return deriveEventsHashV2(events)
	case 4, 5, 6, 7:
		return deriveEventsHashV4(events)
	case 8:
		return deriveEventsHashV8(events)
	}
	panic("unreachable code")
}

func deriveEventsHashV1(events []flowEvent) flow.Identifier {
	hasher := hash.NewSHA3_256()
	for _, src := range events {
		dst := struct {
			TxID             []byte
			Index            uint32
			Type             string
			TransactionIndex uint32
			Payload          []byte
		}{
			TxID:             src.TransactionID[:],
			Index:            src.EventIndex,
			Type:             string(src.Type),
			TransactionIndex: src.TransactionIndex,
			Payload:          src.Payload,
		}
		_, err := hasher.Write(fingerprint.Fingerprint(dst))
		if err != nil {
			log.Fatalf("Failed to write to SHA3-256 hasher: %s", err)
		}
	}
	return toFlowIdentifier(hasher.SumHash())
}

func deriveEventsHashV2(events []flowEvent) flow.Identifier {
	tree, err := merkle.NewTree(flow.IdentifierLen)
	if err != nil {
		log.Fatalf("Failed to instantiate merkle tree: %s", err)
	}
	for _, src := range events {
		dst := struct {
			TxID             []byte
			Index            uint32
			Type             string
			TransactionIndex uint32
			Payload          []byte
		}{
			TxID:             src.TransactionID[:],
			Index:            src.EventIndex,
			Type:             string(src.Type),
			TransactionIndex: src.TransactionIndex,
			Payload:          src.Payload,
		}
		fp := fingerprint.Fingerprint(dst)
		eventID := flow.MakeID(fp)
		_, err = tree.Put(eventID[:], fp)
		if err != nil {
			log.Fatalf("Failed to put event into the merkle tree: %s", err)
		}
	}
	var root flow.Identifier
	copy(root[:], tree.Hash())
	return root
}

func deriveEventsHashV4(events []flowEvent) flow.Identifier {
	tree, err := merkle.NewTree(flow.IdentifierLen)
	if err != nil {
		log.Fatalf("Failed to instantiate merkle tree: %s", err)
	}
	for _, src := range events {
		dst := struct {
			TxID             []byte
			Index            uint32
			Type             string
			TransactionIndex uint32
			Payload          []byte
		}{
			TxID:             src.TransactionID[:],
			Index:            src.EventIndex,
			Type:             string(src.Type),
			TransactionIndex: src.TransactionIndex,
			Payload:          src.Payload,
		}
		fp := fingerprint.Fingerprint(dst)
		eventID := flow.MakeIDFromFingerPrint(fp)
		_, err = tree.Put(eventID[:], fp)
		if err != nil {
			log.Fatalf("Failed to put event into the merkle tree: %s", err)
		}
	}
	var root flow.Identifier
	copy(root[:], tree.Hash())
	return root
}

func deriveEventsHashV8(events []flowEvent) flow.Identifier {
	tree, err := merkle.NewTree(flow.IdentifierLen)
	if err != nil {
		log.Fatalf("Failed to instantiate merkle tree: %s", err)
	}
	for _, src := range events {
		dst := struct {
			Type             string
			TxID             []byte
			TransactionIndex uint32
			EventIndex       uint32
			Payload          []byte
		}{
			Type:             string(src.Type),
			TxID:             src.TransactionID[:],
			TransactionIndex: src.TransactionIndex,
			EventIndex:       src.EventIndex,
			Payload:          src.Payload,
		}
		fp := fingerprint.Fingerprint(dst)
		eventID := flow.MakeIDFromFingerPrint(fp)
		_, err = tree.Put(eventID[:], fp)
		if err != nil {
			log.Fatalf("Failed to put event into the merkle tree: %s", err)
		}
	}
	var root flow.Identifier
	copy(root[:], tree.Hash())
	return root
}

func deriveExecutionResult(spork *config.Spork, exec flowExecutionResult) flow.Identifier {
	switch spork.Version {
	case 1:
		return deriveExecutionResultV1(exec)
	case 2, 3, 4, 5, 6:
		return deriveExecutionResultV2(exec) // ExecutionDataID field was added
	case 7:
		return deriveExecutionResultV7(exec) // Chunk.ServiceEventsCount field was added as pointer
	case 8:
		return deriveExecutionResultV8(exec) // Chunk.ServiceEventsCount field was changed to non-pointer
	}
	panic("unreachable code")
}

type ChunkBodyV1 struct {
	CollectionIndex      uint
	StartState           flow.StateCommitment
	EventCollection      flow.Identifier
	BlockID              flow.Identifier
	TotalComputationUsed uint64
	NumberOfTransactions uint64
}

type ChunkV1 struct {
	ChunkBodyV1 ChunkBodyV1
	Index       uint64
	EndState    flow.StateCommitment
}

func ChunksToV1(chunks []*flowChunk) []*ChunkV1 {
	result := make([]*ChunkV1, 0, len(chunks))
	for _, chunk := range chunks {
		result = append(result, &ChunkV1{
			ChunkBodyV1: ChunkBodyV1{
				CollectionIndex:      chunk.CollectionIndex,
				StartState:           chunk.StartState,
				EventCollection:      chunk.EventCollection,
				BlockID:              chunk.BlockID,
				TotalComputationUsed: chunk.TotalComputationUsed,
				NumberOfTransactions: chunk.NumberOfTransactions,
			},
			Index:    chunk.Index,
			EndState: chunk.EndState,
		})
	}
	return result
}

type ChunkBodyV7 struct {
	CollectionIndex      uint
	StartState           flow.StateCommitment
	EventCollection      flow.Identifier
	ServiceEventCount    *uint16
	BlockID              flow.Identifier
	TotalComputationUsed uint64
	NumberOfTransactions uint64
}

type ChunkV7 struct {
	ChunkBodyV7 ChunkBodyV7
	Index       uint64
	EndState    flow.StateCommitment
}

func ChunksToV7(chunks []*flowChunk) []*ChunkV7 {
	result := make([]*ChunkV7, 0, len(chunks))
	for _, chunk := range chunks {
		result = append(result, &ChunkV7{
			ChunkBodyV7: ChunkBodyV7{
				CollectionIndex:      chunk.CollectionIndex,
				StartState:           chunk.StartState,
				EventCollection:      chunk.EventCollection,
				ServiceEventCount:    chunk.ServiceEventCount,
				BlockID:              chunk.BlockID,
				TotalComputationUsed: chunk.TotalComputationUsed,
				NumberOfTransactions: chunk.NumberOfTransactions,
			},
			Index:    chunk.Index,
			EndState: chunk.EndState,
		})
	}
	return result
}

func ChunksToV8(chunks []*flowChunk) []*flow.Chunk {
	result := make([]*flow.Chunk, 0, len(chunks))
	for _, chunk := range chunks {
		result = append(result, &flow.Chunk{
			ChunkBody: flow.ChunkBody{
				CollectionIndex:      chunk.CollectionIndex,
				StartState:           chunk.StartState,
				EventCollection:      chunk.EventCollection,
				ServiceEventCount:    chunk.ServiceEventCount,
				BlockID:              chunk.BlockID,
				TotalComputationUsed: chunk.TotalComputationUsed,
				NumberOfTransactions: chunk.NumberOfTransactions,
			},
			Index:    chunk.Index,
			EndState: chunk.EndState,
		})
	}
	return result
}

func deriveExecutionResultV1(exec flowExecutionResult) flow.Identifier {
	dst := struct {
		PreviousResultID flow.Identifier
		BlockID          flow.Identifier
		Chunks           []*ChunkV1
		ServiceEvents    flow.ServiceEventList
	}{
		BlockID:          exec.BlockID,
		Chunks:           ChunksToV1(exec.Chunks),
		PreviousResultID: exec.PreviousResultID,
		ServiceEvents:    exec.ServiceEvents,
	}
	return flow.MakeID(dst)
}

func deriveExecutionResultV2(exec flowExecutionResult) flow.Identifier {
	dst := struct {
		PreviousResultID flow.Identifier
		BlockID          flow.Identifier
		Chunks           []*ChunkV1
		ServiceEvents    flow.ServiceEventList
		ExecutionDataID  flow.Identifier
	}{
		BlockID:          exec.BlockID,
		Chunks:           ChunksToV1(exec.Chunks),
		PreviousResultID: exec.PreviousResultID,
		ServiceEvents:    exec.ServiceEvents,
		ExecutionDataID:  exec.ExecutionDataID,
	}
	return flow.MakeID(dst)
}

func deriveExecutionResultV7(exec flowExecutionResult) flow.Identifier {
	if len(exec.Chunks) > 0 && exec.Chunks[0].ServiceEventCount == nil {
		// Chunks are required to consistently have all nil or all non-nil ServiceEventCount.
		// In the nil case, the hash is calculated the same as in the previous spork version
		// (i.e. the ServiceEventCount field is totally omitted.)
		return deriveExecutionResultV2(exec)
	}
	dst := struct {
		PreviousResultID flow.Identifier
		BlockID          flow.Identifier
		Chunks           []*ChunkV7
		ServiceEvents    flow.ServiceEventList
		ExecutionDataID  flow.Identifier
	}{
		BlockID:          exec.BlockID,
		Chunks:           ChunksToV7(exec.Chunks),
		PreviousResultID: exec.PreviousResultID,
		ServiceEvents:    exec.ServiceEvents,
		ExecutionDataID:  exec.ExecutionDataID,
	}
	return flow.MakeID(dst)
}

func deriveExecutionResultV8(exec flowExecutionResult) flow.Identifier {
	dst := struct {
		PreviousResultID flow.Identifier
		BlockID          flow.Identifier
		Chunks           flow.ChunkList
		ServiceEvents    flow.ServiceEventList
		ExecutionDataID  flow.Identifier
	}{
		BlockID:          exec.BlockID,
		Chunks:           ChunksToV8(exec.Chunks),
		PreviousResultID: exec.PreviousResultID,
		ServiceEvents:    exec.ServiceEvents,
		ExecutionDataID:  exec.ExecutionDataID,
	}
	return flow.MakeID(dst)
}

func deriveExecutionReceiptHash(spork *config.Spork, receipt flow.ExecutionReceiptStub) flow.Identifier {
	switch spork.Version {
	case 1, 2, 3, 4, 5, 6, 7:
		return receipt.UnsignedExecutionReceiptStub.ID()
	case 8:
		return receipt.ID()
	default:
		panic("unreachable code")
	}
}

func deriveCollectionGuaranteeID(spork *config.Spork, guarantee *entities.CollectionGuarantee) flow.Identifier {
	switch spork.Version {
	case 1, 2, 3, 4, 5, 6, 7:
		return toFlowIdentifier(guarantee.CollectionId)
	case 8:
		return flow.MakeID(struct {
			CollectionID     flow.Identifier
			ReferenceBlockID flow.Identifier
			ClusterChainID   flow.ChainID
			SignerIndices    []byte
			Signature        crypto.Signature
		}{
			CollectionID:     toFlowIdentifier(guarantee.CollectionId),
			ReferenceBlockID: toFlowIdentifier(guarantee.ReferenceBlockId),
			ClusterChainID:   flow.ChainID(guarantee.ClusterChainId),
			SignerIndices:    guarantee.SignerIndices,
			Signature:        guarantee.Signature,
		})
	default:
		panic("unreachable code")
	}
}

func toFlowIdentifier(v []byte) flow.Identifier {
	id := flow.Identifier{}
	copy(id[:], v)
	return id
}

func toIdentifierSlice(v [][]byte) []flow.Identifier {
	xs := make([]flow.Identifier, len(v))
	for i, elem := range v {
		copy(xs[i][:], elem)
	}
	return xs
}

func toSignatureSlice(v [][]byte) []crypto.Signature {
	xs := make([]crypto.Signature, len(v))
	for i, elem := range v {
		sig := make(crypto.Signature, len(elem))
		copy(sig, elem)
		xs[i] = sig
	}
	return xs
}

func verifyBlockHash(spork *config.Spork, hash []byte, height uint64, hdr *entities.BlockHeader, block *entities.Block) bool {
	chainID := flow.ChainID("flow-" + spork.Chain.Network)
	if spork.Chain.Network == "emulator" {
		chainID = flow.ChainID("flow-benchnet")
	}

	var lastViewTC *flow.TimeoutCertificate
	if hdr.LastViewTc != nil {
		newestQC := hdr.LastViewTc.HighestQc
		if newestQC == nil {
			log.Errorf("invalid structure newest QC should be present")
			return false
		}
		lastViewTC = &flow.TimeoutCertificate{
			View:          hdr.LastViewTc.View,
			NewestQCViews: hdr.LastViewTc.HighQcViews,
			SignerIndices: hdr.LastViewTc.SignerIndices,
			SigData:       hdr.LastViewTc.SigData,
			NewestQC: &flow.QuorumCertificate{
				View:          newestQC.View,
				BlockID:       toFlowIdentifier(newestQC.BlockId),
				SignerIndices: newestQC.SignerIndices,
				SigData:       newestQC.SigData,
			},
		}
	}

	xhdr := flowHeader{
		ChainID:            chainID,
		Height:             hdr.Height,
		ParentID:           toFlowIdentifier(hdr.ParentId),
		ParentVoterIDs:     toIdentifierSlice(hdr.ParentVoterIds),
		ParentVoterIndices: hdr.ParentVoterIndices,
		ParentVoterSigData: hdr.ParentVoterSigData,
		PayloadHash:        toFlowIdentifier(hdr.PayloadHash),
		ProposerID:         toFlowIdentifier(hdr.ProposerId),
		ProposerSigData:    hdr.ProposerSigData,
		Timestamp:          hdr.Timestamp.AsTime().UTC(),
		View:               hdr.View,
		LastViewTC:         lastViewTC,
		ParentView:         hdr.ParentView,
	}
	blockID := deriveBlockHash(spork, xhdr)
	if !bytes.Equal(blockID[:], hash) {
		log.Errorf(
			"Mismatching block ID from header for block %x at height %d: got %x",
			hash, height, blockID[:],
		)
		return false
	}
	var guaranteeIDs []flow.Identifier
	for _, src := range block.CollectionGuarantees {
		guaranteeID := deriveCollectionGuaranteeID(spork, src)
		guaranteeIDs = append(guaranteeIDs, guaranteeID)
	}
	collectionHash := flow.MerkleRoot(guaranteeIDs...)
	var sealIDs []flow.Identifier
	for _, src := range block.BlockSeals {
		seal := &flow.Seal{
			AggregatedApprovalSigs: make([]flow.AggregatedSignature, len(src.AggregatedApprovalSigs)),
			BlockID:                toFlowIdentifier(src.BlockId),
			FinalState:             flow.StateCommitment(toFlowIdentifier(src.FinalState)),
			ResultID:               toFlowIdentifier(src.ResultId),
		}
		for i, sig := range src.AggregatedApprovalSigs {
			seal.AggregatedApprovalSigs[i] = flow.AggregatedSignature{
				SignerIDs:          toIdentifierSlice(sig.SignerIds),
				VerifierSignatures: toSignatureSlice(sig.VerifierSignatures),
			}
		}
		sealIDs = append(sealIDs, seal.ID())
	}
	sealHash := flow.MerkleRoot(sealIDs...)
	var receiptIDs []flow.Identifier
	for _, src := range block.ExecutionReceiptMetaList {
		receipt := flow.ExecutionReceiptStub{
			UnsignedExecutionReceiptStub: flow.UnsignedExecutionReceiptStub{
				ExecutorID: toFlowIdentifier(src.ExecutorId),
				ResultID:   toFlowIdentifier(src.ResultId),
				Spocks:     toSignatureSlice(src.Spocks),
			},
			ExecutorSignature: src.ExecutorSignature,
		}
		receiptIDs = append(receiptIDs, deriveExecutionReceiptHash(spork, receipt))
	}
	receiptHash := flow.MerkleRoot(receiptIDs...)

	var resultIDs []flow.Identifier
	for _, src := range block.ExecutionResultList {
		exec, ok := convertExecutionResult(spork.Version, hash, height, src)
		if ok {
			resultIDs = append(resultIDs, deriveExecutionResult(spork, exec))
		}
		if !ok {
			// couldn't convert either way, go by regular code path
			return false
		}
	}
	resultHash := flow.MerkleRoot(resultIDs...)
	payloadHash := derivePayloadHash(spork.Version, collectionHash, sealHash, receiptHash, resultHash, toFlowIdentifier(block.ProtocolStateId))

	if payloadHash != xhdr.PayloadHash {
		log.Errorf(
			"Mismatching payload hash for block %x at height %d: expected %x, got %x for version %d",
			hash, height, xhdr.PayloadHash[:], payloadHash[:], spork.Version,
		)
		return false
	}
	return true
}

func derivePayloadHash(
	sporkVersion int,
	collectionHash flow.Identifier,
	sealHash flow.Identifier,
	receiptHash flow.Identifier,
	resultHash flow.Identifier,
	protocolStateId flow.Identifier,
) flow.Identifier {
	switch sporkVersion {
	case 1, 2, 3, 4, 5, 6:
		return derivePayloadHashV1(collectionHash, sealHash, receiptHash, resultHash)
	case 7, 8:
		return derivePayloadHashV7(collectionHash, sealHash, receiptHash, resultHash, protocolStateId)
	default:
		panic("unreachable code")
	}
}

// derivePayloadHashV1 generates a payload hash for block versions V1 - V6.
// It concatenates and hashes the provided collection, seal, receipt, and result hashes.
func derivePayloadHashV1(collectionHash flow.Identifier,
	sealHash flow.Identifier,
	receiptHash flow.Identifier,
	resultHash flow.Identifier,
) flow.Identifier {
	return flow.ConcatSum(collectionHash, sealHash, receiptHash, resultHash)
}

// derivePayloadHashV7 generates a payload hash for block version V7.
// It concatenates and hashes the provided collection, seal, receipt, result, and protocol state hashes.
func derivePayloadHashV7(
	collectionHash flow.Identifier,
	sealHash flow.Identifier,
	receiptHash flow.Identifier,
	resultHash flow.Identifier,
	protocolStateId flow.Identifier,
) flow.Identifier {
	return flow.ConcatSum(collectionHash, sealHash, receiptHash, resultHash, protocolStateId)
}

type flowEvent struct {
	EventIndex       uint32
	Payload          []byte
	TransactionID    flow.Identifier
	TransactionIndex uint32
	Type             flow.EventType
}

func (f flowEvent) String() string {
	b := &strings.Builder{}
	fmt.Fprintf(b, "flowEvent{\n")
	fmt.Fprintf(b, "\tEventIndex: %d\n", f.EventIndex)
	fmt.Fprintf(b, "\tPayload: %s\n", f.Payload)
	fmt.Fprintf(b, "\tTransactionID: %x\n", f.TransactionID)
	fmt.Fprintf(b, "\tTransactionIndex: %d\n", f.TransactionIndex)
	fmt.Fprintf(b, "\tType: %q\n", f.Type)
	fmt.Fprintf(b, "}")
	return b.String()
}

type flowExecutionResult struct {
	BlockID          flow.Identifier
	Chunks           []*flowChunk
	ExecutionDataID  flow.Identifier
	PreviousResultID flow.Identifier
	ServiceEvents    flow.ServiceEventList
}

type flowChunk struct {
	// Embedded fields of flow.ChunkBody are directly included here.
	CollectionIndex      uint
	StartState           flow.StateCommitment
	EventCollection      flow.Identifier
	ServiceEventCount    *uint16 // Absent before sporkVersion 7; always present in sporkVersion 8 and later
	BlockID              flow.Identifier
	TotalComputationUsed uint64
	NumberOfTransactions uint64
	Index                uint64
	EndState             flow.StateCommitment
}

type flowHeader struct {
	ChainID            flow.ChainID
	Height             uint64
	ParentID           flow.Identifier
	ParentVoterIDs     []flow.Identifier
	ParentVoterIndices []byte
	ParentVoterSigData []byte
	PayloadHash        flow.Identifier
	ProposerID         flow.Identifier
	ProposerSigData    []byte
	Timestamp          time.Time
	View               uint64
	LastViewTC         *flow.TimeoutCertificate
	ParentView         uint64
}
