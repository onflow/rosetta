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
	"time"

	"github.com/onflow/flow-go/model/flow"
	"github.com/onflow/rosetta/access"
	"github.com/onflow/rosetta/cache"
	"github.com/onflow/rosetta/log"
)

// Chain represents the definition of a Flow chain like mainnet and testnet.
type Chain struct {
	BalanceValidationInterval time.Duration
	Cache                     bool
	ConstructionAccessNodes   access.Pool
	DataAccessNodes           access.Pool
	Contracts                 *Contracts
	DataDir                   string
	MaxBackoffInterval        time.Duration
	Mode                      string
	Network                   string
	Originators               []string
	Port                      uint16
	PurgeProxyAccounts        bool
	ResyncFrom                uint64
	SkipBlocks                map[flow.Identifier]struct{}
	SporkSealTolerance        uint64
	Sporks                    []*Spork
	UseConsensusFollwer       bool
	Workers                   uint
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
	src := &chainConfig{}
	src.readConfigJSON(filename)
	result := &Chain{}

	src.parseAndValidateMode(result)
	src.parseAndValidateNetwork(result)
	src.parseAndValidatePort(result)
	src.parseAndValidateContracts(result)

	if result.Mode == "offline" {
		return result
	}

	result.Cache = src.Cache
	result.PurgeProxyAccounts = src.PurgeProxyAccounts
	src.parseAndValidateBalanceValidationInterval(result)
	src.parseAndValidateDataDir(result)
	src.parseAndValidateMaxBackoffInterval(result)
	src.parseAndValidateOriginators(result)
	src.parseAndValidateConstructionAccessNodes(ctx, result)
	src.parseAndValidateResyncFrom(result)
	src.parseAndValidateSkipBlocks(result)
	src.parseAndValidateSporkSealTolerance(result)
	src.parseAndValidateWorkers(result)
	src.parseAndValidateSporks(ctx, result)
	src.parseAndValidateDisableConsensusFollower(result)
	return result
}

type sporkConfig struct {
	AccessNodes []access.NodeConfig `json:"access_nodes"`
	Consensus   *Consensus          `json:"consensus"`
	RootBlock   uint64              `json:"root_block"`
	Version     int                 `json:"version"`
}

type chainConfig struct {
	filename                  string
	BalanceValidationInterval string                `json:"balance_validation_interval"`
	Cache                     bool                  `json:"cache"`
	ConstructionAccessNodes   []access.NodeConfig   `json:"construction_access_nodes"`
	Contracts                 *Contracts            `json:"contracts"`
	DataDir                   string                `json:"data_dir"`
	DisableConsensusFollower  bool                  `json:"disable_consensus_follower"`
	DropCache                 bool                  `json:"drop_cache"`
	MaxBackoffInterval        string                `json:"max_backoff_interval"`
	Mode                      string                `json:"mode"`
	Network                   string                `json:"network"`
	Originators               []string              `json:"originators"`
	Port                      uint16                `json:"port"`
	PurgeProxyAccounts        bool                  `json:"purge_proxy_accounts"`
	ResyncFrom                uint64                `json:"resync_from"`
	SkipBlocks                []string              `json:"skip_blocks"`
	SporkSealTolerance        uint64                `json:"spork_seal_tolerance"`
	Sporks                    map[uint]*sporkConfig `json:"sporks"`
	Workers                   uint                  `json:"workers"`
}

func (c *chainConfig) readConfigJSON(filename string) {
	data, err := os.ReadFile(filepath.Clean(filename))
	if err != nil {
		log.Fatalf("Failed to read config file at %q: %s", filename, err)
	}
	err = json.Unmarshal(data, c)
	if err != nil {
		log.Fatalf("Failed to decode config file at %q: %s", filename, err)
	}
	c.filename = filename
}

func (c *chainConfig) parseAndValidateMode(result *Chain) {
	result.Mode = c.Mode
	switch result.Mode {
	case "online", "offline":
	case "":
		result.Mode = "online"
	default:
		log.Fatalf("Invalid config value for .mode in %s: %q", c.filename, c.Mode)
	}
}

func (c *chainConfig) parseAndValidateNetwork(result *Chain) {
	if c.Network == "" {
		log.Fatalf("Missing .network value in %s", c.filename)
	}
	if !(c.Network == "mainnet" || c.Network == "testnet" || c.Network == "canary") {
		log.Fatalf(
			"Invalid config value for .network in %s: %q",
			c.filename, c.Network,
		)
	}
	result.Network = c.Network
}

