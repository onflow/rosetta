// Package indexdb provides a Rosetta-specific index of the Flow chain data.
package indexdb

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"

	"github.com/onflow/flow-go/storage"
	"github.com/onflow/flow-go/storage/operation"
	"github.com/onflow/flow-go/storage/operation/pebbleimpl"
	"github.com/onflow/flow-go/storage/pebble"
	"github.com/onflow/rosetta/log"
	"github.com/onflow/rosetta/model"
	"github.com/onflow/rosetta/process"
	"github.com/onflow/rosetta/trace"
	zerolog "github.com/rs/zerolog/log"
	"google.golang.org/protobuf/proto"
)

var (
	ErrBlockNotIndexed = errors.New("indexdb: block not indexed")
)

// NOTE(tav): We store the blockchain data within Badger using the following
// key/value structure:
//
//          accountKey a<acct><height-big-endian> = <amount-big-endian>
//            blockKey b<height-big-endian> = model.IndexedBlock
// blockHash2HeightKey c<block-hash> = <height-big-endian>
// blockHeight2HashKey d<height-big-endian> = <block-hash>
//          isProxyKey p<acct><height-big-endian> = 1
//                     genesis = model.BlockMeta
//                     latest = model.BlockMeta

// AccountInfo represents all the balance changes (block height, balance) for an
// account and whether it's a proxy account or not.
type AccountInfo struct {
	Changes [][2]uint64 `json:"changes"`
	Proxy   bool        `json:"proxy,omitempty"`
}

// BalanceData represents the indexed balance of an account.
type BalanceData struct {
	Balance uint64
	Hash    []byte
	Height  uint64
}

// BlockData represents the indexed data for a block.
type BlockData struct {
	Block        *model.IndexedBlock
	Hash         []byte
	Height       uint64
	ParentHash   []byte
	ParentHeight uint64
}

// Store aggregates the blockchain data for Rosetta API calls.
type Store struct {
	db      storage.DB
	genesis *model.BlockMeta
	latest  *model.BlockMeta
	mu      sync.RWMutex // protects genesis and latest
}

// Accounts returns the addresses of all our indexed accounts, and whether the
// account is a proxy account or not.
func (s *Store) Accounts() (map[[8]byte]bool, error) {
	// NOTE(tav): We scan the entire database and allocate for all accounts
	// here. Even if we had a 100 million accounts, this would only take up
	// about 800MB of memory which seems reasonable.
	//
	// If this ever becomes a bottleneck, we could add a method that returns
	// batches of accounts instead.
	accts := map[[8]byte]bool{}
	accountPrefix := []byte("a")
	err := operation.IterateKeys(s.db.Reader(), accountPrefix, accountPrefix,
		operation.KeyOnlyIterateFunc(func(keyCopy []byte) error {
			acct := [8]byte{}
			copy(acct[:], keyCopy[1:9])
			accts[acct] = false // not a proxy account
			return nil
		}), storage.IteratorOption{})
	if err != nil {
		return nil, fmt.Errorf("indexdb: failed to get all accounts: %w", err)
	}
	proxyAccountPrefix := []byte("p")
	err = operation.IterateKeys(s.db.Reader(), proxyAccountPrefix, proxyAccountPrefix,
		operation.KeyOnlyIterateFunc(func(keyCopy []byte) error {
			acct := [8]byte{}
			copy(acct[:], keyCopy[1:9])
			if _, ok := accts[acct]; !ok {
				return fmt.Errorf(
					"indexdb: found proxy account %x that doesn't have an account balance",
					acct,
				)
			}
			accts[acct] = true // is a proxy account
			return nil
		}), storage.IteratorOption{})
	if err != nil {
		return nil, fmt.Errorf("indexdb: failed to get all accounts: %w", err)
	}
	return accts, nil
}

