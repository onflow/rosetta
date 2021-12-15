// Package config supports configuration of the Flow Rosetta server.
package config

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.cbhq.net/nodes/rosetta-flow/access"
	"github.cbhq.net/nodes/rosetta-flow/cache"
	"github.cbhq.net/nodes/rosetta-flow/log"
	"github.com/onflow/flow-go/model/flow"
)

// Chain represents the definition of a Flow chain like mainnet and testnet.
type Chain struct {
	Cache             bool
	Contracts         *Contracts
	DataDir           string
	LatestAccessNodes access.Pool
	Mode              string
	Network           string
	Originators       []string
	Port              uint16
	ResyncFrom        uint64
	SkipBlocks        map[flow.Identifier]struct{}
	Sporks            []*Spork
	Workers           uint
}

// IsProxyContractDeployed returns whether a deployed Proxy contract has been
// configured.
func (c *Chain) IsProxyContractDeployed() bool {
	return c.Contracts.FlowColdStorageProxy != "0000000000000000"
}

// FirstSpork returns the config for the first spork in the chain.
func (c *Chain) FirstSpork() *Spork {
	return c.Sporks[0]
}

// LatestSpork returns the config for the latest spork in the chain.
func (c *Chain) LatestSpork() *Spork {
	return c.Sporks[len(c.Sporks)-1]
}

// PathFor joins the given subpath elements with the chain's data directory.
func (c *Chain) PathFor(subpath ...string) string {
	return filepath.Join(append([]string{c.DataDir}, subpath...)...)
}

// SporkFor returns the spork for the given height.
func (c *Chain) SporkFor(height uint64) *Spork {
	cur := c.Sporks[0]
	if height < cur.RootBlock {
		return nil
	}
	for {
		next := cur.Next
		if next == nil || height < next.RootBlock {
			return cur
		}
		cur = next
	}
}

// Contracts defines the chain-specific addresses for various core contracts.
type Contracts struct {
	FlowFees             string `json:"flow_fees"`
	FlowToken            string `json:"flow_token"`
	FungibleToken        string `json:"fungible_token"`
	FlowColdStorageProxy string `json:"flow_cold_storage_proxy"`
}

// Consensus defines the metadata needed to initialize a consensus follower for
// a live spork.
type Consensus struct {
	DisableSignatureCheck         bool       `json:"disable_signature_check"`
	RootProtocolStateURL          string     `json:"root_protocol_state_url"`
	RootProtocolStateSignatureURL string     `json:"root_protocol_state_signature_url"`
	SeedNodes                     []SeedNode `json:"seed_nodes"`
	SigningKey                    string     `json:"signing_key"`
}

// SeedNode defines a seed node for the current spork.
type SeedNode struct {
	Host      string `json:"host"`
	Port      uint16 `json:"port"`
	PublicKey string `json:"public_key"`
}

// Spork provides the relevant metadata for a spork.
type Spork struct {
	AccessNodes access.Pool
	Chain       *Chain
	Consensus   *Consensus
	ID          uint
	Next        *Spork
	Prev        *Spork
	RootBlock   uint64
	Version     int
}

func (s *Spork) String() string {
	return fmt.Sprintf("%s-%d", s.Chain.Network, s.ID)
}

