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

	"github.com/onflow/rosetta/log"
	"github.com/onflow/rosetta/model"
	"github.com/onflow/rosetta/process"
	"github.com/onflow/rosetta/trace"
	"github.com/dgraph-io/badger/v3"
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
//           headerKey h<block-hash> = model.BlockHeader
//          isProxyKey p<acct><height-big-endian> = 1
//                     genesis = model.BlockMeta
//                     latest = model.BlockMeta

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
	db      *badger.DB
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
	err := s.db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.IteratorOptions{})
		defer it.Close()
		prefix := []byte("a")
		it.Seek(prefix)
		for {
			if !it.ValidForPrefix(prefix) {
				break
			}
			key := it.Item().Key()
			acct := [8]byte{}
			copy(acct[:], key[1:9])
			accts[acct] = false
			it.Next()
		}
		it = txn.NewIterator(badger.IteratorOptions{})
		defer it.Close()
		prefix = []byte("p")
		it.Seek(prefix)
		for {
			if !it.ValidForPrefix(prefix) {
				break
			}
			key := it.Item().Key()
			acct := [8]byte{}
			copy(acct[:], key[1:9])
			if _, ok := accts[acct]; !ok {
				return fmt.Errorf(
					"indexdb: found proxy account %x that doesn't have an account balance",
					acct,
				)
			}
			accts[acct] = true
			it.Next()
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("indexdb: failed to get all accounts: %s", err)
	}
	return accts, nil
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

// ExportAccounts will export the accounts stored within the index database, and
// the block numbers of any balance-changing transactions.
func (s *Store) ExportAccounts(filename string) {
	type AccountInfo struct {
		Blocks  []uint64 `json:"blocks"`
		IsProxy bool     `json:"is_proxy"`
	}
	data := map[string]*AccountInfo{}
	err := s.db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.IteratorOptions{})
		defer it.Close()
		prefix := []byte("a")
		it.Seek(prefix)
		for {
			if !it.ValidForPrefix(prefix) {
				break
			}
			key := it.Item().Key()
			acct := hex.EncodeToString(key[1:9])
			info, ok := data[acct]
			if !ok {
				info = &AccountInfo{}
				data[acct] = info
			}
			info.Blocks = append(info.Blocks, binary.BigEndian.Uint64(key[9:]))
			it.Next()
		}
		return nil
	})
	if err != nil {
		log.Fatalf("Failed to export accounts: %s", err)
	}
	err = s.db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.IteratorOptions{})
		defer it.Close()
		prefix := []byte("p")
		it.Seek(prefix)
		for {
			if !it.ValidForPrefix(prefix) {
				break
			}
			key := it.Item().Key()
			acct := hex.EncodeToString(key[1:9])
			info, ok := data[acct]
			if !ok {
				return fmt.Errorf(
					"indexdb: failed to find account balance for proxy account 0x%s",
					acct,
				)
			}
			info.IsProxy = true
			it.Next()
		}
		return nil
	})
	if err != nil {
		log.Fatalf("Failed to process proxy accounts during export: %s", err)
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
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte("genesis"))
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			return proto.Unmarshal(val, genesis)
		})
	})
	if err != nil {
		if err != badger.ErrKeyNotFound {
			log.Fatalf("Failed to get genesis block from the index database: %s", err)
		}
		return nil
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
	ok := false
	err := s.db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.IteratorOptions{
			Reverse: true,
		})
		defer it.Close()
		it.Seek(key)
		if it.ValidForPrefix(key[:9]) {
			ok = true
		}
		return nil
	})
	if err != nil {
		return false, fmt.Errorf(
			"indexdb: failed to check balance existence for account %x at height %d: %s",
			acct, height, err,
		)
	}
	return ok, nil
}

// HashForHeight returns the block hash at the given height.
func (s *Store) HashForHeight(height uint64) ([]byte, error) {
	var hash []byte
	heightEnc := make([]byte, 8)
	binary.BigEndian.PutUint64(heightEnc, height)
	key := append([]byte("d"), heightEnc...)
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(key)
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			hash = make([]byte, len(val))
			copy(hash, val)
			return nil
		})
	})
	if err != nil {
		if err == badger.ErrKeyNotFound {
			return nil, ErrBlockNotIndexed
		}
		return nil, fmt.Errorf(
			"indexdb: failed to get hash for height %d: %s", height, err,
		)
	}
	return hash, nil
}

