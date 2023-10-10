package rpc_server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"

	"github.com/cometbft/cometbft/libs/log"
	rpcserver "github.com/cometbft/cometbft/rpc/jsonrpc/server"
	"github.com/cometbft/cometbft/rpc/jsonrpc/types"
)

func StartRPCServer(listenAddr string, logger log.Logger, config *rpcserver.Config) {
	mux := http.NewServeMux()
	logger.Info("Starting RPC HTTP server on", "address", listenAddr)
	rpcLogger := logger.With("module", "rpc-server")
	wmLogger := rpcLogger.With("protocol", "websocket")
	wm := rpcserver.NewWebsocketManager(Routes,
		rpcserver.ReadLimit(config.MaxBodyBytes),
	)
	wm.SetLogger(wmLogger)
	mux.HandleFunc("/websocket", wm.WebsocketHandler)
	rpcserver.RegisterRPCFuncs(mux, Routes, rpcLogger)
	listener, err := rpcserver.Listen(
		listenAddr,
		config.MaxOpenConnections,
	)
	if err != nil {
		panic(err)
	}

	var rootHandler http.Handler = mux
	if err := rpcserver.Serve(
		listener,
		ExtraLogHandler(rootHandler, rpcLogger),
		rpcLogger,
		config,
	); err != nil {
		logger.Error("Error serving server", "err", err)
		panic(err)
	}
}

func StartRPCServerWithDefaultConfig(listenAddr string, logger log.Logger) {
	StartRPCServer(listenAddr, logger, rpcserver.DefaultConfig())
}

// RecoverAndLogHandler wraps an HTTP handler, adding error logging.
// If the inner function panics, the outer function recovers, logs, sends an
// HTTP 500 error response.
func ExtraLogHandler(handler http.Handler, logger log.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				logger.Error("failed to read request body", "err", err)
			} else {
				logger.Debug("served RPC HTTP request",
					"body", string(body),
				)
			}

			r.Body = ioutil.NopCloser(bytes.NewBuffer(body))
		}

		handler.ServeHTTP(w, r)
	})
}

// WriteRPCResponseHTTP marshals res as JSON (with indent) and writes it to w.
func WriteRPCResponseHTTP(w http.ResponseWriter, res ...types.RPCResponse) error {
	return writeRPCResponseHTTP(w, []httpHeader{}, res...)
}

type httpHeader struct {
	name  string
	value string
}

func writeRPCResponseHTTP(w http.ResponseWriter, headers []httpHeader, res ...types.RPCResponse) error {
	var v interface{}
	if len(res) == 1 {
		v = res[0]
	} else {
		v = res
	}

	jsonBytes, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("json marshal: %w", err)
	}
	w.Header().Set("Content-Type", "application/json")
	for _, header := range headers {
		w.Header().Set(header.name, header.value)
	}
	w.WriteHeader(200)
	_, err = w.Write(jsonBytes)
	return err
}

// WriteRPCResponseHTTPError marshals res as JSON (with indent) and writes it
// to w.
//
// source: https://www.jsonrpc.org/historical/json-rpc-over-http.html
func WriteRPCResponseHTTPError(
	w http.ResponseWriter,
	httpCode int,
	res types.RPCResponse,
) error {
	if res.Error == nil {
		panic("tried to write http error response without RPC error")
	}

	jsonBytes, err := json.Marshal(res)
	if err != nil {
		return fmt.Errorf("json marshal: %w", err)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpCode)
	_, err = w.Write(jsonBytes)
	return err
}
