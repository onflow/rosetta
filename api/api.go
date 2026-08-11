// Package api implements the Rosetta API for Flow.
package api

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
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
	"github.com/onflow/rosetta/state"
)

const (
	callAccountBalances         = "account_balances"
	callAccountPublicKeys       = "account_public_keys"
	callBalanceValidationStatus = "balance_validation_status"
	callEcho                    = "echo"
	callFeeValidationStatus     = "fee_receiver_validation_status"
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
		callFeeValidationStatus,
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
	feeAddrs                 map[string]bool
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
	scriptGetFeeReceivers    []byte
	scriptGetProxyNonce      []byte
	scriptGetProxyPublicKey  []byte
	scriptProxyTransfer      []byte
	scriptSetContract        []byte
	validation               *validation
	validationMu             sync.RWMutex // protects validation
	feeValidation            *feeValidation
	feeValidationMu          sync.RWMutex // protects feeValidation
}

// Run initializes the server and starts serving Rosetta API calls.
func (s *Server) Run(ctx context.Context) {
	s.compileScripts()
	s.validation = &validation{
		status: validationNotStarted,
	}
	s.feeValidation = &feeValidation{
		status: validationNotStarted,
	}
	go s.validateBalances(ctx)
	s.feeAddrs = s.Chain.Contracts.FeeAddresses()
	go s.validateFeeReceivers(ctx)
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
	s.scriptGetFeeReceivers = script.Compile("get_fee_receivers", script.GetFeeReceivers, s.Chain)
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
	if s.validation.status == validationFailure {
		return
	}
	s.validation = &validation{
		err:    msg,
		status: validationFailure,
	}
}

// currentFeeAddrs returns the set of fee addresses used to classify fee
// deposits at the given indexed height: the configured fee addresses,
// overridden by the most recent FlowFees.ChildFeeAccountsChanged event
// indexed at or before that height, if any.
func (s *Server) currentFeeAddrs(height uint64) map[string]bool {
	children, err := s.Index.FeeReceiversAt(height)
	if err != nil {
		log.Errorf(
			"Failed to get the indexed fee receivers at height %d, falling back to the configured fee addresses: %s",
			height, err,
		)
		return s.feeAddrs
	}
	if children == nil {
		return s.feeAddrs
	}
	return s.Chain.Contracts.FeeAddressesWith(children)
}

// setFeeValidationFallback records a successful validation against a FlowFees
// contract that predates the concurrent fee collection upgrade: the FlowFees
// account is the only fee receiver until the contract is upgraded.
func (s *Server) setFeeValidationFallback() {
	onchain := []string{s.Chain.Contracts.FlowFees}
	s.feeValidationMu.Lock()
	prev := s.feeValidation.status
	s.feeValidation = &feeValidation{
		onchain: onchain,
		status:  validationSuccess,
	}
	s.feeValidationMu.Unlock()
	// We only log on transitions so that the periodic re-checks don't flood
	// the logs.
	if prev != validationSuccess {
		log.Infof(
			"The FlowFees contract does not define getFeeReceiverAddresses (pre concurrent fee collection); "+
				"falling back to the FlowFees account %s as the only fee receiver and continuing to poll for an upgrade",
			onchain[0],
		)
	}
}

func (s *Server) getFeeValidationStatus() *feeValidation {
	s.feeValidationMu.RLock()
	defer s.feeValidationMu.RUnlock()
	return s.feeValidation
}

func (s *Server) setFeeValidationRetrying(format string, a ...interface{}) {
	msg := fmt.Sprintf(format, a...)
	log.Errorf("%s", msg)
	s.feeValidationMu.Lock()
	defer s.feeValidationMu.Unlock()
	// We only track transient errors while we're still waiting for the first
	// definitive result. Once we have one, it stays in place until the next
	// definitive result replaces it.
	if s.feeValidation.status == validationSuccess || s.feeValidation.status == validationFailure {
		return
	}
	s.feeValidation = &feeValidation{
		err:    msg,
		status: validationInProgress,
	}
}

func (s *Server) setFeeValidationFailure(onchain []string, missing []string) {
	msg := fmt.Sprintf(
		"On-chain fee receiver account(s) %s are missing from the configured fee addresses: "+
			"fee deposits to them would be misclassified as transfers; add them to .contracts.fee_receivers",
		strings.Join(missing, ", "),
	)
	log.Errorf("%s", msg)
	s.feeValidationMu.Lock()
	defer s.feeValidationMu.Unlock()
	s.feeValidation = &feeValidation{
		err:     msg,
		missing: missing,
		onchain: onchain,
		status:  validationFailure,
	}
}

func (s *Server) setFeeValidationSuccess(onchain []string) {
	s.feeValidationMu.Lock()
	prev := s.feeValidation.status
	s.feeValidation = &feeValidation{
		onchain: onchain,
		status:  validationSuccess,
	}
	s.feeValidationMu.Unlock()
	// We only log on transitions so that the periodic re-checks don't flood
	// the logs.
	if prev != validationSuccess {
		log.Infof(
			"Validated the configured fee addresses against the on-chain fee receivers: %s",
			strings.Join(onchain, ", "),
		)
	}
}

func (s *Server) setValidationProgress(accounts int, checked int) {
	s.validationMu.Lock()
	defer s.validationMu.Unlock()
	if s.validation.status == validationFailure || s.validation.status == validationSuccess {
		return
	}
	s.validation = &validation{
		accounts: accounts,
		checked:  checked,
		status:   validationInProgress,
	}
}

func (s *Server) setValidationSuccess(accounts int) {
	s.validationMu.Lock()
	defer s.validationMu.Unlock()
	if s.validation.status == validationFailure {
		return
	}
	s.validation = &validation{
		accounts: accounts,
		status:   validationSuccess,
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

// validationStatus enumerates the states a background validation process can
// be in.
type validationStatus int

const (
	validationNotStarted validationStatus = iota
	validationInProgress
	validationSuccess
	validationFailure
)

// String returns the status in the form reported by the /call endpoint.
func (v validationStatus) String() string {
	switch v {
	case validationNotStarted:
		return "not_started"
	case validationInProgress:
		return "in_progress"
	case validationSuccess:
		return "success"
	case validationFailure:
		return "failure"
	default:
		log.Fatalf("Unsupported validation status %d", int(v))
		panic("unreachable code")
	}
}

type validation struct {
	accounts int
	checked  int
	err      string
	status   validationStatus
}

type feeValidation struct {
	err     string
	missing []string
	onchain []string
	status  validationStatus
}