// HeightForHash returns the block height for the given hash.
func (s *Store) HeightForHash(hash []byte) (uint64, error) {
	height := uint64(0)
	key := append([]byte("c"), hash...)
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(key)
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			height = binary.BigEndian.Uint64(val)
			return nil
		})
	})
	if err != nil {
		if err == badger.ErrKeyNotFound {
			return 0, ErrBlockNotIndexed
		}
		return 0, fmt.Errorf(
			"indexdb: failed to get height for hash %x: %s", hash, err,
		)
	}
	return height, nil
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
	err = s.db.Update(func(txn *badger.Txn) error {
		for _, update := range updates {
			it := txn.NewIterator(badger.IteratorOptions{
				Reverse: true,
			})
			defer it.Close()
			cur := int64(0)
			it.Seek(update.key)
			if it.ValidForPrefix(update.key[:9]) {
				item := it.Item()
				if bytes.Equal(item.Key(), update.key) {
					it.Next()
					if it.ValidForPrefix(update.key[:9]) {
						err := item.Value(func(val []byte) error {
							cur = int64(binary.BigEndian.Uint64(val))
							return nil
						})
						if err != nil {
							return err
						}
					}
				} else {
					err := item.Value(func(val []byte) error {
						cur = int64(binary.BigEndian.Uint64(val))
						return nil
					})
					if err != nil {
						return err
					}
				}
			}
			val := cur + update.diff
			if val < 0 {
				return fmt.Errorf(
					"indexdb: invalid balance found for account %x: %d",
					update.key[1:9], val,
				)
			}
			balance := make([]byte, 8)
			binary.BigEndian.PutUint64(balance, uint64(val))
			if err := txn.Set(update.key, balance); err != nil {
				return err
			}
		}
		for _, acct := range proxyAccts {
			if err := txn.Set(acct, []byte("1")); err != nil {
				return err
			}
		}
		if err := txn.Set(blockKey, blockValue); err != nil {
			return err
		}
		if err := txn.Set(hash2heightKey, hval); err != nil {
			return err
		}
		if err := txn.Set(height2hashKey, hash); err != nil {
			return err
		}
		if err := txn.Set([]byte("latest"), latestEnc); err != nil {
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
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte("latest"))
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			return proto.Unmarshal(val, latest)
		})
	})
	if err != nil {
		if err != badger.ErrKeyNotFound {
			log.Fatalf("Failed to get latest block from the index database: %s", err)
		}
		return nil
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
	err := s.db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.IteratorOptions{})
		defer it.Close()
		prefix := []byte("p")
		it.Seek(prefix)
		for {
			if !it.ValidForPrefix(prefix) {
				break
			}
			key := it.Item().KeyCopy(nil)
			accts[string(key[1:9])] = key
			it.Next()
		}
		return nil
	})
	if err != nil {
		log.Fatalf("Failed to iterate over proxy keys: %s", err)
	}
	accountBalanceKeys := [][]byte{}
	err = s.db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.IteratorOptions{})
		defer it.Close()
		prefix := []byte("a")
		it.Seek(prefix)
		for {
			if !it.ValidForPrefix(prefix) {
				break
			}
			key := it.Item().KeyCopy(nil)
			_, isProxy := accts[string(key[1:9])]
			if isProxy {
				accountBalanceKeys = append(accountBalanceKeys, key)
			}
			it.Next()
		}
		return nil
	})
	if err != nil {
		log.Fatalf("Failed to iterate over account balance keys: %s", err)
	}
	err = s.db.Update(func(txn *badger.Txn) error {
		for _, key := range accts {
			if err := txn.Delete(key); err != nil {
				return err
			}
		}
		for _, key := range accountBalanceKeys {
			if err := txn.Delete(key); err != nil {
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
	err := s.db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.IteratorOptions{})
		prefix := []byte("a")
		it.Seek(prefix)
		for {
			if !it.ValidForPrefix(prefix) {
				break
			}
			item := it.Item()
			key := item.Key()
			height := binary.BigEndian.Uint64(key[9:])
			if height > base {
				delKeys = append(delKeys, item.KeyCopy(nil))
			}
			it.Next()
		}
		it.Close()
		return nil
	})
	if err != nil {
		return fmt.Errorf("indexdb: failed to get account balance keys to delete: %s", err)
	}
	err = s.db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.IteratorOptions{})
		prefix := []byte("p")
		it.Seek(prefix)
		for {
			if !it.ValidForPrefix(prefix) {
				break
			}
			item := it.Item()
			key := item.Key()
			height := binary.BigEndian.Uint64(key[9:])
			if height > base {
				delKeys = append(delKeys, item.KeyCopy(nil))
			}
			it.Next()
		}
		it.Close()
		return nil
	})
	if err != nil {
		return fmt.Errorf("indexdb: failed to get proxy account keys to delete: %s", err)
	}
	last := uint64(0)
	err = s.db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.IteratorOptions{})
		prefix := []byte("d")
		it.Seek(prefix)
		for {
			if !it.ValidForPrefix(prefix) {
				break
			}
			item := it.Item()
			key := item.Key()
			height := binary.BigEndian.Uint64(key[1:])
			if height > base {
				key = item.KeyCopy(nil)
				delKeys = append(delKeys, key)
				key = make([]byte, 9)
				key[0] = 'b'
				binary.BigEndian.PutUint64(key[1:], height)
				delKeys = append(delKeys, key)
				hash, err := item.ValueCopy(nil)
				if err != nil {
					it.Close()
					return err
				}
				key = make([]byte, len(hash)+1)
				key[0] = 'c'
				copy(key[1:], hash)
				delKeys = append(delKeys, key)
				key = make([]byte, len(hash)+1)
				key[0] = 'h'
				copy(key[1:], hash)
				delKeys = append(delKeys, key)
			} else {
				last = height
			}
			it.Next()
		}
		it.Close()
		return nil
	})
	if err != nil {
		return fmt.Errorf("indexdb: failed to get keys to delete: %s", err)
	}
	sort.Slice(delKeys, func(i, j int) bool {
		return bytes.Compare(delKeys[i], delKeys[j]) == -1
	})
	batch := s.db.NewWriteBatch()
	batch.SetMaxPendingTxns(1024)
	for _, key := range delKeys {
		err := batch.Delete(key)
		if err != nil {
			batch.Cancel()
			return fmt.Errorf("indexdb: failed to delete keys: %s", err)
		}
	}
	err = batch.Flush()
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
	err = s.db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte("latest"), latestEnc)
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
	err = s.db.Update(func(txn *badger.Txn) error {
		if err := txn.Set([]byte("genesis"), genesis); err != nil {
			return err
		}
		if err := txn.Set(blockKey, blockValue); err != nil {
			return err
		}
		if err := txn.Set(hash2heightKey, hval); err != nil {
			return err
		}
		if err := txn.Set(height2hashKey, val.Hash); err != nil {
			return err
		}
		return txn.Set([]byte("latest"), genesis)
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
	err := s.db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.IteratorOptions{
			Reverse: true,
		})
		defer it.Close()
		it.Seek(key)
		if !it.ValidForPrefix(key[:9]) {
			return nil
		}
		return it.Item().Value(func(val []byte) error {
			balance = binary.BigEndian.Uint64(val)
			return nil
		})
	})
	if err != nil {
		return 0, fmt.Errorf(
			"indexdb: failed to get balance for account %x at height %d: %s",
			acct, height, err,
		)
	}
	return balance, nil
}

func (s *Store) blockByHeight(height uint64) (*model.IndexedBlock, error) {
	block := &model.IndexedBlock{}
	key := make([]byte, 9)
	key[0] = 'b'
	binary.BigEndian.PutUint64(key[1:], height)
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(key)
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			return proto.Unmarshal(val, block)
		})
	})
	if err == badger.ErrKeyNotFound {
		return nil, ErrBlockNotIndexed
	}
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
	opts := badger.DefaultOptions(dir).WithLogger(log.Badger{Prefix: "index"})
	db, err := badger.Open(opts)
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
		db: db,
	}
}