// AccountsInfo will return a map of the indexed accounts and their
// corresponding account info.
func (s *Store) AccountsInfo() (map[string]*AccountInfo, error) {
	data := map[string]*AccountInfo{}
	accountPrefix := []byte("a")
	err := operation.IterateKeys(s.db.Reader(), accountPrefix, accountPrefix,
		func(keyCopy []byte, getValue func(destVal any) error) (bail bool, err error) {
			acct := hex.EncodeToString(keyCopy[1:9])
			info, ok := data[acct]
			if !ok {
				info = &AccountInfo{}
				data[acct] = info
			}
			var balanceData []byte
			err = getValue(&balanceData)
			if err != nil {
				return true, err
			}
			balance := binary.BigEndian.Uint64(balanceData)
			info.Changes = append(info.Changes, [2]uint64{
				binary.BigEndian.Uint64(keyCopy[9:]),
				balance,
			})
			return false, nil
		}, storage.IteratorOption{})
	if err != nil {
		return nil, fmt.Errorf("indexdb: failed to process indexed accounts: %s", err)
	}

	proxyAccountPrefix := []byte("p")
	err = operation.IterateKeys(s.db.Reader(), proxyAccountPrefix, proxyAccountPrefix,
		operation.KeyOnlyIterateFunc(func(keyCopy []byte) error {
			acct := hex.EncodeToString(keyCopy[1:9])
			info, ok := data[acct]
			if !ok {
				return fmt.Errorf(
					"indexdb: failed to find account balance for proxy account 0x%s",
					acct,
				)
			}
			info.Proxy = true
			return nil
		}), storage.IteratorOption{})
	if err != nil {
		return nil, fmt.Errorf("indexdb: failed to process proxy accounts: %s", err)
	}
	return data, nil
}

// BalanceByHash returns the account balance at the given hash.
func (s *Store) BalanceByHash(acct []byte, hash []byte) (*BalanceData, error) {
	height, err := s.HeightForHash(hash)
	if err != nil {
		return nil, err
	}
	balance, err := s.balanceByHeight(acct, height)
	if err != nil {
		return nil, err
	}
	return &BalanceData{
		Balance: balance,
		Hash:    hash,
		Height:  height,
	}, nil
}

// BalanceByHeight returns the account balance at the given height.
func (s *Store) BalanceByHeight(acct []byte, height uint64) (*BalanceData, error) {
	balance, err := s.balanceByHeight(acct, height)
	if err != nil {
		return nil, err
	}
	hash, err := s.HashForHeight(height)
	if err != nil {
		return nil, err
	}
	return &BalanceData{
		Balance: balance,
		Hash:    hash,
		Height:  height,
	}, nil
}

// BlockByHash returns the block data for the given hash.
func (s *Store) BlockByHash(hash []byte) (*BlockData, error) {
	height, err := s.HeightForHash(hash)
	if err != nil {
		return nil, err
	}
	block, err := s.blockByHeight(height)
	if err != nil {
		return nil, err
	}
	return s.withParentInfo(hash, height, &BlockData{
		Block:  block,
		Hash:   hash,
		Height: height,
	})
}

// BlockByHeight returns the block data for the given height.
func (s *Store) BlockByHeight(height uint64) (*BlockData, error) {
	hash, err := s.HashForHeight(height)
	if err != nil {
		return nil, err
	}
	block, err := s.blockByHeight(height)
	if err != nil {
		return nil, err
	}
	return s.withParentInfo(hash, height, &BlockData{
		Block:  block,
		Hash:   hash,
		Height: height,
	})
}

// ExportAccounts will export a map of the indexed accounts and their
// corresponding account info.
func (s *Store) ExportAccounts(filename string) {
	data, err := s.AccountsInfo()
	if err != nil {
		log.Fatalf("Failed to export accounts: %s", err)
	}
	enc, err := json.Marshal(data)
	if err != nil {
		log.Fatalf("Failed to encode data for export: %s", err)
	}
	err = os.WriteFile(filename, enc, 0o600)
	if err != nil {
		log.Fatalf("Failed to write to %s: %s", filename, err)
	}
}

