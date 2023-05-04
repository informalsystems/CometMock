package main

import (
	"net"
	"net/http"

	"github.com/cometbft/cometbft/libs/log"
	rpc "github.com/cometbft/cometbft/rpc/jsonrpc/server"
)

var Routes = map[string]*rpc.RPCFunc{
	// info API
	"block": rpc.NewRPCFunc(Block, "height", rpc.Cacheable("height")),

	// tx broadcast API
	"broadcast_tx_commit": rpc.NewRPCFunc(BroadcastTxCommit, "tx"),
	"broadcast_tx_sync":   rpc.NewRPCFunc(BroadcastTxSync, "tx"),
	"broadcast_tx_async":  rpc.NewRPCFunc(BroadcastTxAsync, "tx"),

	// abci API
	"abci_query": rpc.NewRPCFunc(ABCIQuery, "path,data,height,prove"),
	"abci_info":  rpc.NewRPCFunc(ABCIInfo, "", rpc.Cacheable()),
}

func ServeRPC(listener net.Listener, handler http.Handler, logger log.Logger) error {
	logger.Info("serve", "msg", log.NewLazySprintf("Starting RPC HTTP server on %s", listener.Addr()))
	s := &http.Server{
		Handler:           RecoverAndLogHandler(maxBytesHandler{h: handler, n: config.MaxBodyBytes}, logger),
		ReadTimeout:       config.ReadTimeout,
		ReadHeaderTimeout: config.ReadTimeout,
		WriteTimeout:      config.WriteTimeout,
		MaxHeaderBytes:    config.MaxHeaderBytes,
	}
	err := s.Serve(listener)
	logger.Info("RPC HTTP server stopped", "err", err)
	return err
}
