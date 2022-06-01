// Package state implements the machinery for tracking on-chain state.
package state

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/dgraph-io/badger/v2"
	"github.com/onflow/flow-go/cmd/bootstrap/utils"
	hotstuff "github.com/onflow/flow-go/consensus/hotstuff/model"
	flowcrypto "github.com/onflow/flow-go/crypto"
	"github.com/onflow/flow-go/follower"
	"github.com/onflow/flow-go/model/flow"
	"github.com/onflow/flow-go/storage/badger/operation"
	"github.com/onflow/rosetta/cache"
	"github.com/onflow/rosetta/config"
	"github.com/onflow/rosetta/deque"
	"github.com/onflow/rosetta/indexdb"
	"github.com/onflow/rosetta/log"
	"github.com/onflow/rosetta/model"
	"github.com/onflow/rosetta/process"
	"golang.org/x/crypto/openpgp"
	"golang.org/x/crypto/openpgp/armor"
)

const (
	debug = false
)

var (
	httpClient = &http.Client{
		Timeout: 5 * time.Minute,
	}
)

// Indexer indexes block data using the Flow Access API and Consensus Follower.
type Indexer struct {
	Chain               *config.Chain
	Store               *indexdb.Store
	accts               map[string]bool
	consensus           *badger.DB
	feeAddr             []byte
	jobs                chan uint64
	lastIndexed         *model.BlockMeta
	liveRoot            *model.BlockMeta
	mu                  sync.RWMutex // protects lastIndexed and synced
	originators         map[string]bool
	queue               *deque.Queue
	root                *stateSnapshot
	sealedResults       map[string]string
	synced              bool
	typProxyCreated     string
	typProxyDeposited   string
	typProxyTransferred string
	typTokensDeposited  string
	typTokensWithdrawn  string
}

// Run kicks off the entire state tracking machinery.
func (i *Indexer) Run(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	process.SetExitHandler(cancel)
	if !i.initStore(ctx) {
		return
	}
	i.handlePurgeProxyAccounts()
	i.handleResyncFrom()
	i.initState()
	i.initWorkers(ctx)
	if i.Chain.UseConsensusFollwer {
		i.runConsensusFollower(ctx)
	}
	go i.indexBlocks(ctx)
}

// Synced returns whether the indexer is synced with the tip of the live spork.
func (i *Indexer) Synced() bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.synced
}

func (i *Indexer) downloadRootState(ctx context.Context, spork *config.Spork, sporkDir string) {
	bootstrapDir := filepath.Join(sporkDir, "public-root-information")
	err := os.MkdirAll(bootstrapDir, 0o744)
	if err != nil {
		log.Fatalf("Failed to create the spork bootstrap directory: %s", err)
	}
	data := download(ctx, spork.Consensus.RootProtocolStateURL)
	dst := filepath.Join(bootstrapDir, "root-protocol-state-snapshot.json")
	err = os.WriteFile(dst, data, 0o600)
	if err != nil {
		log.Fatalf("Failed to write to %s: %s", dst, err)
	}
	i.root = &stateSnapshot{}
	err = json.Unmarshal(data, i.root)
	if err != nil {
		log.Fatalf("Failed to decode root protocol state snapshot: %s", err)
	}
	if spork.Consensus.DisableSignatureCheck {
		return
	}
	signer := bytes.NewReader([]byte(spork.Consensus.SigningKey))
	keyring, err := openpgp.ReadArmoredKeyRing(signer)
	if err != nil {
		log.Fatalf("Failed to read PGP keyring from the configured signing_key: %s", err)
	}
	sig := download(ctx, spork.Consensus.RootProtocolStateSignatureURL)
	block, err := armor.Decode(bytes.NewReader(sig))
	if err != nil {
		log.Fatalf(
			"Failed to decode PGP signature block from %s: %s",
			spork.Consensus.RootProtocolStateSignatureURL, err,
		)
	}
	if block.Type != openpgp.SignatureType {
		log.Fatalf("Failed to get PGP signature block: got %q instead", block.Type)
	}
	_, err = openpgp.CheckDetachedSignature(
		keyring, bytes.NewReader(data), block.Body,
	)
	if err != nil {
		log.Fatalf("Failed to get valid PGP signature for the root protocol state: %s", err)
	}
}

