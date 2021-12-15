package api

import (
	"context"
	"fmt"

	"github.com/onflow/rosetta/log"
	"github.com/onflow/rosetta/trace"
	"github.com/coinbase/rosetta-sdk-go/types"
	"go.uber.org/zap"
)

// Wrapper wraps the Rosetta API handlers so that all requests are automatically
// traced with any response errors logged.
//
// If the system has encountered any invalid indexed state errors, then the
// first such error will be returned on all endpoints except for /call and
// /network/* API calls.
type Wrapper struct {
	s *Server
}

func (w Wrapper) AccountBalance(ctx context.Context, r *types.AccountBalanceRequest) (*types.AccountBalanceResponse, *types.Error) {
	if xerr := w.s.getIndexedStateErr(); xerr != nil {
		return nil, xerr
	}
	ctx, span := trace.NewSpan(ctx, "flow.rosetta_api.AccountBalance")
	resp, xerr := w.s.AccountBalance(ctx, r)
	if xerr != nil {
		logTraceError(span, "/account/balance", xerr)
	} else {
		trace.EndSpanOk(span)
	}
	return resp, xerr
}

func (w Wrapper) AccountCoins(ctx context.Context, r *types.AccountCoinsRequest) (*types.AccountCoinsResponse, *types.Error) {
	if xerr := w.s.getIndexedStateErr(); xerr != nil {
		return nil, xerr
	}
	ctx, span := trace.NewSpan(ctx, "flow.rosetta_api.AccountCoins")
	resp, xerr := w.s.AccountCoins(ctx, r)
	if xerr != nil {
		logTraceError(span, "/account/coins", xerr)
	} else {
		trace.EndSpanOk(span)
	}
	return resp, xerr
}

func (w Wrapper) Block(ctx context.Context, r *types.BlockRequest) (*types.BlockResponse, *types.Error) {
	if xerr := w.s.getIndexedStateErr(); xerr != nil {
		return nil, xerr
	}
	ctx, span := trace.NewSpan(ctx, "flow.rosetta_api.Block")
	resp, xerr := w.s.Block(ctx, r)
	if xerr != nil {
		logTraceError(span, "/block", xerr)
	} else {
		trace.EndSpanOk(span)
	}
	return resp, xerr
}

func (w Wrapper) BlockTransaction(ctx context.Context, r *types.BlockTransactionRequest) (*types.BlockTransactionResponse, *types.Error) {
	if xerr := w.s.getIndexedStateErr(); xerr != nil {
		return nil, xerr
	}
	ctx, span := trace.NewSpan(ctx, "flow.rosetta_api.BlockTransaction")
	resp, xerr := w.s.BlockTransaction(ctx, r)
	if xerr != nil {
		logTraceError(span, "/block/transaction", xerr)
	} else {
		trace.EndSpanOk(span)
	}
	return resp, xerr
}

func (w Wrapper) Call(ctx context.Context, r *types.CallRequest) (*types.CallResponse, *types.Error) {
	ctx, span := trace.NewSpan(ctx, "flow.rosetta_api.Call")
	resp, xerr := w.s.Call(ctx, r)
	if xerr != nil {
		logTraceError(span, "/call", xerr)
	} else {
		trace.EndSpanOk(span)
	}
	return resp, xerr
}

func (w Wrapper) ConstructionCombine(ctx context.Context, r *types.ConstructionCombineRequest) (*types.ConstructionCombineResponse, *types.Error) {
	if xerr := w.s.getIndexedStateErr(); xerr != nil {
		return nil, xerr
	}
	ctx, span := trace.NewSpan(ctx, "flow.rosetta_api.ConstructionCombine")
	resp, xerr := w.s.ConstructionCombine(ctx, r)
	if xerr != nil {
		logTraceError(span, "/construction/combine", xerr)
	} else {
		trace.EndSpanOk(span)
	}
	return resp, xerr
}

func (w Wrapper) ConstructionDerive(ctx context.Context, r *types.ConstructionDeriveRequest) (*types.ConstructionDeriveResponse, *types.Error) {
	if xerr := w.s.getIndexedStateErr(); xerr != nil {
		return nil, xerr
	}
	ctx, span := trace.NewSpan(ctx, "flow.rosetta_api.ConstructionDerive")
	resp, xerr := w.s.ConstructionDerive(ctx, r)
	if xerr != nil {
		logTraceError(span, "/construction/derive", xerr)
	} else {
		trace.EndSpanOk(span)
	}
	return resp, xerr
}

func (w Wrapper) ConstructionHash(ctx context.Context, r *types.ConstructionHashRequest) (*types.TransactionIdentifierResponse, *types.Error) {
	if xerr := w.s.getIndexedStateErr(); xerr != nil {
		return nil, xerr
	}
	ctx, span := trace.NewSpan(ctx, "flow.rosetta_api.ConstructionHash")
	resp, xerr := w.s.ConstructionHash(ctx, r)
	if xerr != nil {
		logTraceError(span, "/construction/hash", xerr)
	} else {
		trace.EndSpanOk(span)
	}
	return resp, xerr
}

func (w Wrapper) ConstructionMetadata(ctx context.Context, r *types.ConstructionMetadataRequest) (*types.ConstructionMetadataResponse, *types.Error) {
	if xerr := w.s.getIndexedStateErr(); xerr != nil {
		return nil, xerr
	}
	ctx, span := trace.NewSpan(ctx, "flow.rosetta_api.ConstructionMetadata")
	resp, xerr := w.s.ConstructionMetadata(ctx, r)
	if xerr != nil {
		logTraceError(span, "/construction/metadata", xerr)
	} else {
		trace.EndSpanOk(span)
	}
	return resp, xerr
}

