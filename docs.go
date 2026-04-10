// Package scheduler exposes the public root import for the A2A scheduler extension.
//
// In addition to extension metadata, the root package provides convenience
// registration helpers such as [RegisterGRPC] and [RegisterHTTP] so callers do
// not need to import the handler and jsonrpc subpackages just to mount the
// standard scheduler surfaces.
package scheduler
