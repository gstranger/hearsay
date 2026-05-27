package a2a

import (
	"encoding/json"
	"testing"
)

func TestJSONRPCEncodeDecode(t *testing.T) {
	req := JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: "tasks/send", Params: json.RawMessage(`{"id":"t1"}`)}
	data, _ := json.Marshal(req)
	var decoded JSONRPCRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Method != "tasks/send" {
		t.Fatal("method mismatch")
	}

	resp := NewResponse(1, map[string]string{"id": "t1"})
	data, _ = json.Marshal(resp)
	var decodedResp JSONRPCResponse
	if err := json.Unmarshal(data, &decodedResp); err != nil {
		t.Fatal(err)
	}
	if decodedResp.Error != nil {
		t.Fatal("unexpected error")
	}
}

func TestNewError(t *testing.T) {
	err := NewError(-32602, "Invalid params", map[string]string{"detail": "missing id"})
	if err.Code != -32602 {
		t.Fatal("code mismatch")
	}
	if err.Message != "Invalid params" {
		t.Fatal("message mismatch")
	}
	if err.Data == nil {
		t.Fatal("expected data")
	}
}
