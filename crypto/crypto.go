// Package crypto provides support for converting secp256k1 keys between Flow
// and SEC compressed formats.
package crypto

import (
	"fmt"

	"github.com/btcsuite/btcd/btcec"
)

// ConvertFlowPublicKey re-encodes a secp256k1 public key from the format used
// by Flow to the SEC compressed format.
func ConvertFlowPublicKey(key []byte) ([]byte, error) {
	raw := append([]byte{4}, key...)
	pub, err := btcec.ParsePubKey(raw, btcec.S256())
	if err != nil {
		return nil, fmt.Errorf(
			"unable to decode Flow public key %x: %s",
			key, err,
		)
	}
	return pub.SerializeCompressed(), nil
}

// ConvertRosettaPublicKey re-encodes a SEC compressed secp256k1 public key to
// the format expected by Flow.
func ConvertRosettaPublicKey(key []byte) ([]byte, error) {
	pub, err := btcec.ParsePubKey(key, btcec.S256())
	if err != nil {
		return nil, fmt.Errorf(
			"unable to decode Rosetta public key %x: %s",
			key, err,
		)
	}
	return pub.SerializeUncompressed()[1:], nil
}
