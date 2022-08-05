// Package api implements the Rosetta API for Flow.
package api

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/coinbase/rosetta-sdk-go/asserter"
	"github.com/coinbase/rosetta-sdk-go/server"
	"github.com/coinbase/rosetta-sdk-go/types"
	"github.com/onflow/rosetta/access"
	"github.com/onflow/rosetta/config"
	"github.com/onflow/rosetta/indexdb"
	"github.com/onflow/rosetta/log"
	"github.com/onflow/rosetta/model"
	"github.com/onflow/rosetta/process"
	"github.com/onflow/rosetta/script"
	"github.com/onflow/rosta/state"
)

const (
	callAccountBalances         = "account_balances"
	callAccountPublicKeys       = "account_public_keys"
	callBalanceValidationStatus = "balance_validation_status"
	callEcho                    = "echo"
	callLatestBlock             = "latest_block"
	callListAccounts            = "list_accounts"
	callVerifyAddress           = "verify_address"
	opCreateAccount             = "create_account"
	opCreateProxyAccount        = "create_proxy_account"
	opDeployContract            = "deploy_contract"
	opFee                       = "fee"
	opProxyTransfer             = "proxy_transfer"
	opProxyTransferInner        = "proxy_transfer_inner"
	opTransfer                  = "transfer"
	opUpdateContract            = "update_contract"
	statusFailed                = "FAILED"
	statusSuccess               = "SUCCESS"
)

var (
	callMethods = []string{
		callAccountBalances,
		callAccountPublicKeys,
		callBalanceValidationStatus,
		callEcho,
		callLatestBlock,
		callListAccounts,
		callVerifyAddress,
	}
	flowCurrency = &types.Currency{
		Decimals: 8,
		Symbol:   "FLOW",
	}
	opTypes = []string{
		opCreateAccount,
		opCreateProxyAccount,
		opDeployContract,
		opFee,
		opProxyTransfer,
		opProxyTransferInner,
		opTransfer,
		opUpdateContract,
	}
	userTag = []byte("FLOW-V0.0-user\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00")
)

// Server is the Flow Rosetta API server.
type Server struct {
	Chain                    *config.Chain
	ConstructionAccessNodes  access.Pool
	DataAccessNodes          access.Pool
	Index                    *indexdb.Store
	Indexer                  *state.Indexer
	Offline                  bool
	Port                     uint16
	feeAddr                  []byte
	genesis                  *model.BlockMeta
	indexedStateErr          *types.Error
	mu                       sync.RWMutex // protects indexedStateErr
	networks                 []*types.NetworkIdentifier
	scriptBasicTransfer      []byte
	scriptComputeFees        []byte
	scriptCreateAccount      []byte
	scriptCreateProxyAccount []byte
	scriptGetBalances        []byte
	scriptGetBalancesBasic   []byte
	scriptGetProxyNonce      []byte
	scriptGetProxyPublicKey  []byte
	scriptProxyTransfer      []byte
	scriptSetContract        []byte
	validation               *validation
	validationMu             sync.RWMutex // protects validation
}

