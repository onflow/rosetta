// Package api implements the Rosetta API for Flow.
package api

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"github.cbhq.net/nodes/rosetta-flow/access"
	"github.cbhq.net/nodes/rosetta-flow/config"
	"github.cbhq.net/nodes/rosetta-flow/indexdb"
	"github.cbhq.net/nodes/rosetta-flow/log"
	"github.cbhq.net/nodes/rosetta-flow/model"
	"github.cbhq.net/nodes/rosetta-flow/process"
	"github.cbhq.net/nodes/rosetta-flow/script"
	"github.cbhq.net/nodes/rosetta-flow/state"
	"github.com/coinbase/rosetta-sdk-go/asserter"
	"github.com/coinbase/rosetta-sdk-go/server"
	"github.com/coinbase/rosetta-sdk-go/types"
)

const (
	callAccountBalances   = "account_balances"
	callAccountPublicKeys = "account_public_keys"
	callEcho              = "echo"
	callLatestBlock       = "latest_block"
	opCreateAccount       = "create_account"
	opCreateProxyAccount  = "create_proxy_account"
	opDeploy              = "deploy"
	opFee                 = "fee"
	opProxyTransfer       = "proxy_transfer"
	opProxyTransferInner  = "proxy_transfer_inner"
	opTransfer            = "transfer"
	statusFailed          = "FAILED"
	statusSuccess         = "SUCCESS"
)

var (
	callMethods = []string{
		callAccountBalances,
		callAccountPublicKeys,
		callEcho,
		callLatestBlock,
	}
	flowCurrency = &types.Currency{
		Decimals: 8,
		Symbol:   "FLOW",
	}
	opTypes = []string{
		opCreateAccount,
		opCreateProxyAccount,
		opDeploy,
		opFee,
		opProxyTransfer,
		opProxyTransferInner,
		opTransfer,
	}
	userTag = []byte("FLOW-V0.0-user\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00")
)

// Server is the Flow Rosetta API server.
type Server struct {
	Access                   access.Pool
	Chain                    *config.Chain
	Index                    *indexdb.Store
	Indexer                  *state.Indexer
	Offline                  bool
	Port                     uint16
	genesis                  *model.BlockMeta
	networks                 []*types.NetworkIdentifier
	spork                    *config.Spork
	scriptBasicTransfer      []byte
	scriptCreateAccount      []byte
	scriptCreateProxyAccount []byte
	scriptDeployContract     []byte
	scriptGetBalances        []byte
	scriptGetBalancesBasic   []byte
	scriptGetProxyNonce      []byte
	scriptGetProxyPublicKey  []byte
	scriptProxyTransfer      []byte
}

// Run initializes the server and starts serving Rosetta API calls.
func (s *Server) Run(ctx context.Context) {
	s.compileScripts()
	go s.validateBalances(ctx)
	s.genesis = s.Index.Genesis()
	s.networks = []*types.NetworkIdentifier{{
		Blockchain: "flow",
		Network:    s.Chain.Network,
	}}
	s.spork = s.Chain.LatestSpork()
	asserter, err := asserter.NewServer(
		opTypes,
		true,
		s.networks,
		callMethods,
		false,
		"",
	)
	if err != nil {
		log.Fatalf("Failed to instantiate the Rosetta asserter: %w", err)
	}
	router := server.NewRouter(
		server.NewAccountAPIController(s, asserter),
		server.NewBlockAPIController(s, asserter),
		server.NewCallAPIController(s, asserter),
		server.NewConstructionAPIController(s, asserter),
		server.NewMempoolAPIController(s, asserter),
		server.NewNetworkAPIController(s, asserter),
	)
	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", s.Port),
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
	log.Infof("Starting Rosetta Server on port %d", s.Port)
	go func() {
		process.SetExitHandler(func() {
			log.Infof("Shutting down Rosetta HTTP Server gracefully")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := srv.Shutdown(ctx); err != nil {
				log.Errorf("Failed to shutdown Rosetta HTTP Server gracefully: %s", err)
			}
		})
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Rosetta HTTP Server failed: %s", err)
		}
	}()
}

func (s *Server) compileScripts() {
	s.scriptBasicTransfer = script.Compile("basic_transfer", script.BasicTransfer, s.Chain)
	s.scriptCreateAccount = script.Compile("create_account", script.CreateAccount, s.Chain)
	s.scriptCreateProxyAccount = script.Compile("create_proxy_account", script.CreateProxyAccount, s.Chain)
	s.scriptDeployContract = script.Compile("deploy_contract", script.DeployContract, s.Chain)
	s.scriptGetBalances = script.Compile("get_balances", script.GetBalances, s.Chain)
	s.scriptGetBalancesBasic = script.Compile("get_balances_basic", script.GetBalancesBasic, s.Chain)
	s.scriptGetProxyNonce = script.Compile("get_proxy_nonce", script.GetProxyNonce, s.Chain)
	s.scriptGetProxyPublicKey = script.Compile("get_proxy_public_key", script.GetProxyPublicKey, s.Chain)
	s.scriptProxyTransfer = script.Compile("proxy_transfer", script.ProxyTransfer, s.Chain)
}

func (s *Server) getAccount(addr string) ([]byte, *types.Error) {
	if len(addr) != 18 || addr[:2] != "0x" {
		return nil, errInvalidAccountAddress
	}
	acct, err := hex.DecodeString(addr[2:])
	if err != nil {
		return nil, wrapErr(errInvalidAccountAddress, err)
	}
	return acct, nil
}

type accountKey struct {
	HashAlgorithm      uint32 `json:"hash_algorithm"`
	KeyIndex           uint32 `json:"key_index"`
	PublicKey          string `json:"public_key"`
	SequenceNumber     uint32 `json:"sequence_number"`
	SignatureAlgorithm uint32 `json:"signature_algorithm"`
	Weight             uint32 `json:"weight"`
}

type onchainData struct {
	DefaultBalance uint64
	IsProxy        bool
	ProxyBalance   uint64
}

type innerTxn struct {
	amount   uint64
	nonce    uint64
	raw      []byte
	receiver []byte
	sender   []byte
}

type transferEvent struct {
	Account string `json:"account"`
	Amount  string `json:"amount"`
	Type    string `json:"type"`
}

type txnIntent struct {
	amount       uint64
	contractCode string
	contractName string
	keys         []string
	inner        bool
	proxy        bool
	receiver     []byte
	sender       []byte
}