func (i *Indexer) findRootBlock(ctx context.Context, spork *config.Spork) *model.BlockMeta {
	// NOTE(tav): We're blindly trusting the data from the Access API here for
	// the block metadata of the "genesis" block. Since we try to establish a
	// chain to this block from authenticated sources, this shouldn't be an
	// issue. Worst case, we'll be serving the wrong genesis block hash and
	// won't ever index any other blocks. And a node that's not indexing
	// anything should hopefully be evident pretty quickly.
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		client := spork.AccessNodes.Client()
		block, err := client.BlockByHeight(ctx, spork.RootBlock)
		if err != nil {
			log.Errorf(
				"Failed to fetch the %s root block: %s",
				spork, err,
			)
			time.Sleep(time.Second)
			continue
		}
		if block.Height != spork.RootBlock {
			log.Errorf(
				"Unexpected block height (%d) when fetching the root block of %s at height %d",
				block.Height, spork, spork.RootBlock,
			)
			time.Sleep(time.Second)
			continue
		}
		return &model.BlockMeta{
			Hash:      block.Id,
			Height:    block.Height,
			Parent:    block.ParentId,
			Timestamp: uint64(block.Timestamp.AsTime().UnixNano()),
		}
	}
}

func (i *Indexer) getVerifiedParent(ctx context.Context, hash []byte, height uint64) []byte {
	initial := true
	spork := i.Chain.SporkFor(height)
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		if initial {
			initial = false
		} else {
			time.Sleep(time.Second)
		}
		client := spork.AccessNodes.Client()
		hdr, err := client.BlockHeaderByHeight(ctx, height)
		if err != nil {
			log.Errorf(
				"Failed to fetch header for block %x at height %d: %s",
				hash, height, err,
			)
			continue
		}
		if !bytes.Equal(hdr.Id, hash) {
			log.Errorf(
				"Got unexpected header value for block at height %d: expected %x, got %x",
				height, hash, hdr.Id,
			)
			continue
		}
		block, err := client.BlockByHeight(ctx, height)
		if err != nil {
			log.Errorf(
				"Failed to fetch block %x at height %d: %s",
				hash, height, err,
			)
			continue
		}
		if !bytes.Equal(block.Id, hash) {
			log.Errorf(
				"Got unexpected block value at height %d: expected %x, got %x",
				height, hash, block.Id,
			)
			continue
		}
		if !verifyBlockHash(spork, hash, height, hdr, block) {
			continue
		}
		for _, seal := range block.BlockSeals {
			i.sealedResults[string(seal.BlockId)] = string(seal.ResultId)
		}
		return block.ParentId
	}
}

func (i *Indexer) getConsensusData(ctx context.Context, blockID flow.Identifier, hdrOnly bool) (*flow.Header, []flow.Identifier) {
	hash := blockID[:]
	initial := true
outer:
	for {
		select {
		case <-ctx.Done():
			return nil, nil
		default:
		}
		if initial {
			initial = false
		} else {
			time.Sleep(time.Second)
		}
		hdr := &flow.Header{}
		err := i.consensus.View(operation.RetrieveHeader(blockID, hdr))
		if err != nil {
			log.Errorf(
				"Failed to get header for block %x from consensus: %s",
				hash, err,
			)
			continue
		}
		if hdrOnly {
			return hdr, nil
		}
		payloadSeals := []flow.Identifier{}
		err = i.consensus.View(operation.LookupPayloadSeals(blockID, &payloadSeals))
		if err != nil {
			log.Errorf(
				"Failed to get payload seals for block %x from consensus: %s",
				hash, err,
			)
			continue
		}
		seals := []flow.Identifier{}
		for _, sealID := range payloadSeals {
			seal := &flow.Seal{}
			err = i.consensus.View(operation.RetrieveSeal(sealID, seal))
			if err != nil {
				log.Errorf(
					"Failed to get seal %x from block %x from consensus: %s",
					sealID[:], hash, err,
				)
				continue outer
			}
			seals = append(seals, seal.BlockID)
		}
		return hdr, seals
	}
}

func (i *Indexer) handlePurgeProxyAccounts() {
	if i.Chain.PurgeProxyAccounts {
		i.Store.PurgeProxyAccounts()
	}
}

