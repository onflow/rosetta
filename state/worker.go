package state

import (
	"context"
	"time"

	"github.com/onflow/rosetta/cache"
	"github.com/onflow/rosetta/log"
	"github.com/onflow/rosetta/trace"
)

var (
	workerBlockHeight = trace.Gauge("state", "worker_block_height")
)

func (i *Indexer) runWorker(rctx context.Context, id int) {
	rctx = cache.Context(rctx, "worker")
	for {
		select {
		case <-rctx.Done():
			return
		case height := <-i.jobs:
			var (
				lastErr error
				span    trace.Span
			)
			workerBlockHeight.Observe(rctx, int64(height))
			attempt := 0
			backoff := time.Duration(0)
			slowPath := false
			spork := i.Chain.SporkFor(height)
		retry:
			for {
				if span != nil {
					trace.EndSpanErr(span, lastErr)
					if attempt%10 == 0 {
						log.Errorf(
							"Failed to prefetch Access API calls for height %d: %s (worker %d)",
							height, lastErr, id,
						)
					}
				}
				select {
				case <-rctx.Done():
					return
				default:
				}
				if attempt > 0 {
					if attempt == 1 {
						backoff = 100 * time.Millisecond
					} else {
						backoff *= 2
						if backoff > time.Second {
							backoff = time.Second
						}
					}
					time.Sleep(backoff)
				}
				attempt++
				ctx, span := trace.NewSpan(
					rctx, "flow.prefetch_block",
					trace.Int64("block_height", int64(height)),
					trace.Bool("slow_path", slowPath),
					trace.Int("worker", id),
				)
				client := spork.AccessNodes.Client()
				hdr, err := client.BlockHeaderByHeight(ctx, height)
				if err != nil {
					lastErr = err
					continue
				}
				block, err := client.BlockByHeight(ctx, height)
				if err != nil {
					lastErr = err
					continue
				}
				if !slowPath {
					_, err = client.TransactionsByBlockID(ctx, hdr.Id)
					if err != nil {
						if useSlowPath(err) {
							slowPath = true
							span.SetAttributes(trace.Bool("slow_path", true))
						} else {
							lastErr = err
							continue
						}
					}
				}
				if !slowPath {
					_, err = client.TransactionResultsByBlockID(ctx, hdr.Id)
					if err != nil {
						if useSlowPath(err) {
							slowPath = true
							span.SetAttributes(trace.Bool("slow_path", true))
						} else {
							lastErr = err
							continue
						}
					}
				}
				if slowPath {
					txnIndex := -1
					for _, col := range block.CollectionGuarantees {
						select {
						case <-rctx.Done():
							return
						default:
						}
						info, err := client.CollectionByID(ctx, col.CollectionId)
						if err != nil {
							lastErr = err
							continue retry
						}
						for _, txn := range info.TransactionIds {
							select {
							case <-rctx.Done():
								return
							default:
							}
							txnIndex++
							_, err := client.Transaction(ctx, txn)
							if err != nil {
								lastErr = err
								continue retry
							}
							_, err = client.TransactionResult(ctx, hdr.Id, uint32(txnIndex))
							if err != nil {
								lastErr = err
								continue retry
							}
						}
					}
					txnIndex++
					_, err = client.TransactionResult(ctx, hdr.Id, uint32(txnIndex))
					if err != nil {
						lastErr = err
						continue
					}
				}
				_, err = client.ExecutionResultForBlockID(ctx, block.Id)
				if err != nil {
					lastErr = err
					continue
				}
				lastErr = nil
				if debug {
					log.Infof(
						"Successfully prefetched Access API calls for height %d (worker %d)",
						height, id,
					)
				}
				trace.EndSpanOk(span)
				break
			}
		}
	}
}
