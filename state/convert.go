package state

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/onflow/cadence"
	"github.com/onflow/cadence/encoding/ccf"
	jsoncdc "github.com/onflow/cadence/encoding/json"
	_ "github.com/onflow/cadence/stdlib" // imported for side-effects only
	"github.com/onflow/crypto"
	"github.com/onflow/crypto/hash"
	"github.com/onflow/flow-go/model/fingerprint"
	"github.com/onflow/flow-go/model/flow"
	"github.com/onflow/flow-go/storage/merkle"
	"github.com/onflow/flow/protobuf/go/flow/entities"

	"github.com/onflow/rosetta/access"
	"github.com/onflow/rosetta/config"
	"github.com/onflow/rosetta/log"
)

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
		eventType := flow.ServiceEventType(ev.Type)
		switch eventType {
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
				Type:  eventType,
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
				Type:  eventType,
			})
		case flow.ServiceEventVersionBeacon:
			beacon := &flow.VersionBeacon{}
			err := json.Unmarshal(ev.Payload, beacon)
			if err != nil {
				log.Errorf(
					"Failed to decode %q service event in block %x at height %d: %s",
					ev.Type, hash, height, err,
				)
				return flowExecutionResult{}, false
			}
			exec.ServiceEvents = append(exec.ServiceEvents, flow.ServiceEvent{
				Event: beacon,
				Type:  eventType,
			})
		case flow.ServiceEventProtocolStateVersionUpgrade:
			event := &flow.ProtocolStateVersionUpgrade{}
			err := json.Unmarshal(ev.Payload, event)
			if err != nil {
				log.Errorf(
					"Failed to decode %q service event in block %x at height %d: %s",
					ev.Type, hash, height, err,
				)
				return flowExecutionResult{}, false
			}
			exec.ServiceEvents = append(exec.ServiceEvents, flow.ServiceEvent{
				Event: event,
				Type:  eventType,
			})
		case flow.ServiceEventRecover:
			event := &flow.EpochRecover{}
			err := json.Unmarshal(ev.Payload, event)
			if err != nil {
				log.Errorf(
					"Failed to decode %q service event in block %x at height %d: %s",
					ev.Type, hash, height, err,
				)
				return flowExecutionResult{}, false
			}
			exec.ServiceEvents = append(exec.ServiceEvents, flow.ServiceEvent{
				Event: event,
				Type:  eventType,
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
	case 4, 5, 6, 7:
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
	case 2, 3, 4, 5, 6, 7:
		return deriveExecutionResultV2(exec)
	}
	panic("unreachable code")
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
	for _, src := range block.ExecutionResultList {
		exec, ok := convertExecutionResult(hash, height, src)
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
	case 7:
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
	Chunks           flow.ChunkList
	ExecutionDataID  flow.Identifier
	PreviousResultID flow.Identifier
	ServiceEvents    flow.ServiceEventList
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