// Genesis returns the stored genesis block metadata.
func (s *Store) Genesis() *model.BlockMeta {
	s.mu.RLock()
	genesis := s.genesis
	s.mu.RUnlock()
	if genesis != nil {
		return genesis.Clone()
	}
	genesis = &model.BlockMeta{}
	value, closer, err := s.db.Reader().Get([]byte("genesis"))
	if err != nil {
		if err != storage.ErrNotFound {
			log.Fatalf("Failed to get genesis block from the index database: %s", err)
		}
		return nil
	}
	defer closer.Close()
	err = proto.Unmarshal(value, genesis)
	if err != nil {
		log.Fatalf("Failed to unmarshal genesis block from the index database: %s", err)
	}
	s.mu.Lock()
	s.genesis = genesis
	s.mu.Unlock()
	return genesis.Clone()
}

// HasBalance returns whether the given account has an indexed balance at the
// given height.
func (s *Store) HasBalance(acct []byte, height uint64) (bool, error) {
	key := make([]byte, 1+8+8)
	key[0] = 'a'
	copy(key[1:9], acct)
	binary.BigEndian.PutUint64(key[9:], height)
	r := s.db.Reader()
	seeker := r.NewSeeker()
	_, err := seeker.SeekLE(key[:9], key)
	if err != nil {
		return false, fmt.Errorf(
			"indexdb: failed to check balance existence for account %x at height %d: %s",
			acct, height, err,
		)
	}
	return true, nil
}

// HashForHeight returns the block hash at the given height.
func (s *Store) HashForHeight(height uint64) ([]byte, error) {
	var hash []byte
	heightEnc := make([]byte, 8)
	binary.BigEndian.PutUint64(heightEnc, height)
	key := append([]byte("d"), heightEnc...)
	value, closer, err := s.db.Reader().Get(key)
	if err != nil {
		if err == storage.ErrNotFound {
			return nil, ErrBlockNotIndexed
		}
		return nil, fmt.Errorf(
			"indexdb: failed to get hash for height %d: %s", height, err,
		)
	}
	defer closer.Close()
	hash = make([]byte, len(value))
	copy(hash, value)
	return hash, nil
}

// HeightForHash returns the block height for the given hash.
func (s *Store) HeightForHash(hash []byte) (uint64, error) {
	key := append([]byte("c"), hash...)
	value, closer, err := s.db.Reader().Get(key)
	if err != nil {
		if err == storage.ErrNotFound {
			return 0, ErrBlockNotIndexed
		}
		return 0, fmt.Errorf(
			"indexdb: failed to get height for hash %x: %s", hash, err,
		)
	}
	defer closer.Close()
	return binary.BigEndian.Uint64(value), nil
}

