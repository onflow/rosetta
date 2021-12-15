// Command server runs the Flow Rosetta server
package main

import (
	"context"
	"fmt"
	"os"

	"github.cbhq.net/nodes/rosetta-flow/api"
	"github.cbhq.net/nodes/rosetta-flow/config"
	"github.cbhq.net/nodes/rosetta-flow/indexdb"
	"github.cbhq.net/nodes/rosetta-flow/log"
	"github.cbhq.net/nodes/rosetta-flow/process"
	"github.cbhq.net/nodes/rosetta-flow/state"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: server path/to/config.json [--export filename]")
		os.Exit(1)
	}
	ctx, cancel := context.WithCancel(context.Background())
	process.SetExitHandler(cancel)
	cfg := config.Init(ctx, os.Args[1])
	index := indexdb.New(cfg.PathFor(cfg.Network + "-index"))
	if len(os.Args) == 4 && os.Args[2] == "--export" {
		index.ExportAccounts(os.Args[3])
		process.Exit(0)
	}
	log.Infof("Running %s in %s mode", cfg.Network, cfg.Mode)
	offline := cfg.Mode == "offline"
	indexer := &state.Indexer{
		Chain: cfg,
		Store: index,
	}
	if !offline {
		indexer.Run(ctx)
	}
	srv := &api.Server{
		Access:  cfg.LatestAccessNodes,
		Chain:   cfg,
		Index:   index,
		Indexer: indexer,
		Offline: offline,
		Port:    cfg.Port,
	}
	srv.Run(ctx)
	<-ctx.Done()
}
