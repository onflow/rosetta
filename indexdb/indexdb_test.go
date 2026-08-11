package indexdb

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/dgraph-io/badger/v3"
	"github.com/stretchr/testify/require"

	"github.com/onflow/rosetta/model"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	opts := badger.DefaultOptions("").WithInMemory(true).WithLogger(nil)
	db, err := badger.Open(opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return &Store{db: db}
}

func addr(b byte) []byte {
	return []byte{b, b, b, b, b, b, b, b}
}

func TestFeeReceiversAt(t *testing.T) {
	s := newTestStore(t)

	// No events indexed yet.
	got, err := s.FeeReceiversAt(100)
	require.NoError(t, err)
	require.Nil(t, got)

	require.NoError(t, s.SetFeeReceivers(100, [][]byte{addr(1), addr(2)}))
	require.NoError(t, s.SetFeeReceivers(200, [][]byte{addr(3)}))

	// The most recent event at or before the given height applies.
	got, err = s.FeeReceiversAt(99)
	require.NoError(t, err)
	require.Nil(t, got)

	got, err = s.FeeReceiversAt(100)
	require.NoError(t, err)
	require.Equal(t, [][]byte{addr(1), addr(2)}, got)

	got, err = s.FeeReceiversAt(150)
	require.NoError(t, err)
	require.Equal(t, [][]byte{addr(1), addr(2)}, got)

	got, err = s.FeeReceiversAt(200)
	require.NoError(t, err)
	require.Equal(t, [][]byte{addr(3)}, got)

	got, err = s.FeeReceiversAt(1000)
	require.NoError(t, err)
	require.Equal(t, [][]byte{addr(3)}, got)
}

func TestSetFeeReceiversEmpty(t *testing.T) {
	s := newTestStore(t)

	// An event carrying an empty address list resets the child fee accounts.
	require.NoError(t, s.SetFeeReceivers(100, [][]byte{addr(1)}))
	require.NoError(t, s.SetFeeReceivers(200, [][]byte{}))

	got, err := s.FeeReceiversAt(200)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Empty(t, got)
}

func TestSetFeeReceiversInvalidAddress(t *testing.T) {
	s := newTestStore(t)
	err := s.SetFeeReceivers(100, [][]byte{[]byte("short")})
	require.Error(t, err)
}

func TestResetToDeletesFeeReceivers(t *testing.T) {
	s := newTestStore(t)
	require.NoError(t, s.SetGenesis(testBlockMeta(10)))

	ctx := context.Background()
	for height := uint64(11); height <= 200; height++ {
		if height == 50 {
			require.NoError(t, s.SetFeeReceivers(50, [][]byte{addr(1)}))
		}
		if height == 150 {
			require.NoError(t, s.SetFeeReceivers(150, [][]byte{addr(2)}))
		}
		err := s.Index(ctx, height, testBlockMeta(height).Hash, &model.IndexedBlock{})
		require.NoError(t, err)
	}

	require.NoError(t, s.ResetTo(100))

	got, err := s.FeeReceiversAt(100)
	require.NoError(t, err)
	require.Equal(t, [][]byte{addr(1)}, got)

	got, err = s.FeeReceiversAt(200)
	require.NoError(t, err)
	require.Equal(t, [][]byte{addr(1)}, got)
}

func testBlockMeta(height uint64) *model.BlockMeta {
	hash := make([]byte, 8)
	binary.BigEndian.PutUint64(hash, height)
	return &model.BlockMeta{
		Hash:   hash,
		Height: height,
	}
}
