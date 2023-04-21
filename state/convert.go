package state

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	jsoncdc "github.com/onflow/cadence/encoding/json"
	_ "github.com/onflow/cadence/runtime/stdlib" // imported for side-effects only
	"github.com/onflow/flow-go/crypto"
	"github.com/onflow/flow-go/crypto/hash"
	"github.com/onflow/flow-go/model/fingerprint"
	"github.com/onflow/flow-go/model/flow"
	"github.com/onflow/flow-go/storage/merkle"
	"github.com/onflow/flow/protobuf/go/flow/entities"
	"github.com/onflow/rosetta/access"
	"github.com/onflow/rosetta/config"
	"github.com/onflow/rosetta/log"
)

// convertExecutionResultV5 temporary function to convert to v0.29.x ExecutionResults while using v0.30.x in go.mod
func convertExecutionResultV5(
	hash []byte,
	height uint64,
	result *entities.ExecutionResult) (flowExecutionResultV5, bool) {
	// largely repeated code, but inner types changed, so can't re-use/abstract convertExecutionResult
	exec := flowExecutionResultV5{
		BlockID:          toFlowIdentifier(result.BlockId),
		ExecutionDataID:  toFlowIdentifier(result.ExecutionDataId),
		PreviousResultID: toFlowIdentifier(result.PreviousResultId),
	}
	for _, chunk := range result.Chunks {
		exec.Chunks = append(exec.Chunks, &flowChunkV5{
			// same order as v0.29.x
			ChunkBody: flowChunkBodyV5{
				BlockID:              toFlowIdentifier(chunk.BlockId),
				CollectionIndex:      uint(chunk.CollectionIndex),
				EventCollection:      toFlowIdentifier(chunk.EventCollection),
				NumberOfTransactions: uint64(chunk.NumberOfTransactions),
				StartState:           flow.StateCommitment(toFlowIdentifier(chunk.StartState)),
				TotalComputationUsed: chunk.TotalComputationUsed,
			},
			EndState: flow.StateCommitment(toFlowIdentifier(chunk.EndState)),
			Index:    chunk.Index,
		})
	}
	for _, ev := range result.ServiceEvents {
		switch ev.Type {
		case flow.ServiceEventSetup:
			setup := &flow.EpochSetup{}
			err := json.Unmarshal(ev.Payload, setup)
			if err != nil {
				log.Errorf(
					"Failed to decode %q service event in block %x at height %d: %s",
					ev.Type, hash, height, err,
				)
				return flowExecutionResultV5{}, false
			}
			exec.ServiceEvents = append(exec.ServiceEvents, flow.ServiceEvent{
				Event: setup,
				Type:  ev.Type,
			})
		case flow.ServiceEventCommit:
			commit := &flow.EpochCommit{}
			err := json.Unmarshal(ev.Payload, commit)
			if err != nil {
				log.Errorf(
					"Failed to decode %q service event in block %x at height %d: %s",
					ev.Type, hash, height, err,
				)
				return flowExecutionResultV5{}, false
			}
			exec.ServiceEvents = append(exec.ServiceEvents, flow.ServiceEvent{
				Event: commit,
				Type:  ev.Type,
			})
		default:
			log.Errorf(
				"Unknown service event type in block %x at height %d: %q",
				hash, height, ev.Type,
			)
			return flowExecutionResultV5{}, false
		}
	}
	return exec, true

}

