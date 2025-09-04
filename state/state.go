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
	"strings"
	"sync"
	"time"

	flowcrypto "github.com/onflow/crypto"
	"github.com/onflow/flow-go/cmd/bootstrap/utils"
	hotstuff "github.com/onflow/flow-go/consensus/hotstuff/model"
	"github.com/onflow/flow-go/follower"
	"github.com/onflow/flow-go/model/flow"
	"github.com/onflow/flow-go/module/chainsync"
	"github.com/onflow/flow-go/module/compliance"
	"github.com/onflow/flow-go/storage"
	"github.com/onflow/flow-go/storage/operation"
	"github.com/onflow/flow-go/storage/operation/pebbleimpl"
	pebblestorage "github.com/onflow/flow-go/storage/pebble"
	"github.com/onflow/rosetta/cache"
	"github.com/onflow/rosetta/config"
	"github.com/onflow/rosetta/indexdb"
	"github.com/onflow/rosetta/log"
	"github.com/onflow/rosetta/model"
	"github.com/onflow/rosetta/process"
	"github.com/rs/zerolog"
	"golang.org/x/crypto/openpgp"
	"golang.org/x/crypto/openpgp/armor"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
	consensus           storage.DB
	feeAddr             []byte
	jobs                chan uint64
	lastIndexed         *model.BlockMeta
	liveRoot            *model.BlockMeta
	mu                  sync.RWMutex // protects lastIndexed and synced
	originators         map[string]bool
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
	if i.Chain.UseConsensusFollower {
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

func (i *Indexer) getVerifiedParent(rctx context.Context, hash []byte, height uint64) []byte {
	ctx := rctx
	initial := true
	skipCache := false
	skipCacheWarned := false
	spork := i.Chain.SporkFor(height)
	for {
		select {
		case <-rctx.Done():
			return nil
		default:
		}
		if initial {
			initial = false
		} else {
			time.Sleep(time.Second)
		}
		if skipCache {
			ctx = cache.Skip(rctx)
			if !skipCacheWarned {
				log.Warnf(
					"Skipping cache for getting verified parent for block %x at height %d",
					hash, height,
				)
				skipCacheWarned = true
			}
		} else {
			ctx = rctx
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
			skipCache = true
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
			skipCache = true
			continue
		}
		if !verifyBlockHash(spork, hash, height, hdr, block) {
			skipCache = true
			continue
		}
		for _, seal := range block.BlockSeals {
			i.sealedResults[string(seal.BlockId)] = string(seal.ResultId)
		}
		return block.ParentId
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

func (i *Indexer) indexLiveSpork(rctx context.Context, spork *config.Spork, lastIndexed *model.BlockMeta, startHeight uint64) {
	// We consider ourselves synced if we're within a minute of tip.
	const (
		syncWindow = time.Minute
	)
	blocks := map[string]uint64{}
	ctx := rctx
	height := startHeight
	parent := lastIndexed.Hash
	synced := false
	useConsensus := i.Chain.UseConsensusFollower
	// NOTE(tav): Since the root block of a live spork is self sealed, we avoid
	// trying to detect its seal within descendant blocks.
	if height == spork.RootBlock {
		i.processBlock(ctx, spork, height, i.liveRoot.Hash)
		height++
		parent = i.liveRoot.Hash
	}
	for {
		backoff := time.Second
		initial := true
		skipCache := false
		skipCacheWarned := false
	inner:
		for {
			select {
			case <-rctx.Done():
				return
			default:
			}
			if initial {
				initial = false
			} else {
				time.Sleep(backoff)
			}
			if skipCache {
				ctx = cache.Skip(rctx)
				if !skipCacheWarned {
					log.Warnf(
						"Skipping cache for retrieving block at height %d",
						height,
					)
					skipCacheWarned = true
				}
			} else {
				ctx = rctx
			}
			client := spork.AccessNodes.Client()
			hdr, err := client.BlockHeaderByHeight(ctx, height)
			if err != nil {
				switch status.Code(err) {
				case codes.NotFound:
					// NOTE(tav): Since Flow doesn't produce blocks at exact
					// intervals, it is possible for us to get NotFound errors
					// for a little while.
					if synced {
						time.Sleep(time.Second)
					} else {
						log.Errorf(
							"Failed to fetch header for block at height %d: %s",
							height, err,
						)
					}
				default:
					log.Errorf(
						"Failed to fetch header for block at height %d: %s",
						height, err,
					)
				}
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
				skipCache = true
				continue
			}
			if !verifyBlockHash(spork, block.Id, height, hdr, block) {
				skipCache = true
				continue
			}
			if useConsensus {
				blockID := flow.Identifier{}
				err := operation.LookupBlockHeight(i.consensus.Reader(), height, &blockID)
				if err != nil {
					log.Errorf(
						"Failed to get block ID for height %d from consensus: %s",
						height, err,
					)
					if err == storage.ErrNotFound {
						backoff = i.nextBackoff(backoff)
					}
					continue
				}
				if !bytes.Equal(blockID[:], block.Id) {
					log.Errorf(
						"Mismatching block ID found for height %d: %x from Access API, %x from consensus",
						height, block.Id, blockID[:],
					)
					skipCache = true
					continue
				}
			}
			log.Infof("Retrieved block %x at height %d", block.Id, block.Height)
			blocks[string(block.Id)] = height
			// NOTE(tav): We assume that block seals will only ever be seen in
			// block height order.
			for _, seal := range block.BlockSeals {
				blockHeight, ok := blocks[string(seal.BlockId)]
				if !ok {
					log.Warnf(
						"Skipping seal for block %x in block %x at height %d",
						seal.BlockId, block.Id, height,
					)
					continue
				}
				if useConsensus {
					blockID := flow.Identifier{}
					copy(blockID[:], seal.BlockId)
					sealID := flow.Identifier{}
					err := operation.LookupBySealedBlockID(i.consensus.Reader(), blockID, &sealID)
					if err != nil {
						log.Errorf(
							"Failed to get seal ID for block %x from consensus: %s",
							seal.BlockId, err,
						)
						continue inner
					}
					blockSeal := &flow.Seal{}
					err = operation.RetrieveSeal(i.consensus.Reader(), sealID, blockSeal)
					if err != nil {
						log.Errorf(
							"Failed to get seal %x for block %x from consensus: %s",
							sealID[:], seal.BlockId, err,
						)
						continue inner
					}
					if !bytes.Equal(seal.BlockId, blockSeal.BlockID[:]) {
						log.Errorf(
							"Unverifiable seal found in block %x at height %d: got seal for block %x via Acccess API, found seal for block %x via consensus",
							block.Id, height, seal.BlockId, blockSeal.BlockID[:],
						)
						continue inner
					}
					if !bytes.Equal(seal.ResultId, blockSeal.ResultID[:]) {
						log.Errorf(
							"Unverifiable execution result for block %x found in block %x at height %d: got %x via Acccess API, got %x via consensus",
							seal.BlockId, block.Id, height, seal.ResultId, blockSeal.ResultID[:],
						)
						continue inner
					}
				}
				i.sealedResults[string(seal.BlockId)] = string(seal.ResultId)
				i.processBlock(ctx, spork, blockHeight, seal.BlockId)
				delete(blocks, string(seal.BlockId))
			}
			height++
			parent = block.Id
			if time.Since(block.Timestamp.AsTime()) < syncWindow {
				if !synced {
					log.Infof("Indexer is close to tip")
					synced = true
					i.mu.Lock()
					i.synced = true
					i.mu.Unlock()
				}
			} else if synced {
				log.Errorf("Indexer is not close to tip")
				synced = false
				i.mu.Lock()
				i.synced = false
				i.mu.Unlock()
			}
			break
		}
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
		// NOTE(tav): If we've arrived at this state, it's effectively a fatal
		// error as we've already done our best to verify the chain of parent
		// block hashes — at least as much as Flow's data integrity support will
		// allow us.
		//
		// This will most likely only happen if we got corrupted block data when
		// we looked up the "genesis" block, i.e. the "root" block of the first
		// spork amongst the configured sporks.
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

func (i *Indexer) nextBackoff(d time.Duration) time.Duration {
	d *= 2
	if d > i.Chain.MaxBackoffInterval {
		d = i.Chain.MaxBackoffInterval
	}
	return d
}

func (i *Indexer) onBlockFinalized(f *hotstuff.Block) {
	log.Infof(
		"Got finalized block via consensus follower: %x (block timestamp: %s)",
		f.BlockID[:], time.Unix(int64(f.Timestamp), 0).UTC().Format(time.RFC3339),
	)
}

func (i *Indexer) runConsensusFollower(ctx context.Context) {
	spork := i.Chain.LatestSpork()
	sporkDir := i.Chain.PathFor(spork.String())
	i.downloadRootState(ctx, spork, sporkDir)
	dbDir := filepath.Join(sporkDir, "consensus")
	pebbleDB, err := pebblestorage.SafeOpen(NewPrefixedLogger("consensus"), dbDir)
	if err != nil {
		log.Fatalf("Failed to open consensus database at %s: %s", dbDir, err)
	}
	db := pebbleimpl.ToDB(pebbleDB)
	// Initialize a private key for joining the unstaked peer-to-peer network.
	// This can be ephemeral, so we generate a new one each time we start.
	seed := make([]byte, flowcrypto.KeyGenSeedMinLen)
	n, err := rand.Read(seed)
	if err != nil || n != flowcrypto.KeyGenSeedMinLen {
		log.Fatalf("Could not generate seed for the consensus follower private key")
	}
	key, err := utils.GeneratePublicNetworkingKey(seed)
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
		follower.WithComplianceConfig(&compliance.Config{
			SkipNewProposalsThreshold: 5 * compliance.MinSkipNewProposalsThreshold,
		}),
		follower.WithProtocolDB(db),
		follower.WithLogLevel("info"),
		follower.WithSyncCoreConfig(&chainsync.Config{
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
		block, err := client.LatestFinalizedBlockHeader(ctx)
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
				log.Warnf("Added job to prefetch block %d", height)
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

// NewPrefixedLogger creates a zerolog.Logger with a given prefix.
// It respects LOG_LEVEL and JSON_LOGS env vars like the zap setup.
func NewPrefixedLogger(prefix string) zerolog.Logger {
	// decide writer: console vs JSON
	var w zerolog.LevelWriter
	jsonLogs := true
	switch strings.ToLower(os.Getenv("JSON_LOGS")) {
	case "", "disable", "disabled", "false", "off", "0":
		jsonLogs = false
	}
	if jsonLogs {
		w = os.Stderr // JSON is default for zerolog
	} else {
		cw := zerolog.NewConsoleWriter(func(w *zerolog.ConsoleWriter) {
			w.Out = os.Stderr
			w.TimeFormat = zerolog.TimeFormatUnix
		})
		w = cw
	}

	// set level from LOG_LEVEL
	levelStr := strings.ToLower(os.Getenv("LOG_LEVEL"))
	if levelStr == "" {
		levelStr = "info"
	}
	level, err := zerolog.ParseLevel(levelStr)
	if err != nil {
		level = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(level)

	// build logger with prefix and timestamp
	return zerolog.New(w).With().
		Timestamp().
		Str("prefix", prefix).
		Logger()
}
