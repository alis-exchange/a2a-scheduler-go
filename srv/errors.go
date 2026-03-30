package srv

import (
	"errors"
	"fmt"
)

// JSONRPCError is a JSON-RPC 2.0 error.
type JSONRPCError interface {
	Error() string
	Is(target error) bool
	JSONRPCErrorObject() *jsonrpcErrorObject
}

// jsonrpcErrorObject represents a JSON-RPC 2.0 error
type jsonrpcErrorObject struct {
	// JSON-RPC 2.0 error code
	//
	//  - 32700 Parse error: Invalid JSON was received by the server.
	//  - 32600 Invalid Request: The JSON sent is not a valid Request object.
	//  - 32601 Method not found: The method does not exist / is not available.
	//  - 32602 Invalid params: Invalid method parameter(s).
	//  - 32603 Internal error: Internal JSON-RPC error.
	//  - 32000 to 32099 Server error: Reserved for implementation-defined server-errors.
	Code int `json:"code"`
	// JSON-RPC 2.0 error message
	Message string `json:"message"`
	// JSON-RPC 2.0 error data
	Data any `json:"data,omitempty"`
}

// ErrInvalidRequest represents an invalid request error. The JSON sent is not a valid Request object.
type ErrInvalidRequest struct {
	err error
}

// Error returns the error message.
func (e ErrInvalidRequest) Error() string {
	return fmt.Sprintf("invalid request: %v", e.err)
}

// Is checks if the error is an ErrInvalidRequest.
func (e ErrInvalidRequest) Is(target error) bool {
	var errInvalidRequest ErrInvalidRequest
	return errors.As(target, &errInvalidRequest) || errors.Is(e.err, target)
}

// JSONRPCErrorObject returns a JSON-RPC 2.0 error object.
func (e ErrInvalidRequest) JSONRPCErrorObject() *jsonrpcErrorObject {
	return &jsonrpcErrorObject{
		Code:    -32600,
		Message: e.Error(),
		Data:    e.err,
	}
}

// ErrMethodNotFound represents a method not found error. The method does not exist / is not available.
type ErrMethodNotFound struct {
	err error
}

func (e ErrMethodNotFound) Error() string {
	return fmt.Sprintf("method not found: %v", e.err)
}

func (e ErrMethodNotFound) Is(target error) bool {
	var errMethodNotFound ErrMethodNotFound
	return errors.As(target, &errMethodNotFound) || errors.Is(e.err, target)
}

func (e ErrMethodNotFound) JSONRPCErrorObject() *jsonrpcErrorObject {
	return &jsonrpcErrorObject{
		Code:    -32601,
		Message: e.Error(),
		Data:    e.err,
	}
}

// ErrInvalidParams represents an invalid params error. Invalid method parameter(s).
type ErrInvalidParams struct {
	err error
}

func (e ErrInvalidParams) Error() string {
	return fmt.Sprintf("invalid params: %v", e.err)
}

func (e ErrInvalidParams) Is(target error) bool {
	var errInvalidParams ErrInvalidParams
	return errors.As(target, &errInvalidParams) || errors.Is(e.err, target)
}

func (e ErrInvalidParams) JSONRPCErrorObject() *jsonrpcErrorObject {
	return &jsonrpcErrorObject{
		Code:    -32602,
		Message: e.Error(),
		Data:    e.err,
	}
}

// ErrInternalError represents an internal error. Internal JSON-RPC error.
type ErrInternalError struct {
	err error
}

func (e ErrInternalError) Error() string {
	return fmt.Sprintf("internal error: %v", e.err)
}

func (e ErrInternalError) Is(target error) bool {
	var errInternalError ErrInternalError
	return errors.As(target, &errInternalError) || errors.Is(e.err, target)
}

func (e ErrInternalError) JSONRPCErrorObject() *jsonrpcErrorObject {
	return &jsonrpcErrorObject{
		Code:    -32603,
		Message: e.Error(),
		Data:    e.err,
	}
}

// ErrParseError represents a parse error. Invalid JSON was received by the server.
type ErrParseError struct {
	err error
}

func (e ErrParseError) Error() string {
	return fmt.Sprintf("parse error: %v", e.err)
}

func (e ErrParseError) Is(target error) bool {
	var errParseError ErrParseError
	return errors.As(target, &errParseError) || errors.Is(e.err, target)
}

func (e ErrParseError) JSONRPCErrorObject() *jsonrpcErrorObject {
	return &jsonrpcErrorObject{
		Code:    -32700,
		Message: e.Error(),
		Data:    e.err,
	}
}

// ErrServerError represents a server error. Reserved for implementation-defined server-errors.
type ErrServerError struct {
	err error
}

func (e ErrServerError) Error() string {
	return fmt.Sprintf("server error: %v", e.err)
}

func (e ErrServerError) Is(target error) bool {
	var errServerError ErrServerError
	return errors.As(target, &errServerError) || errors.Is(e.err, target)
}

func (e ErrServerError) JSONRPCErrorObject() *jsonrpcErrorObject {
	return &jsonrpcErrorObject{
		Code:    -32000,
		Message: e.Error(),
		Data:    e.err,
	}
}

// ErrUnauthenticated represents an unauthenticated error.
type ErrUnauthenticated struct {
	err error
}

func (e ErrUnauthenticated) Error() string {
	return fmt.Sprintf("unauthenticated: %v", e.err)
}

func (e ErrUnauthenticated) Is(target error) bool {
	return errors.Is(e.err, target)
}

func (e ErrUnauthenticated) JSONRPCErrorObject() *jsonrpcErrorObject {
	return &jsonrpcErrorObject{
		Code:    -32001,
		Message: e.Error(),
		Data:    e.err,
	}
}

// ErrPermissionDenied represents a permission denied error.
type ErrPermissionDenied struct {
	err error
}

func (e ErrPermissionDenied) Error() string {
	return fmt.Sprintf("permission denied: %v", e.err)
}

func (e ErrPermissionDenied) Is(target error) bool {
	return errors.Is(e.err, target)
}

func (e ErrPermissionDenied) JSONRPCErrorObject() *jsonrpcErrorObject {
	return &jsonrpcErrorObject{
		Code:    -32003,
		Message: e.Error(),
		Data:    e.err,
	}
}

// ErrUnimplemented represents an unimplemented error.
type ErrUnimplemented struct {
	err error
}

func (e ErrUnimplemented) Error() string {
	return fmt.Sprintf("unimplemented: %v", e.err)
}

func (e ErrUnimplemented) Is(target error) bool {
	return errors.Is(e.err, target)
}

func (e ErrUnimplemented) JSONRPCErrorObject() *jsonrpcErrorObject {
	return &jsonrpcErrorObject{
		Code:    -32601,
		Message: e.Error(),
		Data:    e.err,
	}
}

// ErrNotFound represents a not found error.
type ErrNotFound struct {
	err error
}

func (e ErrNotFound) Error() string {
	return fmt.Sprintf("not found: %v", e.err)
}

func (e ErrNotFound) Is(target error) bool {
	return errors.Is(e.err, target)
}

func (e ErrNotFound) JSONRPCErrorObject() *jsonrpcErrorObject {
	return &jsonrpcErrorObject{
		Code:    -32004,
		Message: e.Error(),
		Data:    e.err,
	}
}
