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

	"github.cbhq.net/nodes/rosetta-flow/cache"
	"github.cbhq.net/nodes/rosetta-flow/config"
	"github.cbhq.net/nodes/rosetta-flow/deque"
	"github.cbhq.net/nodes/rosetta-flow/indexdb"
	"github.cbhq.net/nodes/rosetta-flow/log"
	"github.cbhq.net/nodes/rosetta-flow/model"
	"github.cbhq.net/nodes/rosetta-flow/process"
	"github.com/dgraph-io/badger/v2"
	"github.com/onflow/flow-go/cmd/bootstrap/utils"
	hotstuff "github.com/onflow/flow-go/consensus/hotstuff/model"
	flowcrypto "github.com/onflow/flow-go/crypto"
	"github.com/onflow/flow-go/follower"
	"github.com/onflow/flow-go/model/flow"
	"github.com/onflow/flow-go/module/synchronization"
	"github.com/onflow/flow-go/storage/badger/operation"
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
	i.handleResyncFrom()
	i.initState()
	i.initWorkers(ctx)
	i.runConsensusFollower(ctx)
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

func (i *Indexer) findGenesis(ctx context.Context) *model.BlockMeta {
	// NOTE(tav): We're blindly trusting the data from the Access API here for
	// the block metadata of the "genesis" block. Since we try to establish a
	// chain to this block from authenticated sources, this shouldn't be an
	// issue. Worst case, we'll be serving the wrong genesis block hash and
	// won't ever index any other blocks. And a node that's not indexing
	// anything should hopefully be evident pretty quickly.
	spork := i.Chain.FirstSpork()
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

func (i *Indexer) getVerifiedParent(ctx context.Context, hash []byte, height uint64, update bool) []byte {
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
		hdr, err := client.BlockHeaderByHash(ctx, hash)
		if err != nil {
			log.Errorf(
				"Failed to fetch header for block %x at height %d: %s",
				hash, height, err,
			)
			continue
		}
		block, err := client.BlockByHash(ctx, hash)
		if err != nil {
			log.Errorf(
				"Failed to fetch block %x at height %d: %s",
				hash, height, err,
			)
			continue
		}
		if !verifyBlockHash(spork, hash, height, hdr, block) {
			continue
		}
		if update {
			for _, seal := range block.BlockSeals {
				i.sealedResults[string(seal.BlockId)] = string(seal.ResultId)
			}
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
		next := i.lastIndexedHeight() + 1
		spork := i.Chain.SporkFor(next)
		if spork.Next == nil {
			i.indexLiveSpork(ctx, spork, next)
			return
		}
		i.indexPastSporks(ctx, next)
	}
}

func (i *Indexer) indexLiveSpork(ctx context.Context, spork *config.Spork, startHeight uint64) {
	const blockInterval = 3 * time.Second
	delay := false
outer:
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if delay {
			time.Sleep(time.Second)
		} else {
			delay = true
		}
		blockID := i.queue.Pop()
		if blockID == flow.ZeroID {
			log.Warnf("No new block finalized (retrying after %s)", blockInterval)
			time.Sleep(blockInterval)
			delay = false
			continue
		}
		hdr, seals := i.getConsensusData(ctx, blockID, false)
		if hdr == nil {
			return
		}
		if hdr.Height < startHeight {
			log.Warnf(
				"Skipping finalized block at height %d that predates the live spork start height %d",
				hdr.Height, startHeight,
			)
			delay = false
			continue
		}
		// TODO(tav): We currently keep going until we find a finalized block
		// that has seals in it.
		if len(seals) == 0 {
			delay = false
			continue
		}
		target := seals[len(seals)-1]
		targetHdr, _ := i.getConsensusData(ctx, target, true)
		if targetHdr == nil {
			return
		}
		// NOTE(tav): As a sanity check, we make sure that there is an ancestry
		// chain between the finalized and sealed blocks.
		count := 0
		for {
			if hdr.ParentID == target {
				break
			}
			hdr, _ = i.getConsensusData(ctx, hdr.ParentID, true)
			if hdr == nil {
				return
			}
			count++
			if count%1000 == 0 {
				log.Warnf(
					"There is a gap of over %d blocks between the most recently finalized block and the target sealed block",
					count,
				)
				time.Sleep(time.Second)
			}
		}
		i.mu.Lock()
		i.synced = false
		i.mu.Unlock()
		for height := startHeight; height <= targetHdr.Height; height++ {
			select {
			case <-ctx.Done():
				return
			default:
			}
			blockID := flow.Identifier{}
			err := i.consensus.View(operation.LookupBlockHeight(height, &blockID))
			if err != nil {
				log.Errorf(
					"Failed to retrieve block for sealed block at height %d: %s",
					height, err,
				)
				continue outer
			}
			parent := i.getVerifiedParent(ctx, blockID[:], height, false)
			if parent == nil {
				return
			}
			resultID := flow.Identifier{}
			err = i.consensus.View(operation.LookupExecutionResult(blockID, &resultID))
			if err != nil {
				log.Errorf(
					"Failed to retrieve execution result for sealed block %x at height %d: %s",
					blockID[:], height, err,
				)
				continue outer
			}
			i.sealedResults[string(blockID[:])] = string(resultID[:])
			i.processBlock(ctx, spork, height, blockID[:])
		}
		i.mu.Lock()
		i.synced = true
		startHeight = targetHdr.Height + 1
		i.mu.Unlock()
	}
}