func (c *chainConfig) parseAndValidatePort(result *Chain) {
	result.Port = c.Port
	port := os.Getenv("PORT")
	if port != "" {
		portVal, err := strconv.ParseUint(port, 10, 16)
		if err != nil {
			log.Fatalf(
				"Invalid $PORT value: %q (%s)",
				port, err,
			)
		}
		result.Port = uint16(portVal)
	}
	if result.Port == 0 {
		log.Fatalf("Missing .port value in %s", c.filename)
	}
}

func (c *chainConfig) parseAndValidateContracts(result *Chain) {
	if c.Contracts == nil {
		log.Fatalf("Missing .contracts value in %s", c.filename)
	}
	if c.Contracts.FlowColdStorageProxy == "" {
		log.Fatalf("Missing .contracts.flow_cold_storage_proxy value in %s", c.filename)
	}
	if c.Contracts.FlowFees == "" {
		log.Fatalf("Missing .contracts.flow_fees value in %s", c.filename)
	}
	if c.Contracts.FlowToken == "" {
		log.Fatalf("Missing .contracts.flow_token value in %s", c.filename)
	}
	if c.Contracts.FungibleToken == "" {
		log.Fatalf("Missing .contracts.fungible_token value in %s", c.filename)
	}
	result.Contracts = c.Contracts
}

func (c *chainConfig) parseAndValidateBalanceValidationInterval(result *Chain) {
	if c.BalanceValidationInterval == "" {
		result.BalanceValidationInterval = time.Second
		return
	}
	d, err := time.ParseDuration(c.BalanceValidationInterval)
	if err != nil {
		log.Fatalf(
			"Failed to parse .balance_validation_interval value in %s: %s",
			c.filename, err,
		)
	}
	result.BalanceValidationInterval = d
}

func (c *chainConfig) parseAndValidateDataDir(result *Chain) {
	result.DataDir = c.DataDir
	if c.DataDir == "" {
		log.Fatalf("Missing .data_dir value in %s", c.filename)
	}
	stat, err := os.Stat(c.DataDir)
	if err == nil {
		if stat.IsDir() {
			return
		}
		log.Fatalf("Path exists at %q and is not a directory", c.DataDir)
	}
	if !os.IsNotExist(err) {
		log.Fatalf("Failed to stat directory %q: %s", c.DataDir, err)
	}
	if err := os.MkdirAll(c.DataDir, 0o744); err != nil {
		log.Fatalf("Failed to create %q: %s", c.DataDir, err)
	}
}

func (c *chainConfig) parseAndValidateMaxBackoffInterval(result *Chain) {
	if c.MaxBackoffInterval == "" {
		result.MaxBackoffInterval = 10 * time.Second
		return
	}
	d, err := time.ParseDuration(c.MaxBackoffInterval)
	if err != nil {
		log.Fatalf(
			"Failed to parse .max_backoff_interval value in %s: %s",
			c.filename, err,
		)
	}
	result.MaxBackoffInterval = d
}

func (c *chainConfig) parseAndValidateOriginators(result *Chain) {
	originators := make([]string, len(c.Originators))
	for i, raw := range c.Originators {
		addr, err := hex.DecodeString(raw)
		if err != nil {
			log.Fatalf(
				"Invalid originator address found in %s: %q: %s",
				c.filename, raw, err,
			)
		}
		if len(addr) != 8 {
			log.Fatalf(
				"Invalid originator address found in %s: %q: addresses must be 8 bytes long",
				c.filename, raw,
			)
		}
		originators[i] = string(addr)
	}
	result.Originators = originators
}

func (c *chainConfig) parseAndValidateResyncFrom(result *Chain) {
	result.ResyncFrom = c.ResyncFrom
}

func (c *chainConfig) parseAndValidateSkipBlocks(result *Chain) {
	skip := map[flow.Identifier]struct{}{}
	for _, raw := range c.SkipBlocks {
		block, err := flow.HexStringToIdentifier(raw)
		if err != nil {
			log.Fatalf(
				"Invalid block hash found in %s: %q: %s",
				c.filename, raw, err,
			)
		}
		skip[block] = struct{}{}
	}
	result.SkipBlocks = skip
}

func (c *chainConfig) parseAndValidateSporkSealTolerance(result *Chain) {
	result.SporkSealTolerance = c.SporkSealTolerance
}

func (c *chainConfig) parseAndValidateWorkers(result *Chain) {
	if c.Workers == 0 {
		c.Workers = uint(runtime.NumCPU() * 2)
	}
	result.Workers = c.Workers
}

