package proto

import (
	"bytes"
	"strings"
	"testing"
)

func TestHashSlotEdgeCases(t *testing.T) {
	cases := []struct {
		key      string
		hashPart string
		desc     string
	}{
		{"foo{}", "foo{}", "empty braces use full key"},
		{"{{foo}}", "foo", "nested braces inner tag wins"},
		{"{foo}{bar}", "foo", "first valid hash tag wins"},
		{"{foo", "{foo", "no closing brace uses full key"},
		{"foo{", "foo{", "open brace at end uses full key"},
		{"a", "a", "single char key"},
		{"", "", "empty key"},
		{"{}", "{}", "only empty braces uses full key"},
		{"foo{bar}", "bar", "hash tag at end of key"},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			want := int(CRC16([]byte(tc.hashPart)) % ClusterSlots)
			got := HashSlot([]byte(tc.key))
			if got != want {
				t.Errorf("HashSlot(%q) = %d, want %d (hash of %q)", tc.key, got, want, tc.hashPart)
			}
		})
	}
}

func TestCRC16AdditionalKnownValues(t *testing.T) {
	cases := []struct {
		key  string
		want uint16
	}{
		{"a", 0x7c47},
		{"z", 0xdfdd},
	}
	for _, tc := range cases {
		got := CRC16([]byte(tc.key))
		if got != tc.want {
			t.Errorf("CRC16(%q) = 0x%04x, want 0x%04x", tc.key, got, tc.want)
		}
	}

	first := CRC16([]byte("123456789"))
	for i := 0; i < 10; i++ {
		got := CRC16([]byte("123456789"))
		if got != first {
			t.Errorf("CRC16(\"123456789\") run %d = 0x%04x, want 0x%04x (not deterministic)", i, got, first)
		}
	}
}

func TestReadWriteLargeValue(t *testing.T) {
	largeVal := bytes.Repeat([]byte("x"), 1024*1024)
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if err := w.WriteBulkString(largeVal); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}

	r := NewReader(&buf)
	v, err := r.ReadValue()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(v.Str, largeVal) {
		t.Errorf("large value mismatch: len=%d want=%d", len(v.Str), len(largeVal))
	}
}

func TestReadWriteBinaryInArray(t *testing.T) {
	items := [][]byte{
		[]byte("hello\x00world"),
		[]byte("\r\n"),
		[]byte("\x00\r\n\x00"),
		[]byte("normal"),
	}
	arrayVals := make([]*Value, len(items))
	for i, item := range items {
		arrayVals[i] = &Value{Type: TypeBulkString, Str: item}
	}

	var buf bytes.Buffer
	w := NewWriter(&buf)
	if err := w.WriteArray(arrayVals); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}

	r := NewReader(&buf)
	v, err := r.ReadValue()
	if err != nil {
		t.Fatal(err)
	}
	if v.Type != TypeArray || len(v.Array) != len(items) {
		t.Fatalf("expected array of %d, got type=%c len=%d", len(items), v.Type, len(v.Array))
	}
	for i, item := range items {
		if !bytes.Equal(v.Array[i].Str, item) {
			t.Errorf("item[%d]: got %q, want %q", i, v.Array[i].Str, item)
		}
	}
}

func TestReadMultipleValues(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	n := 20
	for i := 0; i < n; i++ {
		_ = w.WriteCommand("SET", "key", "val")
	}
	_ = w.Flush()

	r := NewReader(&buf)
	for i := 0; i < n; i++ {
		req, err := r.ReadRequest()
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		if req.Name() != "SET" {
			t.Errorf("request %d: expected SET, got %s", i, req.Name())
		}
	}
}

func TestReadSplitBuffer(t *testing.T) {
	cmd := "*3\r\n$3\r\nSET\r\n$3\r\nfoo\r\n$3\r\nbar\r\n"

	for splitAt := 1; splitAt < len(cmd); splitAt++ {
		combined := strings.NewReader(cmd[:splitAt] + cmd[splitAt:])
		r := NewReader(combined)
		req, err := r.ReadRequest()
		if err != nil {
			t.Errorf("split at %d: unexpected error: %v", splitAt, err)
			continue
		}
		if req.Name() != "SET" {
			t.Errorf("split at %d: expected SET, got %s", splitAt, req.Name())
		}
		if string(req.Args[1]) != "foo" {
			t.Errorf("split at %d: expected foo, got %s", splitAt, req.Args[1])
		}
		if string(req.Args[2]) != "bar" {
			t.Errorf("split at %d: expected bar, got %s", splitAt, req.Args[2])
		}
	}
}

