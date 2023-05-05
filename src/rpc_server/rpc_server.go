package rpc_server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime/debug"
	"time"

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
		config,
	)
	if err != nil {
		panic(err)
	}

	var rootHandler http.Handler = mux
	if err := rpcserver.Serve(
		listener,
		rootHandler,
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

func ServeRPC(listener net.Listener, handler http.Handler, logger log.Logger, config *rpcserver.Config) error {
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

// RecoverAndLogHandler wraps an HTTP handler, adding error logging.
// If the inner function panics, the outer function recovers, logs, sends an
// HTTP 500 error response.
func RecoverAndLogHandler(handler http.Handler, logger log.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Wrap the ResponseWriter to remember the status
		rww := &responseWriterWrapper{-1, w}
		begin := time.Now()

		rww.Header().Set("X-Server-Time", fmt.Sprintf("%v", begin.Unix()))

		defer func() {
			// Handle any panics in the panic handler below. Does not use the logger, since we want
			// to avoid any further panics. However, we try to return a 500, since it otherwise
			// defaults to 200 and there is no other way to terminate the connection. If that
			// should panic for whatever reason then the Go HTTP server will handle it and
			// terminate the connection - panicing is the de-facto and only way to get the Go HTTP
			// server to terminate the request and close the connection/stream:
			// https://github.com/golang/go/issues/17790#issuecomment-258481416
			if e := recover(); e != nil {
				fmt.Fprintf(os.Stderr, "Panic during RPC panic recovery: %v\n%v\n", e, string(debug.Stack()))
				w.WriteHeader(500)
			}
		}()

		defer func() {
			// Send a 500 error if a panic happens during a handler.
			// Without this, Chrome & Firefox were retrying aborted ajax requests,
			// at least to my localhost.
			if e := recover(); e != nil {
				// If RPCResponse
				if res, ok := e.(types.RPCResponse); ok {
					if wErr := WriteRPCResponseHTTP(rww, res); wErr != nil {
						logger.Error("failed to write response", "res", res, "err", wErr)
					}
				} else {
					// Panics can contain anything, attempt to normalize it as an error.
					var err error
					switch e := e.(type) {
					case error:
						err = e
					case string:
						err = errors.New(e)
					case fmt.Stringer:
						err = errors.New(e.String())
					default:
					}

					logger.Error("panic in RPC HTTP handler", "err", e, "stack", string(debug.Stack()))

					res := types.RPCInternalError(types.JSONRPCIntID(-1), err)
					if wErr := WriteRPCResponseHTTPError(rww, http.StatusInternalServerError, res); wErr != nil {
						logger.Error("failed to write response", "res", res, "err", wErr)
					}
				}
			}

			// Finally, log.
			durationMS := time.Since(begin).Nanoseconds() / 1000000
			if rww.Status == -1 {
				rww.Status = 200
			}
			logger.Debug("served RPC HTTP response",
				"method", r.Method,
				"url", r.URL,
				"status", rww.Status,
				"duration", durationMS,
				"remoteAddr", r.RemoteAddr,
			)
		}()

		handler.ServeHTTP(rww, r)
	})
}

// Remember the status for logging
type responseWriterWrapper struct {
	Status int
	http.ResponseWriter
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

type maxBytesHandler struct {
	h http.Handler
	n int64
}

func (h maxBytesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, h.n)
	h.h.ServeHTTP(w, r)
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