func (i *Indexer) handleResyncFrom() {
	height := i.Chain.ResyncFrom
	if height == 0 {
		return
	}
	genesis := i.Store.Genesis().Height
	if height < genesis {
		log.Fatalf(
			"The resync_from value (%d) cannot be less than the height of the genesis block (%d)",
			height, genesis,
		)
	}
	err := i.Store.ResetTo(height)
	if err != nil {
		log.Fatalf("Failed to reset data for resync_from: %s", err)
	}
	i.lastIndexed = i.Store.Latest()
	log.Infof(
		"Successfully reset indexed data to block %x at height %d",
		i.lastIndexed.Hash, i.lastIndexed.Height,
	)
}

func (i *Indexer) indexBlocks(ctx context.Context) {
	ctx = cache.Context(ctx, "indexer")
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		i.mu.RLock()
		lastIndexed := i.lastIndexed.Clone()
		i.mu.RUnlock()
		startHeight := lastIndexed.Height + 1
		spork := i.Chain.SporkFor(startHeight)
		if spork.Next == nil {
			i.indexLiveSpork(ctx, spork, lastIndexed, startHeight)
			return
		}
		i.indexPastSporks(ctx, lastIndexed, startHeight)
	}
}

func (i *Indexer) indexLiveSpork(ctx context.Context, spork *config.Spork, lastIndexed *model.BlockMeta, startHeight uint64) {
	const maxBlocks = 601
	initial := true
outer:
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if initial {
			initial = false
		} else {
			time.Sleep(time.Second)
		}
		client := spork.AccessNodes.Client()
		latest, err := client.LatestBlockHeader(ctx)
		if err != nil {
			log.Errorf("Failed to fetch latest sealed block: %s", err)
			continue
		}
		if latest.Height < startHeight {
			log.Errorf(
				"Height of latest sealed block (%d) is less than last indexed height (%d)",
				latest.Height, startHeight,
			)
			continue
		}
		if latest.Height == startHeight {
			continue
		}
		i.mu.Lock()
		i.synced = false
		i.mu.Unlock()
		sealCount := 0
		sealMap := map[string]*blockSeal{}
		seals := []*blockSeal{}
		parent := lastIndexed.Hash
		for height := startHeight; height <= latest.Height; height++ {
			initial := true
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}
				if initial {
					initial = false
				} else {
					time.Sleep(time.Second)
				}
				client := spork.AccessNodes.Client()
				hdr, err := client.BlockHeaderByHeight(ctx, height)
				if err != nil {
					log.Errorf(
						"Failed to fetch header for block at height %d: %s",
						height, err,
					)
					continue
				}
				block, err := client.BlockByHeight(ctx, height)
				if err != nil {
					log.Errorf("Failed to fetch block at height %d: %s", height, err)
					continue
				}
				if !bytes.Equal(block.ParentId, parent) {
					log.Errorf(
						"Unexpected parent hash found for block %x at height %d: expected %x, got %x",
						block.Id, height, parent, block.ParentId,
					)
					continue outer
				}
				if !verifyBlockHash(spork, block.Id, height, hdr, block) {
					continue
				}
				seal := &blockSeal{
					hash:   block.Id,
					height: height,
				}
				sealMap[string(block.Id)] = seal
				seals = append(seals, seal)
				for _, src := range block.BlockSeals {
					seal, ok := sealMap[string(src.BlockId)]
					if !ok {
						continue
					}
					seal.resultID = src.ResultId
					sealCount++
				}
				parent = block.Id
				log.Infof("Retrieved block %x at height %d", block.Id, block.Height)
				break
			}
			if sealCount > maxBlocks {
				break
			}
		}
		for _, seal := range seals {
			if len(seal.resultID) > 0 || seal.height == spork.RootBlock {
				i.sealedResults[string(seal.hash)] = string(seal.resultID)
				i.processBlock(ctx, spork, seal.height, seal.hash)
			} else {
				break
			}
		}
		i.mu.Lock()
		lastIndexed = i.lastIndexed.Clone()
		if latest.Height-lastIndexed.Height < i.Chain.SporkSyncedTolerance {
			i.synced = true
		}
		i.mu.Unlock()
		startHeight = lastIndexed.Height + 1
	}
}