func convertExecutionResult(hash []byte, height uint64, result *entities.ExecutionResult) (flowExecutionResult, bool) {
	// todo: add V6 version branching here directly after mainnet23 spork
	exec := flowExecutionResult{
		BlockID:          toFlowIdentifier(result.BlockId),
		ExecutionDataID:  toFlowIdentifier(result.ExecutionDataId),
		PreviousResultID: toFlowIdentifier(result.PreviousResultId),
	}
	for _, chunk := range result.Chunks {
		exec.Chunks = append(exec.Chunks, &flow.Chunk{
			ChunkBody: flow.ChunkBody{
				BlockID:              toFlowIdentifier(chunk.BlockId),
				CollectionIndex:      uint(chunk.CollectionIndex),
				EventCollection:      toFlowIdentifier(chunk.EventCollection),
				NumberOfTransactions: uint64(chunk.NumberOfTransactions),
				StartState:           flow.StateCommitment(toFlowIdentifier(chunk.StartState)),
				TotalComputationUsed: chunk.TotalComputationUsed,
			},
			EndState: flow.StateCommitment(toFlowIdentifier(chunk.EndState)),
			Index:    chunk.Index,
		})
	}
	for _, ev := range result.ServiceEvents {
		switch ev.Type {
		case flow.ServiceEventSetup:
			setup := &flow.EpochSetup{}
			err := json.Unmarshal(ev.Payload, setup)
			if err != nil {
				log.Errorf(
					"Failed to decode %q service event in block %x at height %d: %s",
					ev.Type, hash, height, err,
				)
				return flowExecutionResult{}, false
			}
			exec.ServiceEvents = append(exec.ServiceEvents, flow.ServiceEvent{
				Event: setup,
				Type:  ev.Type,
			})
		case flow.ServiceEventCommit:
			commit := &flow.EpochCommit{}
			err := json.Unmarshal(ev.Payload, commit)
			if err != nil {
				log.Errorf(
					"Failed to decode %q service event in block %x at height %d: %s",
					ev.Type, hash, height, err,
				)
				return flowExecutionResult{}, false
			}
			exec.ServiceEvents = append(exec.ServiceEvents, flow.ServiceEvent{
				Event: commit,
				Type:  ev.Type,
			})
		default:
			log.Errorf(
				"Unknown service event type in block %x at height %d: %q",
				hash, height, ev.Type,
			)
			return flowExecutionResult{}, false
		}
	}
	return exec, true
}

func decodeEvent(typ string, evt *entities.Event, hash []byte, height uint64) []interface{} {
	val, err := jsoncdc.Decode(access.NoopMemoryGauge, evt.Payload)
	if err != nil {
		log.Errorf(
			"Failed to decode %s event payload in transaction %x in block %x at height %d: %s",
			typ, evt.TransactionId, hash, height, err,
		)
		time.Sleep(time.Second)
		return nil
	}
	fields, ok := val.ToGoValue().([]interface{})
	if !ok {
		log.Errorf(
			"Failed to convert %s event payload in transaction %x in block %x at height %d to Go slice",
			typ, evt.TransactionId, hash, height,
		)
		time.Sleep(time.Second)
		return nil
	}
	return fields
}