// Index indexes the given block data.
func (s *Store) Index(ctx context.Context, height uint64, hash []byte, block *model.IndexedBlock) error {
	s.mu.RLock()
	latest := s.latest
	s.mu.RUnlock()
	if latest.Height >= height {
		log.Infof("Skipping re-indexing of previously indexed block at height %d", height)
		return nil
	}
	if height != latest.Height+1 {
		log.Fatalf(
			"Invalid index request for height %d (current height is %d)",
			height, latest.Height,
		)
	}
	_, span := trace.NewSpan(ctx, "flow.indexdb.Index")
	defer span.End()
	hval := make([]byte, 8)
	binary.BigEndian.PutUint64(hval, height)
	accts := map[string]int64{}
	proxyAccts := [][]byte{}
	for _, txn := range block.Transactions {
		for _, op := range txn.Operations {
			switch op.Type {
			case model.OperationType_FEE, model.OperationType_TRANSFER, model.OperationType_PROXY_TRANSFER:
				if len(op.Account) > 0 {
					accts[string(op.Account)] -= int64(op.Amount)
				}
				if len(op.Receiver) > 0 {
					accts[string(op.Receiver)] += int64(op.Amount)
				}
			case model.OperationType_CREATE_ACCOUNT:
				_, exists := accts[string(op.Account)]
				if !exists {
					accts[string(op.Account)] = 0
				}
				if len(op.ProxyPublicKey) > 0 {
					key := make([]byte, 17)
					key[0] = 'p'
					copy(key[1:], op.Account)
					binary.BigEndian.PutUint64(key[9:], height)
					proxyAccts = append(proxyAccts, key)
				}
			default:
				log.Fatalf(
					"Unknown transaction operation type: %d (%s)",
					op.Type, op.Type,
				)
			}
		}
	}
	i := 0
	updates := make([]accountUpdate, len(accts))
	for acct, diff := range accts {
		key := make([]byte, 1+8+8)
		key[0] = 'a'
		copy(key[1:], acct)
		copy(key[9:], hval)
		updates[i] = accountUpdate{
			diff: diff,
			key:  key,
		}
		i++
	}
	blockKey := append([]byte("b"), hval...)
	blockValue, err := proto.Marshal(block)
	if err != nil {
		log.Fatalf("Failed to encode model.IndexedBlock: %s", err)
	}
	hash2heightKey := append([]byte("c"), hash...)
	height2hashKey := append([]byte("d"), hval...)
	latest = &model.BlockMeta{
		Hash:      hash,
		Height:    height,
		Timestamp: block.Timestamp,
	}
	latestEnc, err := proto.Marshal(latest)
	if err != nil {
		log.Fatalf("Failed to encode model.BlockMeta: %s", err)
	}
	err = s.db.WithReaderBatchWriter(func(rbw storage.ReaderBatchWriter) error {
		for _, update := range updates {
			newVal := int64(update.diff)
			key, err := rbw.GlobalReader().NewSeeker().SeekLE(update.key[:9], update.key)
			if err == nil {
				value, closer, err := rbw.GlobalReader().Get(key)
				if err != nil {
				}
				existing := int64(binary.BigEndian.Uint64(value))
				closer.Close()
				newVal += existing
			}
			if newVal < 0 {
				return fmt.Errorf(
					"indexdb: invalid balance found for account %x: %d",
					update.key[1:9], newVal,
				)
			}
			balance := make([]byte, 8)
			binary.BigEndian.PutUint64(balance, uint64(newVal))
			if err := rbw.Writer().Set(update.key, balance); err != nil {
				return err
			}
		}
		for _, acct := range proxyAccts {
			if err := rbw.Writer().Set(acct, []byte("1")); err != nil {
				return err
			}
		}
		if err := rbw.Writer().Set(blockKey, blockValue); err != nil {
			return err
		}
		if err := rbw.Writer().Set(hash2heightKey, hval); err != nil {
			return err
		}
		if err := rbw.Writer().Set(height2hashKey, hash); err != nil {
			return err
		}
		if err := rbw.Writer().Set([]byte("latest"), latestEnc); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("indexdb: got error indexing block at height %d: %s", height, err)
	}
	s.mu.Lock()
	if latest.Height == s.latest.Height+1 {
		s.latest = latest
	}
	s.mu.Unlock()
	return nil
}

// Latest returns the most recently indexed block.
func (s *Store) Latest() *model.BlockMeta {
	s.mu.RLock()
	latest := s.latest
	s.mu.RUnlock()
	if latest != nil {
		return latest.Clone()
	}
	latest = &model.BlockMeta{}

	value, closer, err := s.db.Reader().Get([]byte("latest"))
	if err != nil {
		if err != storage.ErrNotFound {
			log.Fatalf("Failed to get latest block from the index database: %s", err)
		}
		return nil
	}
	defer closer.Close()
	err = proto.Unmarshal(value, latest)
	if err != nil {
		log.Fatalf("Failed to unmarshal latest block from the index database: %s", err)
	}
	s.mu.Lock()
	s.latest = latest
	s.mu.Unlock()
	return latest.Clone()
}

// PurgeProxyAccounts removes any indexed account balances or proxy account
// creation heights from the index database.
func (s *Store) PurgeProxyAccounts() {
	accts := map[string][]byte{}
	err := operation.IterateKeys(s.db.Reader(), []byte("p"), []byte("p"), operation.KeyOnlyIterateFunc(
		func(keyCopy []byte) error {
			accts[string(keyCopy[1:9])] = keyCopy
			return nil
		}), storage.IteratorOption{})
	if err != nil {
		log.Fatalf("Failed to iterate over proxy keys: %s", err)
	}
	accountBalanceKeys := [][]byte{}
	err = operation.IterateKeys(s.db.Reader(), []byte("a"), []byte("a"), operation.KeyOnlyIterateFunc(
		func(keyCopy []byte) error {
			_, isProxy := accts[string(keyCopy[1:9])]
			if isProxy {
				accountBalanceKeys = append(accountBalanceKeys, keyCopy)
			}
			return nil
		}), storage.IteratorOption{})
	if err != nil {
		log.Fatalf("Failed to iterate over account balance keys: %s", err)
	}
	err = s.db.WithReaderBatchWriter(func(rbw storage.ReaderBatchWriter) error {
		for _, key := range accts {
			if err := rbw.Writer().Delete(key); err != nil {
				return err
			}
		}
		for _, key := range accountBalanceKeys {
			if err := rbw.Writer().Delete(key); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		log.Fatalf("Failed to purge proxy accounts data: %s", err)
	}
}

// ResetTo drops all indexed data after the given height.
func (s *Store) ResetTo(base uint64) error {
	latest := s.Latest()
	if latest.Height <= base {
		return nil
	}
	delKeys := [][]byte{}
	err := operation.IterateKeys(s.db.Reader(), []byte("a"), []byte("a"), operation.KeyOnlyIterateFunc(
		func(keyCopy []byte) error {
			height := binary.BigEndian.Uint64(keyCopy[9:])
			if height > base {
				delKeys = append(delKeys, keyCopy)
			}
			return nil
		}), storage.IteratorOption{})
	if err != nil {
		return fmt.Errorf("indexdb: failed to get account balance keys to delete: %s", err)
	}
	err = operation.IterateKeys(s.db.Reader(), []byte("p"), []byte("p"), operation.KeyOnlyIterateFunc(
		func(keyCopy []byte) error {
			height := binary.BigEndian.Uint64(keyCopy[9:])
			if height > base {
				delKeys = append(delKeys, keyCopy)
			}
			return nil
		}), storage.IteratorOption{})
	if err != nil {
		return fmt.Errorf("indexdb: failed to get proxy account keys to delete: %s", err)
	}
	last := uint64(0)
	err = operation.IterateKeys(s.db.Reader(), []byte("d"), []byte("d"),
		func(keyCopy []byte, getValue func(destVal any) error) (bail bool, err error) {
			height := binary.BigEndian.Uint64(keyCopy[1:])
			if height > base {
				delKeys = append(delKeys, keyCopy)
				key := make([]byte, 9)
				key[0] = 'b'
				binary.BigEndian.PutUint64(key[1:], height)
				delKeys = append(delKeys, key)
				var hash []byte
				err := getValue(&hash)
				if err != nil {
					return true, err
				}
				key = make([]byte, len(hash)+1)
				key[0] = 'c'
				copy(key[1:], hash)
				delKeys = append(delKeys, key)
			} else {
				last = height
			}
			return false, nil
		}, storage.IteratorOption{})
	if err != nil {
		return fmt.Errorf("indexdb: failed to get keys to delete: %s", err)
	}
	sort.Slice(delKeys, func(i, j int) bool {
		return bytes.Compare(delKeys[i], delKeys[j]) == -1
	})
	err = s.db.WithReaderBatchWriter(func(rbw storage.ReaderBatchWriter) error {
		for _, key := range delKeys {
			if err := rbw.Writer().Delete(key); err != nil {
				return fmt.Errorf("indexdb: failed to delete keys: %s", err)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("indexdb: failed to flush the batched deletion: %s", err)
	}
	block, err := s.blockByHeight(last)
	if err != nil {
		return fmt.Errorf("indexdb: failed to get block at height %d: %s", last, err)
	}
	hash, err := s.HashForHeight(last)
	if err != nil {
		return fmt.Errorf("indexdb: failed to get block hash for height %d: %s", last, err)
	}
	latest = &model.BlockMeta{
		Hash:      hash,
		Height:    last,
		Timestamp: block.Timestamp,
	}
	latestEnc, err := proto.Marshal(latest)
	if err != nil {
		log.Fatalf("Failed to encode model.BlockMeta: %s", err)
	}
	err = s.db.WithReaderBatchWriter(func(rbw storage.ReaderBatchWriter) error {
		return rbw.Writer().Set([]byte("latest"), latestEnc)
	})
	if err != nil {
		return fmt.Errorf("indexdb: failed to set latest value: %s", err)
	}
	s.mu.Lock()
	s.latest = latest
	s.mu.Unlock()
	return nil
}

// SetGenesis sets the genesis block metadata and the initial latest block.
func (s *Store) SetGenesis(val *model.BlockMeta) error {
	val = val.Clone()
	genesis, err := proto.Marshal(val)
	if err != nil {
		return fmt.Errorf("indexdb: failed to encode genesis value: %s", err)
	}
	hval := make([]byte, 8)
	binary.BigEndian.PutUint64(hval, val.Height)
	blockKey := append([]byte("b"), hval...)
	blockValue, err := proto.Marshal(&model.IndexedBlock{
		Timestamp: val.Timestamp,
	})
	if err != nil {
		log.Fatalf("Failed to encode model.IndexedBlock: %s", err)
	}
	hash2heightKey := append([]byte("c"), val.Hash...)
	height2hashKey := append([]byte("d"), hval...)
	err = s.db.WithReaderBatchWriter(func(rbw storage.ReaderBatchWriter) error {
		if err := rbw.Writer().Set([]byte("genesis"), genesis); err != nil {
			return err
		}
		if err := rbw.Writer().Set(blockKey, blockValue); err != nil {
			return err
		}
		if err := rbw.Writer().Set(hash2heightKey, hval); err != nil {
			return err
		}
		if err := rbw.Writer().Set(height2hashKey, val.Hash); err != nil {
			return err
		}
		return rbw.Writer().Set([]byte("latest"), genesis)
	})
	if err != nil {
		return fmt.Errorf("indexdb: failed to set genesis value: %s", err)
	}
	s.mu.Lock()
	s.genesis = val
	s.latest = val
	s.mu.Unlock()
	return nil
}

func (s *Store) balanceByHeight(acct []byte, height uint64) (uint64, error) {
	balance := uint64(0)
	key := make([]byte, 1+8+8)
	key[0] = 'a'
	copy(key[1:9], acct)
	binary.BigEndian.PutUint64(key[9:], height)
	latestAsOfHeight, err := s.db.Reader().NewSeeker().SeekLE(key[:9], key)
	if err == nil {
		value, closer, err := s.db.Reader().Get(latestAsOfHeight)
		if err != nil {
			return 0, fmt.Errorf(
				"indexdb: failed to get balance for account %x at height %d: %s",
				acct, height, err,
			)
		}
		defer closer.Close()
		balance = binary.BigEndian.Uint64(value)
	}
	return balance, nil
}

func (s *Store) blockByHeight(height uint64) (*model.IndexedBlock, error) {
	block := &model.IndexedBlock{}
	key := make([]byte, 9)
	key[0] = 'b'
	binary.BigEndian.PutUint64(key[1:], height)
	value, closer, err := s.db.Reader().Get(key)
	if err != nil {
		if err == storage.ErrNotFound {
			return nil, ErrBlockNotIndexed
		}
		return nil, fmt.Errorf("indexdb: failed to get block at height %d: %s", height, err)
	}
	defer closer.Close()
	err = proto.Unmarshal(value, block)
	return block, err
}

func (s *Store) withParentInfo(hash []byte, height uint64, data *BlockData) (*BlockData, error) {
	var err error
	data.ParentHash = hash
	data.ParentHeight = height
	if height != s.genesis.Height {
		data.ParentHeight -= 1
		data.ParentHash, err = s.HashForHeight(data.ParentHeight)
		if err != nil {
			return nil, err
		}
	}
	return data, nil
}

type accountUpdate struct {
	diff int64
	key  []byte
}

// New opens the database at the given directory and returns the corresponding
// Store.
func New(dir string) *Store {
	dbLog := zerolog.With().Str("pebbledb", "index").Logger()
	db, err := pebble.SafeOpen(dbLog, dir)
	if err != nil {
		log.Fatalf("Failed to open the index database at %s: %s", dir, err)
	}
	process.SetExitHandler(func() {
		log.Infof("Closing the index database")
		if err := db.Close(); err != nil {
			log.Errorf("Got error closing the index database: %s", err)
		}
	})
	return &Store{
		db: pebbleimpl.ToDB(db),
	}
}
