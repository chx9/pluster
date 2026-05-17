package proto

import (
	"bytes"
	"strings"
	"testing"
)

func TestCRC16(t *testing.T) {
	cases := []struct {
		key  string
		want uint16
	}{
		{"foo", 0xb6da},
		{"bar", 0x4a89},
		{"hello", 0xe595},
		{"", 0},
	}
	for _, tc := range cases {
		got := CRC16([]byte(tc.key))
		if got != tc.want {
			t.Errorf("CRC16(%q) = 0x%04x, want 0x%04x", tc.key, got, tc.want)
		}
	}
}

func TestHashSlot(t *testing.T) {
	cases := []struct {
		key      string
		hashPart string
	}{
		{"foo", "foo"},
		{"{foo}.bar", "foo"},
		{"{foo}", "foo"},
		{"{{foo}}", "foo"},
		{"foo{}{bar}", "bar"},
	}
	for _, tc := range cases {
		want := int(CRC16([]byte(tc.hashPart)) % ClusterSlots)
		got := HashSlot([]byte(tc.key))
		if got != want {
			t.Errorf("HashSlot(%q) = %d, want %d (hash of %q)", tc.key, got, want, tc.hashPart)
		}
	}
}

func TestHashSlotHashTag(t *testing.T) {
	if HashSlot([]byte("{foo}.bar")) != HashSlot([]byte("{foo}.baz")) {
		t.Error("keys with same hash tag should map to same slot")
	}
	_ = HashSlot([]byte("foo")) == HashSlot([]byte("bar"))
}

func TestReadSimpleString(t *testing.T) {
	r := NewReader(strings.NewReader("+OK\r\n"))
	v, err := r.ReadValue()
	if err != nil {
		t.Fatal(err)
	}
	if v.Type != TypeSimpleString {
		t.Errorf("expected SimpleString, got %c", v.Type)
	}
	if string(v.Str) != "OK" {
		t.Errorf("expected OK, got %s", v.Str)
	}
}

func TestReadError(t *testing.T) {
	r := NewReader(strings.NewReader("-ERR something\r\n"))
	v, err := r.ReadValue()
	if err != nil {
		t.Fatal(err)
	}
	if !v.IsError() {
		t.Error("expected error value")
	}
	if v.Error() != "ERR something" {
		t.Errorf("expected 'ERR something', got %q", v.Error())
	}
}

func TestReadInteger(t *testing.T) {
	r := NewReader(strings.NewReader(":42\r\n"))
	v, err := r.ReadValue()
	if err != nil {
		t.Fatal(err)
	}
	if v.Type != TypeInteger || v.Integer != 42 {
		t.Errorf("expected integer 42, got %v", v)
	}
}

func TestReadBulkString(t *testing.T) {
	r := NewReader(strings.NewReader("$5\r\nhello\r\n"))
	v, err := r.ReadValue()
	if err != nil {
		t.Fatal(err)
	}
	if v.Type != TypeBulkString || string(v.Str) != "hello" {
		t.Errorf("expected bulk 'hello', got %v", v)
	}
}

func TestReadNilBulkString(t *testing.T) {
	r := NewReader(strings.NewReader("$-1\r\n"))
	v, err := r.ReadValue()
	if err != nil {
		t.Fatal(err)
	}
	if !v.IsNil {
		t.Error("expected nil bulk string")
	}
}

func TestReadArray(t *testing.T) {
	r := NewReader(strings.NewReader("*3\r\n$3\r\nfoo\r\n$3\r\nbar\r\n$3\r\nbaz\r\n"))
	v, err := r.ReadValue()
	if err != nil {
		t.Fatal(err)
	}
	if v.Type != TypeArray || len(v.Array) != 3 {
		t.Errorf("expected array of 3, got %v", v)
	}
	if string(v.Array[0].Str) != "foo" {
		t.Errorf("expected foo, got %s", v.Array[0].Str)
	}
}