// Init reads the given config file, decodes the JSON config, validates it, and
// returns the initialized config.
func Init(ctx context.Context, filename string) *Chain {
	type SporkConfig struct {
		AccessNodes []access.NodeConfig `json:"access_nodes"`
		Consensus   *Consensus          `json:"consensus"`
		RootBlock   uint64              `json:"root_block"`
		Version     int                 `json:"version"`
	}
	type ChainConfig struct {
		Cache       bool                  `json:"cache"`
		Contracts   *Contracts            `json:"contracts"`
		DataDir     string                `json:"data_dir"`
		Mode        string                `json:"mode"`
		Network     string                `json:"network"`
		Originators []string              `json:"originators"`
		Port        uint16                `json:"port"`
		ResyncFrom  uint64                `json:"resync_from"`
		SkipBlocks  []string              `json:"skip_blocks"`
		Sporks      map[uint]*SporkConfig `json:"sporks"`
		Workers     uint                  `json:"workers"`
	}
	/* #nosec G304 -- We want to read this file based on command line input */
	data, err := os.ReadFile(filename)
	if err != nil {
		log.Fatalf("Failed to read config file at %q: %s", filename, err)
	}
	src := &ChainConfig{}
	err = json.Unmarshal(data, src)
	if err != nil {
		log.Fatalf("Failed to decode config file at %q: %s", filename, err)
	}
	useCache := os.Getenv("CACHE")
	if useCache != "" {
		src.Cache = true
	}
	if src.Contracts == nil {
		log.Fatalf("Missing .contracts value in %s", filename)
	}
	if src.Contracts.FlowColdStorageProxy == "" {
		log.Fatalf("Missing .contracts.flow_cold_storage_proxy value in %s", filename)
	}
	if src.Contracts.FlowFees == "" {
		log.Fatalf("Missing .contracts.flow_fees value in %s", filename)
	}
	if src.Contracts.FlowToken == "" {
		log.Fatalf("Missing .contracts.flow_token value in %s", filename)
	}
	if src.Contracts.FungibleToken == "" {
		log.Fatalf("Missing .contracts.fungible_token value in %s", filename)
	}
	if src.DataDir == "" {
		log.Fatalf("Missing .data_dir value in %s", filename)
	}
	dataDir := os.Getenv("DATA_DIR")
	if dataDir != "" {
		src.DataDir = dataDir
	}
	ensureDir(src.DataDir)
	mode := os.Getenv("MODE")
	if mode != "" {
		src.Mode = mode
	}
	switch src.Mode {
	case "online", "offline":
	case "":
		src.Mode = "online"
	default:
		if mode == "" {
			log.Fatalf("Invalid config value for .mode in %s: %q", filename, src.Mode)
		} else {
			log.Fatalf("Invalid config value for $MODE: %q", mode)
		}
	}
	if src.Network == "" {
		log.Fatalf("Missing .network value in %s", filename)
	}
	if !(src.Network == "mainnet" || src.Network == "testnet") {
		log.Fatalf(
			"Invalid config value for .network in %s: %q",
			filename, src.Network,
		)
	}
	if src.Port == 0 {
		log.Fatalf("Missing .port value in %s", filename)
	}
	originLoc := filename
	originEnv := os.Getenv("ORIGINATORS")
	if originEnv != "" {
		originLoc = "$ORIGINATORS"
		src.Originators = strings.Split(originEnv, ",")
	}
	originators := make([]string, len(src.Originators))
	for i, raw := range src.Originators {
		addr, err := hex.DecodeString(raw)
		if err != nil {
			log.Fatalf(
				"Invalid originator address found in %s: %q: %s",
				originLoc, raw, err,
			)
		}
		if len(addr) != 8 {
			log.Fatalf(
				"Invalid originator address found in %s: %q: addresses must be 8 bytes long",
				originLoc, raw,
			)
		}
		originators[i] = string(addr)
	}
	resync := os.Getenv("RESYNC_FROM")
	if resync != "" {
		src.ResyncFrom, err = strconv.ParseUint(resync, 10, 64)
		if err != nil {
			log.Fatalf("Invalid $RESYNC_FROM value: %s", resync)
		}
	}
	skipLoc := ".skip_blocks in " + filename
	skipEnv := os.Getenv("SKIP_BLOCKS")
	if skipEnv != "" {
		skipLoc = "$SKIP_BLOCKS"
		src.SkipBlocks = strings.Split(skipEnv, ",")
	}
	skip := map[flow.Identifier]struct{}{}
	for _, raw := range src.SkipBlocks {
		block, err := flow.HexStringToIdentifier(raw)
		if err != nil {
			log.Fatalf(
				"Invalid block hash found in %s: %q: %s",
				skipLoc, raw, err,
			)
		}
		skip[block] = struct{}{}
	}
	if len(src.Sporks) < 1 {
		log.Fatalf("The .sporks value in %s must contain at least 1 spork", filename)
	}
	if src.Workers == 0 {
		src.Workers = uint(runtime.NumCPU() * 2)
	}
	dst := &Chain{
		Cache:       src.Cache,
		Contracts:   src.Contracts,
		DataDir:     src.DataDir,
		Mode:        src.Mode,
		Network:     src.Network,
		Originators: originators,
		Port:        src.Port,
		ResyncFrom:  src.ResyncFrom,
		SkipBlocks:  skip,
		Workers:     src.Workers,
	}
	sporksEnv := os.Getenv("SPORKS")
	sporksLoc := filename
	if sporksEnv != "" {
		sporksLoc = "$SPORKS"
		sporks := map[uint]*SporkConfig{}
		err := json.Unmarshal([]byte(sporksEnv), &sporks)
		if err != nil {
			log.Fatalf("Failed to decode the $SPORKS value: %s", err)
		}
		src.Sporks = sporks
	}
	// NOTE(tav): We provide support for overriding spork config for individual
	// sporks via a $NEW_SPORKS environment variable, as it's possible for us to
	// eventually hit the 32KB limit for environment variable data.
	newSporks := map[uint]*SporkConfig{}
	newSporksEnv := os.Getenv("NEW_SPORKS")
	if newSporksEnv != "" {
		err := json.Unmarshal([]byte(newSporksEnv), &newSporks)
		if err != nil {
			log.Fatalf("Failed to decode the $NEW_SPORKS value: %s", err)
		}
		for id, val := range newSporks {
			src.Sporks[id] = val
		}
	}
	sporkIDs := []uint{}
	for id := range src.Sporks {
		sporkIDs = append(sporkIDs, id)
	}
	sort.Slice(sporkIDs, func(i, j int) bool {
		return sporkIDs[i] < sporkIDs[j]
	})
	var prev *Spork
	for i, id := range sporkIDs {
		cfg := src.Sporks[id]
		loc := sporksLoc
		if _, ok := newSporks[id]; ok {
			loc = "$NEW_SPORKS"
		}
		if len(cfg.AccessNodes) == 0 {
			log.Fatalf(
				"Missing .access_nodes definition for %s-%d in %s",
				src.Network, id, loc,
			)
		}
		spork := &Spork{
			Chain:     dst,
			Consensus: cfg.Consensus,
			ID:        id,
			Prev:      prev,
			RootBlock: cfg.RootBlock,
			Version:   cfg.Version,
		}
		if spork.Version < 1 || spork.Version > 2 {
			log.Fatalf(
				"Invalid .version value for %s-%d in %s",
				src.Network, id, loc,
			)
		}
		var store *cache.Store
		if dst.Cache {
			store = cache.New(dst.PathFor(spork.String() + "-cache"))
		}
		spork.AccessNodes = access.New(ctx, cfg.AccessNodes, store)
		if i == len(sporkIDs)-1 {
			dst.LatestAccessNodes = access.New(ctx, cfg.AccessNodes, nil)
		}
		if prev != nil {
			prev.Next = spork
			if id != prev.ID+1 {
				log.Fatalf(
					"Missing .sporks definition for %s-%d in %s",
					src.Network, prev.ID+1, loc,
				)
			}
			if cfg.RootBlock <= prev.RootBlock {
				log.Fatalf(
					"The .root_block for %s-%d is not greater than %s-%d in %s",
					src.Network, id, src.Network, prev.ID, loc,
				)
			}
		}
		prev = spork
		dst.Sporks = append(dst.Sporks, spork)
	}
	latest := dst.Sporks[len(dst.Sporks)-1]
	cfg := latest.Consensus
	if cfg == nil {
		log.Fatalf(
			"Missing .consensus definition for %s-%d",
			src.Network, latest.ID,
		)
		// NOTE(tav): We do the following just to satisfy the linter.
		os.Exit(1)
	}
	if cfg.RootProtocolStateURL == "" {
		log.Fatalf(
			"Missing .consensus.root_protocol_state_url definition for %s-%d",
			src.Network, latest.ID,
		)
	}
	if cfg.RootProtocolStateSignatureURL == "" && !cfg.DisableSignatureCheck {
		log.Fatalf(
			"Missing .consensus.root_protocol_state_signature_url definition for %s-%d",
			src.Network, latest.ID,
		)
	}
	if len(cfg.SeedNodes) == 0 {
		log.Fatalf(
			"Missing .consensus.seed_nodes definition for %s-%d",
			src.Network, latest.ID,
		)
	}
	for i, node := range cfg.SeedNodes {
		if node.Host == "" {
			log.Fatalf(
				"Missing .consensus.seed_nodes[%d].host definition for %s-%d",
				i, src.Network, latest.ID,
			)
		}
		if node.Port == 0 {
			log.Fatalf(
				"Missing .consensus.seed_nodes[%d].port definition for %s-%d",
				i, src.Network, latest.ID,
			)
		}
		if node.PublicKey == "" {
			log.Fatalf(
				"Missing .consensus.seed_nodes[%d].public_key definition for %s-%d",
				i, src.Network, latest.ID,
			)
		}
	}
	if cfg.SigningKey == "" {
		log.Fatalf(
			"Missing .consensus.signing_key definition for %s-%d",
			src.Network, latest.ID,
		)
	}
	return dst
}

func ensureDir(path string) {
	stat, err := os.Stat(path)
	if err == nil {
		if stat.IsDir() {
			return
		}
		log.Fatalf("Path exists at %q and is not a directory", path)
	}
	if !os.IsNotExist(err) {
		log.Fatalf("Failed to stat directory %q: %s", path, err)
	}
	if err := os.MkdirAll(path, 0o744); err != nil {
		log.Fatalf("Failed to create %q: %s", path, err)
	}
}