func (c *chainConfig) parseAndValidateConstructionAccessNodes(ctx context.Context, result *Chain) {
	if len(c.ConstructionAccessNodes) == 0 {
		log.Fatalf(
			"Missing .construction_access_nodes value in %s",
			c.filename,
		)
	}
	result.ConstructionAccessNodes = access.New(ctx, c.ConstructionAccessNodes, nil)
}

func (c *chainConfig) parseAndValidateSporks(ctx context.Context, result *Chain) {
	if len(c.Sporks) < 1 {
		log.Fatalf("The .sporks value in %s must contain at least 1 spork", c.filename)
	}
	sporkIDs := []uint{}
	for id := range c.Sporks {
		sporkIDs = append(sporkIDs, id)
	}
	sort.Slice(sporkIDs, func(i, j int) bool {
		return sporkIDs[i] < sporkIDs[j]
	})
	var prev *Spork
	for i, id := range sporkIDs {
		cfg := c.Sporks[id]
		if len(cfg.AccessNodes) == 0 {
			log.Fatalf(
				"Missing .access_nodes definition for %s-%d in %s",
				c.Network, id, c.filename,
			)
		}
		spork := &Spork{
			Chain:     result,
			Consensus: cfg.Consensus,
			ID:        id,
			Prev:      prev,
			RootBlock: cfg.RootBlock,
			Version:   cfg.Version,
		}
		if spork.Version < 1 || spork.Version > 4 {
			log.Fatalf(
				"Invalid .version value for %s-%d in %s",
				c.Network, id, c.filename,
			)
		}
		var store *cache.Store
		if result.Cache {
			store = cache.New(result.PathFor(spork.String() + "-cache"))
			if c.DropCache {
				err := store.DropAll()
				if err != nil {
					log.Fatalf("Failed to drop cache: %s", err)
				}
			}
		}
		spork.AccessNodes = access.New(ctx, cfg.AccessNodes, store)
		if i == len(sporkIDs)-1 {
			result.DataAccessNodes = access.New(ctx, cfg.AccessNodes, nil)
		}
		if prev != nil {
			prev.Next = spork
			if id != prev.ID+1 {
				log.Fatalf(
					"Missing .sporks definition for %s-%d in %s",
					c.Network, prev.ID+1, c.filename,
				)
			}
			if cfg.RootBlock <= prev.RootBlock {
				log.Fatalf(
					"The .root_block for %s-%d is not greater than %s-%d in %s",
					c.Network, id, c.Network, prev.ID, c.filename,
				)
			}
		}
		prev = spork
		result.Sporks = append(result.Sporks, spork)
	}
}

func (c *chainConfig) parseAndValidateDisableConsensusFollower(result *Chain) {
	result.UseConsensusFollwer = !c.DisableConsensusFollower
	if result.UseConsensusFollwer {
		latest := result.Sporks[len(result.Sporks)-1]
		cfg := latest.Consensus
		if cfg == nil {
			log.Fatalf(
				"Missing .consensus definition for %s-%d",
				c.Network, latest.ID,
			)
		}
		//lint:ignore SA5011 We exit above in the log.Fatalf call if cfg is nil.
		if cfg.RootProtocolStateURL == "" {
			log.Fatalf(
				"Missing .consensus.root_protocol_state_url definition for %s-%d",
				c.Network, latest.ID,
			)
		}
		//lint:ignore SA5011 We exit above in the log.Fatalf call if cfg is nil.
		if cfg.RootProtocolStateSignatureURL == "" && !cfg.DisableSignatureCheck {
			log.Fatalf(
				"Missing .consensus.root_protocol_state_signature_url definition for %s-%d",
				c.Network, latest.ID,
			)
		}
		if len(cfg.SeedNodes) == 0 {
			log.Fatalf(
				"Missing .consensus.seed_nodes definition for %s-%d",
				c.Network, latest.ID,
			)
		}
		for i, node := range cfg.SeedNodes {
			if node.Host == "" {
				log.Fatalf(
					"Missing .consensus.seed_nodes[%d].host definition for %s-%d",
					i, c.Network, latest.ID,
				)
			}
			if node.Port == 0 {
				log.Fatalf(
					"Missing .consensus.seed_nodes[%d].port definition for %s-%d",
					i, c.Network, latest.ID,
				)
			}
			if node.PublicKey == "" {
				log.Fatalf(
					"Missing .consensus.seed_nodes[%d].public_key definition for %s-%d",
					i, c.Network, latest.ID,
				)
			}
		}
		if cfg.SigningKey == "" {
			log.Fatalf(
				"Missing .consensus.signing_key definition for %s-%d",
				c.Network, latest.ID,
			)
		}
	}
}