func (w Wrapper) ConstructionParse(ctx context.Context, r *types.ConstructionParseRequest) (*types.ConstructionParseResponse, *types.Error) {
	if xerr := w.s.getIndexedStateErr(); xerr != nil {
		return nil, xerr
	}
	ctx, span := trace.NewSpan(ctx, "flow.rosetta_api.ConstructionParse")
	resp, xerr := w.s.ConstructionParse(ctx, r)
	if xerr != nil {
		logTraceError(span, "/construction/parse", xerr)
	} else {
		trace.EndSpanOk(span)
	}
	return resp, xerr
}

func (w Wrapper) ConstructionPayloads(ctx context.Context, r *types.ConstructionPayloadsRequest) (*types.ConstructionPayloadsResponse, *types.Error) {
	if xerr := w.s.getIndexedStateErr(); xerr != nil {
		return nil, xerr
	}
	ctx, span := trace.NewSpan(ctx, "flow.rosetta_api.ConstructionPayloads")
	resp, xerr := w.s.ConstructionPayloads(ctx, r)
	if xerr != nil {
		logTraceError(span, "/construction/payloads", xerr)
	} else {
		trace.EndSpanOk(span)
	}
	return resp, xerr
}

func (w Wrapper) ConstructionPreprocess(ctx context.Context, r *types.ConstructionPreprocessRequest) (*types.ConstructionPreprocessResponse, *types.Error) {
	if xerr := w.s.getIndexedStateErr(); xerr != nil {
		return nil, xerr
	}
	ctx, span := trace.NewSpan(ctx, "flow.rosetta_api.ConstructionPreprocess")
	resp, xerr := w.s.ConstructionPreprocess(ctx, r)
	if xerr != nil {
		logTraceError(span, "/construction/preprocess", xerr)
	} else {
		trace.EndSpanOk(span)
	}
	return resp, xerr
}

func (w Wrapper) ConstructionSubmit(ctx context.Context, r *types.ConstructionSubmitRequest) (*types.TransactionIdentifierResponse, *types.Error) {
	if xerr := w.s.getIndexedStateErr(); xerr != nil {
		return nil, xerr
	}
	ctx, span := trace.NewSpan(ctx, "flow.rosetta_api.ConstructionSubmit")
	resp, xerr := w.s.ConstructionSubmit(ctx, r)
	if xerr != nil {
		logTraceError(span, "/construction/submit", xerr)
	} else {
		trace.EndSpanOk(span)
	}
	return resp, xerr
}

func (w Wrapper) Mempool(ctx context.Context, r *types.NetworkRequest) (*types.MempoolResponse, *types.Error) {
	if xerr := w.s.getIndexedStateErr(); xerr != nil {
		return nil, xerr
	}
	ctx, span := trace.NewSpan(ctx, "flow.rosetta_api.Mempool")
	resp, xerr := w.s.Mempool(ctx, r)
	if xerr != nil {
		logTraceError(span, "/mempool", xerr)
	} else {
		trace.EndSpanOk(span)
	}
	return resp, xerr
}

func (w Wrapper) MempoolTransaction(ctx context.Context, r *types.MempoolTransactionRequest) (*types.MempoolTransactionResponse, *types.Error) {
	if xerr := w.s.getIndexedStateErr(); xerr != nil {
		return nil, xerr
	}
	ctx, span := trace.NewSpan(ctx, "flow.rosetta_api.MempoolTransaction")
	resp, xerr := w.s.MempoolTransaction(ctx, r)
	if xerr != nil {
		logTraceError(span, "/mempool/transaction", xerr)
	} else {
		trace.EndSpanOk(span)
	}
	return resp, xerr
}

func (w Wrapper) NetworkList(ctx context.Context, r *types.MetadataRequest) (*types.NetworkListResponse, *types.Error) {
	ctx, span := trace.NewSpan(ctx, "flow.rosetta_api.NetworkList")
	resp, xerr := w.s.NetworkList(ctx, r)
	if xerr != nil {
		logTraceError(span, "/network/list", xerr)
	} else {
		trace.EndSpanOk(span)
	}
	return resp, xerr
}

func (w Wrapper) NetworkOptions(ctx context.Context, r *types.NetworkRequest) (*types.NetworkOptionsResponse, *types.Error) {
	ctx, span := trace.NewSpan(ctx, "flow.rosetta_api.NetworkOptions")
	resp, xerr := w.s.NetworkOptions(ctx, r)
	if xerr != nil {
		logTraceError(span, "/network/options", xerr)
	} else {
		trace.EndSpanOk(span)
	}
	return resp, xerr
}

func (w Wrapper) NetworkStatus(ctx context.Context, r *types.NetworkRequest) (*types.NetworkStatusResponse, *types.Error) {
	ctx, span := trace.NewSpan(ctx, "flow.rosetta_api.NetworkStatus")
	resp, xerr := w.s.NetworkStatus(ctx, r)
	if xerr != nil {
		logTraceError(span, "/network/status", xerr)
	} else {
		trace.EndSpanOk(span)
	}
	return resp, xerr
}

func logTraceError(span trace.Span, method string, xerr *types.Error) {
	fields := []zap.Field{
		zap.Int32("error_code", xerr.Code),
		zap.String("error_message", xerr.Message),
		zap.Bool("error_retriable", xerr.Retriable),
		zap.Bool("rosetta_error", true),
		zap.String("rosetta_method", method),
	}
	msg := fmt.Sprintf("Failed %s request: %d %s", method, xerr.Code, xerr.Message)
	if len(xerr.Details) > 0 {
		derr := xerr.Details["error"].(string)
		fields = append(fields, zap.String("error_details", derr))
		msg = fmt.Sprintf("%s: %s", msg, derr)
	}
	log.Error(msg, fields...)
	trace.EndSpanErrorf(span, msg)
}
