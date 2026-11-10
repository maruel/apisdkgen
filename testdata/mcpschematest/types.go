// Package mcpschematest supplies JSON-RPC wire types for generated-client integration tests.
package mcpschematest

import "encoding/json"

type Method string

const MethodDiscovery Method = "server/discover"

type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitzero"`
	Method  Method          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}
type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitzero"`
	Result  any             `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}
type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
type JSONRPCNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
}
type ClientCapabilities struct{}
type Implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}
type RequestMeta struct {
	ProtocolVersion    string             `json:"io.modelcontextprotocol/protocolVersion"`
	ClientInfo         Implementation     `json:"io.modelcontextprotocol/clientInfo"`
	ClientCapabilities ClientCapabilities `json:"io.modelcontextprotocol/clientCapabilities"`
}
type ServerDiscoverResult struct {
	Instructions string `json:"instructions,omitempty"`
}

type RequiredRequest struct {
	Version   string          `json:"jsonrpc"`
	CallID    json.RawMessage `json:"id"`
	Operation Method          `json:"method"`
	Arguments json.RawMessage `json:"params"`
}
type RequiredResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Payload any             `json:"result"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}
