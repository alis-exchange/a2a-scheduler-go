package srv

import (
	"net/http"
	"strings"
)

// corsConfig holds Access-Control-* values when CORS is enabled on [jsonrpcHandler].
type corsConfig struct {
	allowOrigin  string
	allowMethods string
	allowHeaders string
}

func defaultCORSConfig() corsConfig {
	return corsConfig{
		allowOrigin:  "*",
		allowMethods: "POST, OPTIONS",
		allowHeaders: "Content-Type, Authorization, X-Alis-Forwarded-Authorization, X-Alis-User-Id, X-Alis-User-Email",
	}
}

func (c *corsConfig) writeHeaders(rw http.ResponseWriter) {
	rw.Header().Set("Access-Control-Allow-Origin", c.allowOrigin)
	rw.Header().Set("Access-Control-Allow-Methods", c.allowMethods)
	rw.Header().Set("Access-Control-Allow-Headers", c.allowHeaders)
}

// CORSOption configures CORS when passed to [WithCORS].
type CORSOption func(*corsConfig)

// CORSAllowOrigin sets Access-Control-Allow-Origin (e.g. "https://app.example.com" or "*").
func CORSAllowOrigin(origin string) CORSOption {
	return func(c *corsConfig) {
		c.allowOrigin = origin
	}
}

// CORSAllowMethods sets Access-Control-Allow-Methods (comma-separated list in the header).
func CORSAllowMethods(methods ...string) CORSOption {
	return func(c *corsConfig) {
		if len(methods) == 0 {
			return
		}
		c.allowMethods = strings.Join(methods, ", ")
	}
}

// CORSAllowHeaders sets Access-Control-Allow-Headers (comma-separated list in the header).
func CORSAllowHeaders(headers ...string) CORSOption {
	return func(c *corsConfig) {
		if len(headers) == 0 {
			return
		}
		c.allowHeaders = strings.Join(headers, ", ")
	}
}

// WithCORS enables CORS for the JSON-RPC handler: sets response headers on every request and
// handles OPTIONS preflight. Pass [CORSAllowOrigin], [CORSAllowMethods], and/or [CORSAllowHeaders]
// to override defaults from [defaultCORSConfig].
func WithCORS(opts ...CORSOption) JSONRPCHandlerOption {
	cfg := defaultCORSConfig()
	for _, o := range opts {
		o(&cfg)
	}
	return func(h *jsonrpcHandler) {
		c := cfg
		h.cors = &c
	}
}