func TestParsePipelinedRequests(t *testing.T) {
	type cmdSpec struct {
		name string
		args []string
	}
	cmds := []cmdSpec{
		{"SET", []string{"k1", "v1"}},
		{"GET", []string{"k1"}},
		{"DEL", []string{"k1"}},
		{"PING", nil},
	}

	var buf bytes.Buffer
	w := NewWriter(&buf)
	for _, cmd := range cmds {
		all := append([]string{cmd.name}, cmd.args...)
		_ = w.WriteCommand(all...)
	}
	_ = w.Flush()

	r := NewReader(&buf)
	for i, cmd := range cmds {
		req, err := r.ReadRequest()
		if err != nil {
			t.Fatalf("cmd %d: unexpected error: %v", i, err)
		}
		if req.Name() != cmd.name {
			t.Errorf("cmd %d: expected %s, got %s", i, cmd.name, req.Name())
		}
	}
}

func TestWriteArrayOfValues(t *testing.T) {
	vals := []*Value{
		{Type: TypeBulkString, Str: []byte("a")},
		{Type: TypeBulkString, Str: []byte("b")},
		{Type: TypeBulkString, Str: []byte("c")},
	}
	var buf bytes.Buffer
	w := NewWriter(&buf)
	_ = w.WriteArray(vals)
	_ = w.Flush()

	r := NewReader(&buf)
	v, err := r.ReadValue()
	if err != nil {
		t.Fatal(err)
	}
	if v.Type != TypeArray || len(v.Array) != 3 {
		t.Fatalf("expected array of 3, got %v", v)
	}
	for i, want := range []string{"a", "b", "c"} {
		if string(v.Array[i].Str) != want {
			t.Errorf("item[%d]: expected %s, got %s", i, want, v.Array[i].Str)
		}
	}
}

func TestMovedAskParseEdgeCases(t *testing.T) {
	cases := []struct {
		raw     string
		isMoved bool
		isAsk   bool
		slot    int
		addr    string
	}{
		{"MOVED 0 127.0.0.1:7000", true, false, 0, "127.0.0.1:7000"},
		{"MOVED 16383 10.0.0.1:6379", true, false, 16383, "10.0.0.1:6379"},
		{"ASK 0 127.0.0.1:7001", false, true, 0, "127.0.0.1:7001"},
		{"ASK 8192 192.168.1.1:7002", false, true, 8192, "192.168.1.1:7002"},
	}
	for _, tc := range cases {
		v := &Value{Type: TypeError, Str: []byte(tc.raw)}
		if v.IsMovedError() != tc.isMoved {
			t.Errorf("%q: IsMovedError() = %v, want %v", tc.raw, v.IsMovedError(), tc.isMoved)
		}
		if v.IsAskError() != tc.isAsk {
			t.Errorf("%q: IsAskError() = %v, want %v", tc.raw, v.IsAskError(), tc.isAsk)
		}
		slot, addr, err := v.ParseRedirection()
		if err != nil {
			t.Errorf("%q: ParseRedirection error: %v", tc.raw, err)
			continue
		}
		if slot != tc.slot {
			t.Errorf("%q: slot = %d, want %d", tc.raw, slot, tc.slot)
		}
		if addr != tc.addr {
			t.Errorf("%q: addr = %s, want %s", tc.raw, addr, tc.addr)
		}
	}
}

func TestHashSlotConsistency(t *testing.T) {
	tag := "user:1"
	keys := []string{
		"{user:1}:name",
		"{user:1}:email",
		"{user:1}:age",
		"{user:1}:profile",
		"{user:1}:settings",
	}
	baseSlot := HashSlot([]byte(keys[0]))
	tagSlot := int(CRC16([]byte(tag)) % ClusterSlots)
	if baseSlot != tagSlot {
		t.Errorf("first key slot %d != tag slot %d", baseSlot, tagSlot)
	}
	for _, k := range keys[1:] {
		slot := HashSlot([]byte(k))
		if slot != baseSlot {
			t.Errorf("key %q: slot %d != base slot %d", k, slot, baseSlot)
		}
	}
}