func TestReadNilArray(t *testing.T) {
	r := NewReader(strings.NewReader("*-1\r\n"))
	v, err := r.ReadValue()
	if err != nil {
		t.Fatal(err)
	}
	if !v.IsNil {
		t.Error("expected nil array")
	}
}

func TestReadRequest(t *testing.T) {
	r := NewReader(strings.NewReader("*3\r\n$3\r\nSET\r\n$3\r\nfoo\r\n$3\r\nbar\r\n"))
	req, err := r.ReadRequest()
	if err != nil {
		t.Fatal(err)
	}
	if req.Name() != "SET" {
		t.Errorf("expected SET, got %s", req.Name())
	}
	if len(req.Args) != 3 {
		t.Errorf("expected 3 args, got %d", len(req.Args))
	}
}

func TestWriteReadRoundtrip(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)

	_ = w.WriteSimpleString("PONG")
	_ = w.WriteError("ERR test error")
	_ = w.WriteInteger(123)
	_ = w.WriteBulkString([]byte("hello world"))
	_ = w.WriteNilBulkString()
	_ = w.Flush()

	r := NewReader(&buf)

	v, _ := r.ReadValue()
	if string(v.Str) != "PONG" {
		t.Errorf("expected PONG, got %s", v.Str)
	}

	v, _ = r.ReadValue()
	if !v.IsError() || v.Error() != "ERR test error" {
		t.Errorf("expected error, got %v", v)
	}

	v, _ = r.ReadValue()
	if v.Integer != 123 {
		t.Errorf("expected 123, got %d", v.Integer)
	}

	v, _ = r.ReadValue()
	if string(v.Str) != "hello world" {
		t.Errorf("expected 'hello world', got %s", v.Str)
	}

	v, _ = r.ReadValue()
	if !v.IsNil {
		t.Error("expected nil")
	}
}

func TestWriteCommand(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	_ = w.WriteCommand("GET", "mykey")
	_ = w.Flush()

	r := NewReader(&buf)
	req, err := r.ReadRequest()
	if err != nil {
		t.Fatal(err)
	}
	if req.Name() != "GET" || string(req.Args[1]) != "mykey" {
		t.Errorf("unexpected request: %v", req.Args)
	}
}

func TestMovedError(t *testing.T) {
	v := &Value{Type: TypeError, Str: []byte("MOVED 1234 127.0.0.1:7001")}
	if !v.IsMovedError() {
		t.Error("expected MOVED error")
	}
	slot, addr, err := v.ParseRedirection()
	if err != nil {
		t.Fatal(err)
	}
	if slot != 1234 || addr != "127.0.0.1:7001" {
		t.Errorf("unexpected redirection: slot=%d addr=%s", slot, addr)
	}
}

func TestAskError(t *testing.T) {
	v := &Value{Type: TypeError, Str: []byte("ASK 5678 127.0.0.1:7002")}
	if !v.IsAskError() {
		t.Error("expected ASK error")
	}
	slot, addr, err := v.ParseRedirection()
	if err != nil {
		t.Fatal(err)
	}
	if slot != 5678 || addr != "127.0.0.1:7002" {
		t.Errorf("unexpected redirection: slot=%d addr=%s", slot, addr)
	}
}

func TestBinaryData(t *testing.T) {
	data := []byte("hello\x00\r\nworld")
	var buf bytes.Buffer
	w := NewWriter(&buf)
	_ = w.WriteBulkString(data)
	_ = w.Flush()

	r := NewReader(&buf)
	v, err := r.ReadValue()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(v.Str, data) {
		t.Errorf("binary data mismatch: got %v, want %v", v.Str, data)
	}
}

func TestEncodeCommand(t *testing.T) {
	b := EncodeCommand("SET", "key", "value")
	r := NewReader(bytes.NewReader(b))
	req, err := r.ReadRequest()
	if err != nil {
		t.Fatal(err)
	}
	if req.Name() != "SET" || string(req.Args[1]) != "key" || string(req.Args[2]) != "value" {
		t.Errorf("unexpected: %v", req.Args)
	}
}