func (i *Indexer) indexPastSporks(ctx context.Context, lastIndexed *model.BlockMeta, startHeight uint64) {
	var parent []byte
	hashes := [][]byte{}
	for height := i.liveRoot.Height; height >= startHeight; height-- {
		if len(hashes) == 0 {
			parent = i.liveRoot.Parent
			hashes = append(hashes, i.liveRoot.Hash)
			continue
		}
		hash := parent
		parent = i.getVerifiedParent(ctx, hash, height)
		if parent == nil {
			return
		}
		hashes = append(hashes, hash)
		log.Infof("Retrieved block %x at height %d", hash, height)
	}
	if !bytes.Equal(lastIndexed.Hash, parent) {
		log.Errorf(
			"Unable to establish a hash chain from live spork root block to last indexed block %x at height %d: found %x as parent instead",
			lastIndexed.Hash, lastIndexed.Height, parent,
		)
		return
	}
	height := startHeight
	for j := len(hashes) - 1; j >= 0; j-- {
		select {
		case <-ctx.Done():
			return
		default:
		}
		spork := i.Chain.SporkFor(height)
		i.processBlock(ctx, spork, height, hashes[j])
		height++
	}
}

func (i *Indexer) initState() {
	accts, err := i.Store.Accounts()
	if err != nil {
		log.Fatalf("Failed to load accounts from the index database: %s", err)
	}
	i.accts = map[string]bool{}
	for acct, isProxy := range accts {
		i.accts[string(acct[:])] = isProxy
	}
	i.feeAddr, err = hex.DecodeString(i.Chain.Contracts.FlowFees)
	if err != nil {
		log.Fatalf(
			"Invalid FlowFees contract address %q: %s",
			i.Chain.Contracts.FlowFees, err,
		)
	}
	i.originators = map[string]bool{}
	for _, addr := range i.Chain.Originators {
		i.originators[string(addr)] = true
	}
	i.queue = deque.New(1024)
	i.sealedResults = map[string]string{}
	i.typProxyCreated = fmt.Sprintf("A.%s.FlowColdStorageProxy.Created", i.Chain.Contracts.FlowColdStorageProxy)
	i.typProxyDeposited = fmt.Sprintf("A.%s.FlowColdStorageProxy.Deposited", i.Chain.Contracts.FlowColdStorageProxy)
	i.typProxyTransferred = fmt.Sprintf("A.%s.FlowColdStorageProxy.Transferred", i.Chain.Contracts.FlowColdStorageProxy)
	i.typTokensDeposited = fmt.Sprintf("A.%s.FlowToken.TokensDeposited", i.Chain.Contracts.FlowToken)
	i.typTokensWithdrawn = fmt.Sprintf("A.%s.FlowToken.TokensWithdrawn", i.Chain.Contracts.FlowToken)
}

func (i *Indexer) initStore(ctx context.Context) bool {
	genesis := i.Store.Genesis()
	if genesis != nil {
		i.lastIndexed = i.Store.Latest()
		i.liveRoot = i.findRootBlock(ctx, i.Chain.LatestSpork())
		return i.liveRoot != nil
	}
	genesis = i.findRootBlock(ctx, i.Chain.FirstSpork())
	if genesis == nil {
		return false
	}
	if err := i.Store.SetGenesis(genesis); err != nil {
		log.Fatalf("Couldn't set the genesis block on the index database: %s", err)
	}
	log.Infof("Genesis block set to block %x at height %d", genesis.Hash, genesis.Height)
	i.lastIndexed = genesis
	i.liveRoot = i.findRootBlock(ctx, i.Chain.LatestSpork())
	return i.liveRoot != nil
}

func (i *Indexer) initWorkers(ctx context.Context) {
	if !i.Chain.Cache {
		return
	}
	i.jobs = make(chan uint64, i.Chain.Workers)
	for j := 0; j < int(i.Chain.Workers); j++ {
		go i.runWorker(ctx, j)
	}
	go i.scheduleJobs(ctx, i.lastIndexedHeight())
}

func (i *Indexer) isProxy(addr []byte, newAccounts map[string]bool) bool {
	isProxy, ok := i.accts[string(addr)]
	if ok {
		return isProxy
	}
	return newAccounts[string(addr)]
}

func (i *Indexer) isTracked(addr []byte, newAccounts map[string]bool) bool {
	_, ok := i.accts[string(addr)]
	if ok {
		return true
	}
	_, ok = newAccounts[string(addr)]
	if ok {
		return true
	}
	return i.originators[string(addr)]
}

func (i *Indexer) lastIndexedHeight() uint64 {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.lastIndexed.Height
}

func (i *Indexer) onBlockFinalized(f *hotstuff.Block) {
	log.Infof("New block finalized: %x", f.BlockID[:])
	i.queue.Push(f.BlockID)
}

