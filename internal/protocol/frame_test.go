package protocol

import (
	"bytes"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	payload := []byte("hello sql tunnel")
	msg, err := Encode(TypeData, 42, payload)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	frame, err := Decode(msg)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if frame.Type != TypeData {
		t.Fatalf("Type = %d, want %d", frame.Type, TypeData)
	}
	if frame.StreamID != 42 {
		t.Fatalf("StreamID = %d, want 42", frame.StreamID)
	}
	if !bytes.Equal(frame.Payload, payload) {
		t.Fatalf("Payload = %q, want %q", frame.Payload, payload)
	}
}

func TestDecodeRejectsLengthMismatch(t *testing.T) {
	msg, err := Encode(TypeData, 1, []byte("abc"))
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	msg = msg[:len(msg)-1]

	if _, err := Decode(msg); err == nil {
		t.Fatal("Decode() error = nil, want error")
	}
}
