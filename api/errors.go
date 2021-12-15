package api

import (
	"fmt"
	"strings"

	"github.com/onflow/rosetta/log"
	"github.com/coinbase/rosetta-sdk-go/types"
)

var (
	errorCodes   = map[int32]*types.Error{}
	serverErrors = []*types.Error{}
)

var (
	// base errors
	errBroadcastFailed         = newError(101, "broadcast failed", false)
	errNotImplemented          = newError(102, "method not implemented", false)
	errOfflineMode             = newError(103, "method not available in offline mode", false)
	errTransactionNotInMempool = newError(104, "transaction not in mempool", false)
	// on-chain errors
	errInvalidNumberOfAccountKeys = newError(201, "invalid number of account keys", false)
	errTransactionExpired         = newError(202, "transaction expired", false)
	// internal errors
	errInternal    = newError(301, "unexpected internal error", false)
	errMissingData = newError(302, "block state doesn't exist or has been pruned at the execution node", false)
	errProtobuf    = newError(303, "protobuf error", false)
	// input validation errors
	errInvalidAccountAddress     = newError(401, "invalid account address", false)
	errInvalidBlockHash          = newError(402, "invalid block hash", false)
	errInvalidBlockIdentifier    = newError(403, "invalid block identifier", false)
	errInvalidBlockIndex         = newError(404, "invalid block index", false)
	errInvalidConstructOptions   = newError(405, "invalid construct options", false)
	errInvalidCurrency           = newError(406, "invalid currency", false)
	errInvalidMetadataField      = newError(407, "invalid metadata field", false)
	errInvalidOpsIntent          = newError(408, "invalid ops intent", false)
	errInvalidSignature          = newError(409, "invalid signature", false)
	errInvalidTransactionHash    = newError(410, "invalid transaction hash", false)
	errInvalidTransactionPayload = newError(411, "invalid transaction payload", false)
	errUnknownTransactionHash    = newError(412, "unknown transaction hash", false)
	// potentially retriable errors
	errAccessNodeInaccessible = newError(501, "access node inaccessible", true)
	errBlockNotIndexed        = newError(502, "block data not in index", true)
	errRateLimited            = newError(503, "rate limited", true)
	errInvalidIndexedState    = newError(504, "invalid indexed state", true)
	errFailedAccessAPICall    = newError(505, "failed access api call", true)
)

func formatErr(xerr *types.Error) string {
	if len(xerr.Details) > 0 {
		detail := xerr.Details["error"].(string)
		return fmt.Sprintf(
			"%s [%d] (%s)", xerr.Message, xerr.Code, detail,
		)
	}
	return fmt.Sprintf("%s [%d]", xerr.Message, xerr.Code)
}

func handleExecutionErr(err error, typ string) *types.Error {
	// NOTE(tav): We use string matching to try and determine if the data
	// doesn't exist, e.g. because the state has been pruned from the execution
	// node or doesn't exist.
	//
	// This error string may change upstream in which case, we'd need to update
	// this check.
	if strings.Contains(err.Error(), "get register failed") {
		return wrapErrorf(errMissingData, "failed to %s: %s", typ, err)
	}
	return wrapErrorf(errInternal, "failed to %s: %s", typ, err)
}

func newError(code int32, msg string, retriable bool) *types.Error {
	prev, exists := errorCodes[code]
	if exists {
		log.Fatalf(
			"Duplicate API error definition %d for %q and %q",
			code, msg, prev.Message,
		)
	}
	err := &types.Error{
		Code:      code,
		Message:   msg,
		Retriable: retriable,
	}
	serverErrors = append(serverErrors, err)
	return err
}

func wrapErr(xerr *types.Error, err error) *types.Error {
	dup := *xerr
	dup.Details = map[string]interface{}{
		"error": err.Error(),
	}
	return &dup
}

func wrapErrorf(xerr *types.Error, format string, args ...interface{}) *types.Error {
	dup := *xerr
	dup.Details = map[string]interface{}{
		"error": fmt.Sprintf(format, args...),
	}
	return &dup
}