func (i *Indexer) runConsensusFollower(ctx context.Context) {
	spork := i.Chain.LatestSpork()
	sporkDir := i.Chain.PathFor(spork.String())
	i.downloadRootState(ctx, spork, sporkDir)
	dbDir := filepath.Join(sporkDir, "consensus")
	opts := badger.DefaultOptions(dbDir).WithLogger(log.Badger{Prefix: "consensus"})
	db, err := badger.Open(opts)
	if err != nil {
		log.Fatalf("Failed to open consensus database at %s: %s", dbDir, err)
	}
	// Initialize a private key for joining the unstaked peer-to-peer network.
	// This can be ephemeral, so we generate a new one each time we start.
	seed := make([]byte, flowcrypto.KeyGenSeedMinLenECDSASecp256k1)
	n, err := rand.Read(seed)
	if err != nil || n != flowcrypto.KeyGenSeedMinLenECDSASecp256k1 {
		log.Fatalf("Could not generate seed for the consensus follower private key")
	}
	key, err := utils.GenerateUnstakedNetworkingKey(seed)
	if err != nil {
		log.Fatalf("Could not generate the consensus follower private key")
	}
	nodes := []follower.BootstrapNodeInfo{}
	for _, node := range spork.Consensus.SeedNodes {
		rawkey, err := hex.DecodeString(node.PublicKey)
		if err != nil {
			log.Fatalf("Failed to hex decode the seed node key %q: %s", key, err)
		}
		pubkey, err := flowcrypto.DecodePublicKey(flowcrypto.ECDSAP256, rawkey)
		if err != nil {
			log.Fatalf("Failed to decode the seed node key %q: %s", key, err)
		}
		nodes = append(nodes, follower.BootstrapNodeInfo{
			Host:             node.Host,
			Port:             uint(node.Port),
			NetworkPublicKey: pubkey,
		})
	}
	follow, err := follower.NewConsensusFollower(
		key,
		"0.0.0.0:8040",
		nodes,
		follower.WithBootstrapDir(sporkDir),
		follower.WithDB(db),
		follower.WithLogLevel("info"),
		// NOTE(siva): Enable sync config once it has been merged upstream.
		// follower.WithSyncCoreConfig(&synchronization.Config{
		// 	MaxAttempts:   5,
		// 	MaxRequests:   5,
		// 	MaxSize:       64,
		// 	RetryInterval: 4 * time.Second,
		// 	Tolerance:     10,
		// }),
	)
	if err != nil {
		log.Fatalf("Failed to create the consensus follower: %s", err)
	}
	i.consensus = db
	ctx, cancel := context.WithCancel(ctx)
	process.SetExitHandler(cancel)
	follow.AddOnBlockFinalizedConsumer(i.onBlockFinalized)
	go follow.Run(ctx)
}

func (i *Indexer) scheduleJobs(ctx context.Context, startHeight uint64) {
	pool := i.Chain.LatestSpork().AccessNodes
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		client := pool.Client()
		block, err := client.LatestBlockHeader(ctx)
		if err != nil {
			log.Errorf("Failed to fetch latest sealed block: %s", err)
			time.Sleep(time.Second)
			continue
		}
		if startHeight >= block.Height {
			time.Sleep(time.Second)
			continue
		}
		// NOTE(tav): If the Access API server is compromised, this could
		// potentially be used to make the cache ineffective, e.g. by returning
		// a height long way into the future.
		for height := startHeight + 1; height <= block.Height; height++ {
			i.jobs <- height
			if height%200 == 0 {
				log.Infof("Added job to prefetch block %d", height)
			}
		}
		startHeight = block.Height
	}
}

func download(ctx context.Context, src string) []byte {
	for {
		req, err := http.NewRequestWithContext(ctx, "GET", src, nil)
		if err != nil {
			log.Fatalf("Failed to create HTTP request to %s: %s", src, err)
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			log.Errorf("Failed to download %s: %s", src, err)
			time.Sleep(time.Second)
			continue
		}
		data, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			log.Errorf("Failed to read data from %s: %s", src, err)
			time.Sleep(time.Second)
			continue
		}
		return data
	}
}

type blockSeal struct {
	hash     []byte
	height   uint64
	resultID []byte
}

type stateSnapshot struct {
	Head       stateSnapshotHeader
	LatestSeal stateSnapshotSeal
}

type stateSnapshotHeader struct {
	ChainID  string
	Height   uint64
	ParentID string
}

type stateSnapshotSeal struct {
	BlockID string
}
