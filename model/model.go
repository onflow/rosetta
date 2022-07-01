// Package model defines generic datatypes for Flow blockchain data.
package model

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/rlp"
	"github.com/onflow/flow/protobuf/go/flow/entities"
	"golang.org/x/crypto/sha3"
)

// Clone creates a copy of the block metadata.
func (b *BlockMeta) Clone() *BlockMeta {
	return &BlockMeta{
		Hash:      b.Hash,
		Height:    b.Height,
		Parent:    b.Parent,
		Timestamp: b.Timestamp,
	}
}

// Equal returns whether the given BlockMeta has the same hash, height, and
// timestamp.
func (b *BlockMeta) Equal(other *BlockMeta) bool {
	if b == nil || other == nil {
		return false
	}
	return bytes.Equal(b.Hash, other.Hash) &&
		b.Height == other.Height && b.Timestamp == other.Timestamp
}

// Pretty generates a custom-formatted representation of an operation.
func (o *Operation) Pretty() string {
	w := &strings.Builder{}
	w.WriteByte('{')
	if len(o.Account) == 0 {
		w.WriteString("account: nil, ")
	} else {
		fmt.Fprintf(w, `account: "0x%x", `, o.Account)
	}
	fmt.Fprintf(w, "amount: %v, ", o.Amount)
	if len(o.ProxyPublicKey) == 0 {
		w.WriteString("proxy_public_key: nil, ")
	} else {
		fmt.Fprintf(w, `proxy_public_key: "%x", `, o.ProxyPublicKey)
	}
	if len(o.Receiver) == 0 {
		w.WriteString("receiver: nil, ")
	} else {
		fmt.Fprintf(w, `receiver: "0x%x", `, o.Receiver)
	}
	fmt.Fprintf(w, `type: "%s"`, o.Type)
	w.WriteByte('}')
	return w.String()
}

// NOTE(tav): The ordering of the fields matter for the following data types, as
// they are used to RLP-encode data deterministically for hashing.

type flowEnvelope struct {
	Payload           flowTransactionPayload
	PayloadSignatures []flowTransactionSignature
}

type flowTransaction struct {
	Payload            flowTransactionPayload
	PayloadSignatures  []flowTransactionSignature
	EnvelopeSignatures []flowTransactionSignature
}

type flowTransactionPayload struct {
	Script                    []byte
	Arguments                 [][]byte
	ReferenceBlockID          []byte
	GasLimit                  uint64
	ProposalKeyAddress        []byte
	ProposalKeyIndex          uint64
	ProposalKeySequenceNumber uint64
	Payer                     []byte
	Authorizers               [][]byte
}

type flowTransactionSignature struct {
	SignerIndex uint
	KeyIndex    uint
	Signature   []byte
}

// TransactionEnvelopeHash computes the transaction's hash for use in
// transaction construction.
func TransactionEnvelopeHash(txn *entities.Transaction) ([]byte, error) {
	return deriveHash(
		txn,
		[]byte("FLOW-V0.0-transaction\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00"),
	)
}

// TransactionHash computes the transaction's hash for use in computing a
// block's payload hash.
func TransactionHash(txn *entities.Transaction) ([]byte, error) {
	return deriveHash(txn, nil)
}

func deriveHash(t *entities.Transaction, tag []byte) ([]byte, error) {
	payload := flowTransactionPayload{
		Arguments:                 t.Arguments,
		Authorizers:               t.Authorizers,
		GasLimit:                  t.GasLimit,
		Payer:                     t.Payer,
		ProposalKeyAddress:        t.ProposalKey.Address,
		ProposalKeyIndex:          uint64(t.ProposalKey.KeyId),
		ProposalKeySequenceNumber: t.ProposalKey.SequenceNumber,
		ReferenceBlockID:          t.ReferenceBlockId,
		Script:                    t.Script,
	}
	var (
		data []byte
		err  error
	)
	if tag == nil {
		txn := flowTransaction{
			Payload: payload,
		}
		for _, sig := range t.EnvelopeSignatures {
			txn.EnvelopeSignatures = append(txn.EnvelopeSignatures, flowTransactionSignature{
				KeyIndex:    uint(sig.KeyId),
				Signature:   sig.Signature,
				SignerIndex: 0,
			})
		}
		for _, sig := range t.PayloadSignatures {
			txn.PayloadSignatures = append(txn.PayloadSignatures, flowTransactionSignature{
				KeyIndex:    uint(sig.KeyId),
				Signature:   sig.Signature,
				SignerIndex: 0,
			})
		}
		data, err = rlp.EncodeToBytes(txn)
		if err != nil {
			return nil, fmt.Errorf("model: could not RLP-encode transaction: %s", err)
		}
	} else {
		txn := flowEnvelope{
			Payload: payload,
		}
		for _, sig := range t.PayloadSignatures {
			txn.PayloadSignatures = append(txn.PayloadSignatures, flowTransactionSignature{
				KeyIndex:    uint(sig.KeyId),
				Signature:   sig.Signature,
				SignerIndex: 0,
			})
		}
		data, err = rlp.EncodeToBytes(txn)
		if err != nil {
			return nil, fmt.Errorf("model: could not RLP-encode transaction: %s", err)
		}
	}
	hasher := sha3.New256()
	if tag != nil {
		_, _ = hasher.Write(tag)
	}
	_, _ = hasher.Write(data)
	return hasher.Sum(nil), nil
}