func (i *Indexer) indexPastSporks(ctx context.Context, startHeight uint64) {
	var parent []byte
	hashes := [][]byte{}
	for height := i.root.Head.Height; height >= startHeight; height-- {
		if len(hashes) == 0 {
			hash, err := hex.DecodeString(i.root.LatestSeal.BlockID)
			if err != nil {
				log.Fatalf(
					"Unable to decode the sealed block hash in the root protocol state snapshot: %s",
					err,
				)
			}
			parent, err = hex.DecodeString(i.root.Head.ParentID)
			if err != nil {
				log.Fatalf(
					"Unable to decode the parent hash in the root protocol state snapshot: %s",
					err,
				)
			}
			if len(parent) != 32 {
				log.Fatalf(
					"Invalid parent hash found in the root protocol state snapshot: %s",
					i.root.Head.ParentID,
				)
			}
			hashes = append(hashes, hash)
			continue
		}
		hash := parent
		parent = i.getVerifiedParent(ctx, hash, height, true)
		if parent == nil {
			return
		}
		hashes = append(hashes, hash)
		log.Infof("Retrieved block %x at height %d", hash, height)
	}
	i.mu.RLock()
	lastIndexed := i.lastIndexed.Clone()
	i.mu.RUnlock()
	if !bytes.Equal(lastIndexed.Hash, parent) {
		log.Errorf(
			"Unable to establish a hash chain from the root protocol state snapshot to last indexed block %x at height %d: found %x as parent instead",
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
		return true
	}
	genesis = i.findGenesis(ctx)
	if genesis == nil {
		return false
	}
	if err := i.Store.SetGenesis(genesis); err != nil {
		log.Fatalf("Couldn't set the genesis block on the index database: %s", err)
	}
	log.Infof("Genesis block set to block %x at height %d", genesis.Hash, genesis.Height)
	i.lastIndexed = genesis
	return true
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
	isProxy, ok := i.accts[string(addr[:])]
	if ok {
		return isProxy
	}
	return newAccounts[string(addr)]
}

func (i *Indexer) isTracked(addr []byte, newAccounts map[string]bool) bool {
	_, ok := i.accts[string(addr[:])]
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

func (i *Indexer) onBlockFinalized(id flow.Identifier) {
	log.Infof("New block finalized: %x", id[:])
	i.queue.Push(id)
}

func (i *Indexer) onBlockFinalizedV2(f *hotstuff.Block) {
	log.Infof("New block finalized: %x", f.BlockID[:])
	i.queue.Push(f.BlockID)
}

func (i *Indexer) runConsensusFollower(ctx context.Context) {
	spork := i.Chain.LatestSpork()
	sporkDir := i.Chain.PathFor(spork.String())
	i.downloadRootState(ctx, spork, sporkDir)
	// TODO(tav): Do we need to wipe any existing data in the DB?
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
		follower.WithSyncCoreConfig(&synchronization.Config{
			MaxAttempts:   5,
			MaxRequests:   5,
			MaxSize:       64,
			RetryInterval: 4 * time.Second,
			Tolerance:     10,
		}),
	)
	if err != nil {
		log.Fatalf("Failed to create the consensus follower: %s", err)
	}
	i.consensus = db
	// process.SetExitHandler(func() {
	// 	log.Infof("Closing the consensus database")
	// 	if err := db.Close(); err != nil {
	// 		log.Errorf("Got error closing the consensus database: %s", err)
	// 	}
	// })
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
			if height%1000 == 0 {
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
