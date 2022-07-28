// Package crypto provides support for converting secp256k1 keys between Flow
// and SEC compressed formats.
package crypto

import (
	"encoding/hex"
	"fmt"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
)

// ConvertDERSignature re-encodes a DER-encoded ECDSA signature into the format
// expected by Flow.
func ConvertDERSignature(sig string) (string, error) {
	if len(sig) < 16 || len(sig) > 144 {
		return "", fmt.Errorf(
			"got unexpected length for the signature %q: %d",
			sig, len(sig),
		)
	}
	raw, err := hex.DecodeString(sig)
	if err != nil {
		return "", fmt.Errorf(
			"unable to hex-decode signature %q: %s",
			sig, err,
		)
	}
	size := len(raw)
	if raw[0] != 0x30 {
		return "", fmt.Errorf("invalid signature %q", sig)
	}
	if int(raw[1]) != size-2 {
		return "", fmt.Errorf("invalid signature %q", sig)
	}
	if raw[2] != 0x02 {
		return "", fmt.Errorf("invalid signature %q", sig)
	}
	lenR := int(raw[3])
	if lenR == 0 || lenR > 33 {
		return "", fmt.Errorf("invalid signature %q", sig)
	}
	if sig[4]&0x80 != 0 {
		return "", fmt.Errorf("invalid signature %q", sig)
	}
	if lenR > 1 && sig[4] == 0x00 && sig[5]&0x80 == 0 {
		return "", fmt.Errorf("invalid signature %q", sig)
	}
	offset := 4 + lenR
	if offset > size {
		return "", fmt.Errorf("invalid signature %q", sig)
	}
	rawR := raw[4:offset]
	for len(rawR) > 0 && rawR[0] == 0x00 {
		rawR = rawR[1:]
	}
	if len(rawR) > 32 {
		return "", fmt.Errorf("invalid signature %q", sig)
	}
	r := &secp256k1.ModNScalar{}
	if overflow := r.SetByteSlice(rawR); overflow {
		return "", fmt.Errorf("invalid signature %q", sig)
	}
	if r.IsZero() {
		return "", fmt.Errorf("invalid signature %q", sig)
	}
	if raw[offset] != 0x02 {
		return "", fmt.Errorf("invalid signature %q", sig)
	}
	offset++
	lenS := int(raw[offset])
	if lenS == 0 || lenS > 33 {
		return "", fmt.Errorf("invalid signature %q", sig)
	}
	offset++
	if sig[offset]&0x80 != 0 {
		return "", fmt.Errorf("invalid signature %q", sig)
	}
	if lenS > 1 && sig[offset] == 0x00 && sig[offset+1]&0x80 == 0 {
		return "", fmt.Errorf("invalid signature %q", sig)
	}
	if offset+lenS > size {
		return "", fmt.Errorf("invalid signature %q", sig)
	}
	rawS := raw[offset : offset+lenS]
	for len(rawS) > 0 && rawS[0] == 0x00 {
		rawS = rawS[1:]
	}
	if len(rawS) > 32 {
		return "", fmt.Errorf("invalid signature %q", sig)
	}
	s := &secp256k1.ModNScalar{}
	if overflow := s.SetByteSlice(rawS); overflow {
		return "", fmt.Errorf("invalid signature %q", sig)
	}
	if s.IsZero() {
		return "", fmt.Errorf("invalid signature %q", sig)
	}
	var buf [64]byte
	bytesR := r.Bytes()
	bytesS := s.Bytes()
	copy(buf[:], bytesR[:])
	copy(buf[32:], bytesS[:])
	return hex.EncodeToString(buf[:]), nil
}

// ConvertFlowPublicKey re-encodes a secp256k1 public key from the format used
// by Flow to the SEC compressed format.
func ConvertFlowPublicKey(key []byte) ([]byte, error) {
	raw := append([]byte{4}, key...)
	pub, err := secp256k1.ParsePubKey(raw)
	if err != nil {
		return nil, fmt.Errorf(
			"unable to decode Flow public key %x: %s",
			key, err,
		)
	}
	return pub.SerializeCompressed(), nil
}

// ConvertFlowSignature re-encodes an ECDSA signature from Flow's format into
// DER encoding.
//
// The format of a DER encoded signature for secp256k1 is as follows:
//
//     0x30 <total length> 0x02 <length of R> <R> 0x02 <length of S> <S>
func ConvertFlowSignature(sig string) (string, error) {
	if len(sig) != 128 {
		return "", fmt.Errorf(
			"got unexpected length for the signature %q: %d",
			sig, len(sig),
		)
	}
	raw, err := hex.DecodeString(sig)
	if err != nil {
		return "", fmt.Errorf(
			"unable to hex-decode signature %q: %s",
			sig, err,
		)
	}
	r := append([]byte{0}, raw[:32]...)
	s := append([]byte{0}, raw[32:]...)
	for len(r) > 1 && r[0] == 0x00 && r[1]&0x80 == 0 {
		r = r[1:]
	}
	for len(s) > 1 && s[0] == 0x00 && s[1]&0x80 == 0 {
		s = s[1:]
	}
	size := 6 + len(r) + len(s)
	b := make([]byte, 0, size)
	b = append(b, 0x30)
	b = append(b, byte(size-2))
	b = append(b, 0x02)
	b = append(b, byte(len(r)))
	b = append(b, r...)
	b = append(b, 0x02)
	b = append(b, byte(len(s)))
	b = append(b, s...)
	return hex.EncodeToString(b), nil
}

// ConvertRosettaPublicKey re-encodes a SEC compressed secp256k1 public key to
// the format expected by Flow.
func ConvertRosettaPublicKey(key []byte) ([]byte, error) {
	pub, err := secp256k1.ParsePubKey(key)
	if err != nil {
		return nil, fmt.Errorf(
			"unable to decode Rosetta public key %x: %s",
			key, err,
		)
	}
	return pub.SerializeUncompressed()[1:], nil
}