func deriveBlockHash(spork *config.Spork, hdr flowHeader) flow.Identifier {
	switch spork.Version {
	case 1, 2:
		return deriveBlockHashV1(hdr)
	case 3, 4:
		return deriveBlockHashV3(hdr)
	case 5:
		return deriveBlockHashV5(hdr)
	}
	panic("unreachable code")
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
	case 4, 5:
		return deriveEventsHashV4(events)
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

func deriveExecutionResult(spork *config.Spork, exec flowExecutionResult) flow.Identifier {
	switch spork.Version {
	case 1:
		return deriveExecutionResultV1(exec)
	case 2, 3, 4, 5:
		return deriveExecutionResultV2(exec)
	}
	panic("unreachable code")
}

func deriveExecutionResultV5(exec flowExecutionResultV5) flow.Identifier {
	// after the mainnet23 spork, deriveExecutionResult will produce the correct struct and hash
	// but while on mainnet22, we need to use the custom struct that reflects the old V5 order of elements
	return flow.MakeID(exec)
}

func deriveExecutionResultV1(exec flowExecutionResult) flow.Identifier {
	dst := struct {
		PreviousResultID flow.Identifier
		BlockID          flow.Identifier
		Chunks           flow.ChunkList
		ServiceEvents    flow.ServiceEventList
	}{
		BlockID:          exec.BlockID,
		Chunks:           exec.Chunks,
		PreviousResultID: exec.PreviousResultID,
		ServiceEvents:    exec.ServiceEvents,
	}
	return flow.MakeID(dst)
}

func deriveExecutionResultV2(exec flowExecutionResult) flow.Identifier {
	dst := struct {
		PreviousResultID flow.Identifier
		BlockID          flow.Identifier
		Chunks           flow.ChunkList
		ServiceEvents    flow.ServiceEventList
		ExecutionDataID  flow.Identifier
	}{
		BlockID:          exec.BlockID,
		Chunks:           exec.Chunks,
		PreviousResultID: exec.PreviousResultID,
		ServiceEvents:    exec.ServiceEvents,
		ExecutionDataID:  exec.ExecutionDataID,
	}
	return flow.MakeID(dst)
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
	if spork.Chain.Network == "canary" {
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
	var collectionIDs []flow.Identifier
	for _, src := range block.CollectionGuarantees {
		collectionIDs = append(collectionIDs, toFlowIdentifier(src.CollectionId))
	}
	collectionHash := flow.MerkleRoot(collectionIDs...)
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
		receipt := flow.ExecutionReceiptMeta{
			ExecutorID:        toFlowIdentifier(src.ExecutorId),
			ResultID:          toFlowIdentifier(src.ResultId),
			ExecutorSignature: src.ExecutorSignature,
			Spocks:            toSignatureSlice(src.Spocks),
		}
		receiptIDs = append(receiptIDs, receipt.ID())
	}
	receiptHash := flow.MerkleRoot(receiptIDs...)
	var resultIDs []flow.Identifier
	if spork.Version >= 5 {
		// for hashing versions V5 (mainnet22) and above
		// todo: remove this double check after mainnet23 spork, and have just one call to convertExecutionResult
		var resultIDsV5 []flow.Identifier
		for _, src := range block.ExecutionResultList {
			exec, ok := convertExecutionResult(hash, height, src)
			if ok {
				resultIDs = append(resultIDs, deriveExecutionResult(spork, exec))
			}
			execV5, okV5 := convertExecutionResultV5(hash, height, src)
			if okV5 {
				resultIDsV5 = append(resultIDsV5, deriveExecutionResultV5(execV5))
			}
			if !okV5 && !ok {
				// couldn't convert either way, go by regular code path
				return false
			}
		}
		resultHash := flow.MerkleRoot(resultIDs...)
		resultHashV5 := flow.MerkleRoot(resultIDsV5...)
		payloadHash := flow.ConcatSum(collectionHash, sealHash, receiptHash, resultHash)
		payloadHashV5 := flow.ConcatSum(collectionHash, sealHash, receiptHash, resultHashV5)
		log.Info(fmt.Sprintf("collectionHash: %x", collectionHash[:]))
		log.Info(fmt.Sprintf("sealHash: %x", sealHash[:]))
		log.Info(fmt.Sprintf("receiptHash: %x", receiptHash[:]))
		if payloadHash != xhdr.PayloadHash && payloadHashV5 != xhdr.PayloadHash {
			log.Errorf(
				"Mismatching payload hash for block %x at height %d: expected %x, got %x for version 6 and %x for Versions before it",
				hash, height, xhdr.PayloadHash[:], payloadHash[:], payloadHashV5[:],
			)
			return false
		}
		return true
	}
	for _, src := range block.ExecutionResultList {
		exec, ok := convertExecutionResult(hash, height, src)
		if !ok {
			return false
		}
		resultIDs = append(resultIDs, deriveExecutionResult(spork, exec))
	}
	resultHash := flow.MerkleRoot(resultIDs...)
	payloadHash := flow.ConcatSum(collectionHash, sealHash, receiptHash, resultHash)
	if payloadHash != xhdr.PayloadHash {
		log.Errorf(
			"Mismatching payload hash for block %x at height %d: expected %x, got %x",
			hash, height, xhdr.PayloadHash[:], payloadHash[:],
		)
		return false
	}
	return true
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
	Chunks           flow.ChunkList
	ExecutionDataID  flow.Identifier
	PreviousResultID flow.Identifier
	ServiceEvents    flow.ServiceEventList
}

// todo: decide if we should keep this or make this for V6 and above instead, since an inner structs have changed for V6+
// flowExecutionResultV5 follows ExecutionResult formats of v0.29.x
type flowExecutionResultV5 struct {
	BlockID          flow.Identifier
	Chunks           ChunkListV5
	ExecutionDataID  flow.Identifier
	PreviousResultID flow.Identifier
	ServiceEvents    flow.ServiceEventList
}

type flowChunkBodyV5 struct {
	BlockID              flow.Identifier
	CollectionIndex      uint
	EventCollection      flow.Identifier
	NumberOfTransactions uint64
	StartState           flow.StateCommitment
	TotalComputationUsed uint64
}

type flowChunkV5 struct {
	ChunkBody flowChunkBodyV5
	Index     uint64
	EndState  flow.StateCommitment
}

type ChunkListV5 []*flowChunkV5

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
