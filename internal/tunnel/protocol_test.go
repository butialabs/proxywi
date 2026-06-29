package tunnel

import (
	"encoding/json"
	"testing"
)

func TestValidateHandshake_AcceptsWithAgentVersion(t *testing.T) {
	hs := &Handshake{
		Version:      ProtocolVersion,
		Token:        "valid-token",
		AgentVersion: "1.2.3",
	}
	if err := ValidateHandshake(hs); err != nil {
		t.Fatalf("expected valid handshake, got %v", err)
	}
}

func TestValidateHandshake_RequiresToken(t *testing.T) {
	hs := &Handshake{Version: ProtocolVersion}
	if err := ValidateHandshake(hs); err == nil {
		t.Fatal("expected error for empty token")
	}
}

func TestValidateHandshake_RequiresVersion(t *testing.T) {
	hs := &Handshake{Version: 999, Token: "token"}
	if err := ValidateHandshake(hs); err == nil {
		t.Fatal("expected error for unsupported version")
	}
}

func TestHandshakeJSON_KeepsAgentVersion(t *testing.T) {
	hs := Handshake{Version: ProtocolVersion, Token: "t", AgentVersion: "v1"}
	b, err := json.Marshal(hs)
	if err != nil {
		t.Fatal(err)
	}
	var got Handshake
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.AgentVersion != hs.AgentVersion {
		t.Fatalf("agent_version mismatch: got %q, want %q", got.AgentVersion, hs.AgentVersion)
	}
}