// Run initializes the server and starts serving Rosetta API calls.
func (s *Server) Run(ctx context.Context) {
	s.compileScripts()
	s.validation = &validation{
		status: "not_started",
	}
	go s.validateBalances(ctx)
	feeAddr, err := hex.DecodeString(s.Chain.Contracts.FlowFees)
	if err != nil {
		log.Fatalf(
			"Invalid FlowFees contract address %q: %s",
			s.Chain.Contracts.FlowFees, err,
		)
	}
	s.feeAddr = feeAddr
	s.genesis = s.Index.Genesis()
	s.networks = []*types.NetworkIdentifier{{
		Blockchain: "flow",
		Network:    s.Chain.Network,
	}}
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
	wrapped := Wrapper{s}
	router := server.NewRouter(
		server.NewAccountAPIController(wrapped, asserter),
		server.NewBlockAPIController(wrapped, asserter),
		server.NewCallAPIController(wrapped, asserter),
		server.NewConstructionAPIController(wrapped, asserter),
		server.NewMempoolAPIController(wrapped, asserter),
		server.NewNetworkAPIController(wrapped, asserter),
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
	s.scriptComputeFees = script.Compile("compute_fees", script.ComputeFees, s.Chain)
	s.scriptCreateAccount = script.Compile("create_account", script.CreateAccount, s.Chain)
	s.scriptCreateProxyAccount = script.Compile("create_proxy_account", script.CreateProxyAccount, s.Chain)
	s.scriptGetBalances = script.Compile("get_balances", script.GetBalances, s.Chain)
	s.scriptGetBalancesBasic = script.Compile("get_balances_basic", script.GetBalancesBasic, s.Chain)
	s.scriptGetProxyNonce = script.Compile("get_proxy_nonce", script.GetProxyNonce, s.Chain)
	s.scriptGetProxyPublicKey = script.Compile("get_proxy_public_key", script.GetProxyPublicKey, s.Chain)
	s.scriptProxyTransfer = script.Compile("proxy_transfer", script.ProxyTransfer, s.Chain)
	s.scriptSetContract = script.Compile("set_contract", script.SetContract, s.Chain)
}

func (s *Server) getAccount(addr string) ([]byte, *types.Error) {
	if len(addr) != 18 || addr[:2] != "0x" {
		return nil, wrapErrorf(
			errInvalidAccountAddress,
			"api: address %q is not valid",
			addr,
		)
	}
	acct, err := hex.DecodeString(addr[2:])
	if err != nil {
		return nil, wrapErrorf(
			errInvalidAccountAddress,
			"api: address %q could not be hex decoded: %s",
			addr, err,
		)
	}
	return acct, nil
}

func (s *Server) getIndexedStateErr() *types.Error {
	s.mu.RLock()
	xerr := s.indexedStateErr
	s.mu.RUnlock()
	return xerr
}

func (s *Server) getValidationStatus() *validation {
	s.validationMu.RLock()
	defer s.validationMu.RUnlock()
	return s.validation
}

func (s *Server) setIndexedStateErr(format string, a ...interface{}) {
	msg := fmt.Sprintf(format, a...)
	log.Errorf(msg)
	xerr := wrapErrorf(errInvalidIndexedState, msg)
	s.mu.Lock()
	// NOTE(tav): We preserve the very first invalid indexed state error we see.
	if s.indexedStateErr == nil {
		s.indexedStateErr = xerr
	}
	s.mu.Unlock()
	s.validationMu.Lock()
	defer s.validationMu.Unlock()
	if s.validation.status == "failure" {
		return
	}
	s.validation = &validation{
		err:    msg,
		status: "failure",
	}
}

func (s *Server) setValidationProgress(accounts int, checked int) {
	s.validationMu.Lock()
	defer s.validationMu.Unlock()
	if s.validation.status == "failure" || s.validation.status == "success" {
		return
	}
	s.validation = &validation{
		accounts: accounts,
		checked:  checked,
		status:   "in_progress",
	}
}

func (s *Server) setValidationSuccess(accounts int) {
	s.validationMu.Lock()
	defer s.validationMu.Unlock()
	if s.validation.status == "failure" {
		return
	}
	s.validation = &validation{
		accounts: accounts,
		status:   "success",
	}
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
	Amount   string `json:"amount"`
	Receiver string `json:"receiver,omitempty"`
	Sender   string `json:"sender,omitempty"`
	Type     string `json:"type"`
}

type txnIntent struct {
	amount         uint64
	contractCode   string
	contractName   string
	contractUpdate bool
	keyMessage     string
	keyMetadata    string
	keySignature   string
	keys           []string
	inner          bool
	newKey         string
	prevKeyIndex   uint32
	proxy          bool
	receiver       []byte
	sender         []byte
}

type validation struct {
	accounts int
	checked  int
	err      string
	status   string
}
