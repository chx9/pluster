// Package proto implements the Redis Serialization Protocol (RESP).
// It supports both RESP2 and inline commands.
package proto

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"unsafe"
)

// Types of RESP values
const (
	TypeSimpleString = '+'
	TypeError        = '-'
	TypeInteger      = ':'
	TypeBulkString   = '$'
	TypeArray        = '*'
)

// Common errors
var (
	ErrInvalidProtocol = errors.New("invalid RESP protocol")
	ErrUnexpectedEOF   = errors.New("unexpected EOF")
	ErrNilValue        = errors.New("nil value")
)

// Value represents a RESP value.
type Value struct {
	Type    byte
	Str     []byte   // for SimpleString, Error, BulkString
	Integer int64    // for Integer
	Array   []*Value // for Array
	IsNil   bool     // for nil bulk string or nil array
}

// String returns string representation of Value.
func (v *Value) String() string {
	if v == nil || v.IsNil {
		return ""
	}
	switch v.Type {
	case TypeSimpleString, TypeError, TypeBulkString:
		return string(v.Str)
	case TypeInteger:
		return strconv.FormatInt(v.Integer, 10)
	case TypeArray:
		if len(v.Array) == 0 {
			return "[]"
		}
		return fmt.Sprintf("[%d elements]", len(v.Array))
	}
	return ""
}

// IsError returns true if the value is an error.
func (v *Value) IsError() bool {
	return v != nil && v.Type == TypeError
}

// Error returns the error message if this is an error value.
func (v *Value) Error() string {
	if v.IsError() {
		return string(v.Str)
	}
	return ""
}

// IsMovedError checks if this is a MOVED redirection error.
func (v *Value) IsMovedError() bool {
	return v.IsError() && bytes.HasPrefix(v.Str, []byte("MOVED "))
}

// IsAskError checks if this is an ASK redirection error.
func (v *Value) IsAskError() bool {
	return v.IsError() && bytes.HasPrefix(v.Str, []byte("ASK "))
}

// IsClusterDownError checks if this is a CLUSTERDOWN error.
func (v *Value) IsClusterDownError() bool {
	return v.IsError() && bytes.HasPrefix(v.Str, []byte("CLUSTERDOWN"))
}

// IsLoadingError checks if this is a LOADING error.
func (v *Value) IsLoadingError() bool {
	return v.IsError() && bytes.HasPrefix(v.Str, []byte("LOADING"))
}

// ParseRedirection parses MOVED or ASK error and returns slot and addr.
func (v *Value) ParseRedirection() (slot int, addr string, err error) {
	if !v.IsError() {
		return 0, "", fmt.Errorf("not an error value")
	}
	parts := bytes.SplitN(v.Str, []byte(" "), 3)
	if len(parts) != 3 {
		return 0, "", fmt.Errorf("invalid redirection: %s", v.Str)
	}
	s, e := strconv.Atoi(string(parts[1]))
	if e != nil {
		return 0, "", fmt.Errorf("invalid slot in redirection: %s", v.Str)
	}
	return s, string(parts[2]), nil
}

// Reader reads RESP values from an io.Reader.
type Reader struct {
	r *bufio.Reader
}

// NewReader creates a new RESP reader.
func NewReader(r io.Reader) *Reader {
	return &Reader{r: bufio.NewReaderSize(r, 64*1024)}
}

// NewReaderSize creates a new RESP reader with specified buffer size.
func NewReaderSize(r io.Reader, size int) *Reader {
	return &Reader{r: bufio.NewReaderSize(r, size)}
}

// ReadValue reads a single RESP value.
func (r *Reader) ReadValue() (*Value, error) {
	b, err := r.r.ReadByte()
	if err != nil {
		return nil, err
	}
	return r.readValue(b)
}

func (r *Reader) readValue(typeByte byte) (*Value, error) {
	switch typeByte {
	case TypeSimpleString:
		return r.readSimpleString()
	case TypeError:
		return r.readError()
	case TypeInteger:
		return r.readInteger()
	case TypeBulkString:
		return r.readBulkString()
	case TypeArray:
		return r.readArray()
	default:
		// Inline command
		return r.readInline(typeByte)
	}
}

func (r *Reader) readLine() ([]byte, error) {
	line, err := r.r.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	if len(line) < 2 || line[len(line)-2] != '\r' {
		return nil, ErrInvalidProtocol
	}
	return line[:len(line)-2], nil
}

func (r *Reader) readSimpleString() (*Value, error) {
	line, err := r.readLine()
	if err != nil {
		return nil, err
	}
	return &Value{Type: TypeSimpleString, Str: line}, nil
}

func (r *Reader) readError() (*Value, error) {
	line, err := r.readLine()
	if err != nil {
		return nil, err
	}
	return &Value{Type: TypeError, Str: line}, nil
}

func (r *Reader) readInteger() (*Value, error) {
	line, err := r.readLine()
	if err != nil {
		return nil, err
	}
	n, err := strconv.ParseInt(string(line), 10, 64)
	if err != nil {
		return nil, ErrInvalidProtocol
	}
	return &Value{Type: TypeInteger, Integer: n}, nil
}

func (r *Reader) readBulkString() (*Value, error) {
	line, err := r.readLine()
	if err != nil {
		return nil, err
	}
	n, err := strconv.Atoi(string(line))
	if err != nil {
		return nil, ErrInvalidProtocol
	}
	if n < 0 {
		return &Value{Type: TypeBulkString, IsNil: true}, nil
	}
	buf := make([]byte, n+2) // +2 for \r\n
	if _, err := io.ReadFull(r.r, buf); err != nil {
		return nil, err
	}
	return &Value{Type: TypeBulkString, Str: buf[:n]}, nil
}

func (r *Reader) readArray() (*Value, error) {
	line, err := r.readLine()
	if err != nil {
		return nil, err
	}
	n, err := strconv.Atoi(string(line))
	if err != nil {
		return nil, ErrInvalidProtocol
	}
	if n < 0 {
		return &Value{Type: TypeArray, IsNil: true}, nil
	}
	arr := make([]*Value, n)
	for i := 0; i < n; i++ {
		b, err := r.r.ReadByte()
		if err != nil {
			return nil, err
		}
		v, err := r.readValue(b)
		if err != nil {
			return nil, err
		}
		arr[i] = v
	}
	return &Value{Type: TypeArray, Array: arr}, nil
}

func (r *Reader) readInline(first byte) (*Value, error) {
	// Read the rest of the line
	line, err := r.r.ReadBytes('\n')
	if err != nil && err != io.EOF {
		return nil, err
	}
	// Build full line including first byte
	full := make([]byte, 0, 1+len(line))
	full = append(full, first)
	full = append(full, line...)

	// Trim \r\n
	full = bytes.TrimRight(full, "\r\n")
	if len(full) == 0 {
		return nil, ErrInvalidProtocol
	}

	// Parse inline command into array
	parts := bytes.Fields(full)
	if len(parts) == 0 {
		return nil, ErrInvalidProtocol
	}

	arr := make([]*Value, len(parts))
	for i, p := range parts {
		arr[i] = &Value{Type: TypeBulkString, Str: p}
	}
	return &Value{Type: TypeArray, Array: arr}, nil
}

// Writer writes RESP values to an io.Writer.
type Writer struct {
	w *bufio.Writer
}

// NewWriter creates a new RESP writer.
func NewWriter(w io.Writer) *Writer {
	return &Writer{w: bufio.NewWriterSize(w, 64*1024)}
}

// NewWriterSize creates a new RESP writer with specified buffer size.
func NewWriterSize(w io.Writer, size int) *Writer {
	return &Writer{w: bufio.NewWriterSize(w, size)}
}

// WriteValue writes a RESP value.
func (w *Writer) WriteValue(v *Value) error {
	if v == nil {
		return w.WriteNilBulkString()
	}
	switch v.Type {
	case TypeSimpleString:
		return w.WriteSimpleString(string(v.Str))
	case TypeError:
		return w.WriteError(string(v.Str))
	case TypeInteger:
		return w.WriteInteger(v.Integer)
	case TypeBulkString:
		if v.IsNil {
			return w.WriteNilBulkString()
		}
		return w.WriteBulkString(v.Str)
	case TypeArray:
		if v.IsNil {
			return w.WriteNilArray()
		}
		return w.WriteArray(v.Array)
	}
	return fmt.Errorf("unknown value type: %c", v.Type)
}

// WriteSimpleString writes a simple string.
func (w *Writer) WriteSimpleString(s string) error {
	if err := w.w.WriteByte('+'); err != nil {
		return err
	}
	if _, err := w.w.WriteString(s); err != nil {
		return err
	}
	_, err := w.w.WriteString("\r\n")
	return err
}

func (w *Writer) WriteError(msg string) error {
	if err := w.w.WriteByte('-'); err != nil {
		return err
	}
	if _, err := w.w.WriteString(msg); err != nil {
		return err
	}
	_, err := w.w.WriteString("\r\n")
	return err
}

func (w *Writer) WriteInteger(n int64) error {
	if err := w.w.WriteByte(':'); err != nil {
		return err
	}
	if _, err := w.w.WriteString(strconv.FormatInt(n, 10)); err != nil {
		return err
	}
	_, err := w.w.WriteString("\r\n")
	return err
}

func (w *Writer) WriteBulkString(data []byte) error {
	if err := w.w.WriteByte('$'); err != nil {
		return err
	}
	if _, err := w.w.WriteString(strconv.Itoa(len(data))); err != nil {
		return err
	}
	if _, err := w.w.WriteString("\r\n"); err != nil {
		return err
	}
	if _, err := w.w.Write(data); err != nil {
		return err
	}
	_, err := w.w.WriteString("\r\n")
	return err
}

// WriteNilBulkString writes a nil bulk string.
func (w *Writer) WriteNilBulkString() error {
	_, err := w.w.WriteString("$-1\r\n")
	return err
}

// WriteNilArray writes a nil array.
func (w *Writer) WriteNilArray() error {
	_, err := w.w.WriteString("*-1\r\n")
	return err
}

// WriteArray writes an array of values.
func (w *Writer) WriteArray(arr []*Value) error {
	if err := w.w.WriteByte('*'); err != nil {
		return err
	}
	if _, err := w.w.WriteString(strconv.Itoa(len(arr))); err != nil {
		return err
	}
	if _, err := w.w.WriteString("\r\n"); err != nil {
		return err
	}
	for _, v := range arr {
		if err := w.WriteValue(v); err != nil {
			return err
		}
	}
	return nil
}

func (w *Writer) WriteCommand(args ...string) error {
	if err := w.w.WriteByte('*'); err != nil {
		return err
	}
	if _, err := w.w.WriteString(strconv.Itoa(len(args))); err != nil {
		return err
	}
	if _, err := w.w.WriteString("\r\n"); err != nil {
		return err
	}
	for _, arg := range args {
		if err := w.WriteBulkString([]byte(arg)); err != nil {
			return err
		}
	}
	return nil
}

func (w *Writer) WriteCommandBytes(args ...[]byte) error {
	if err := w.w.WriteByte('*'); err != nil {
		return err
	}
	if _, err := w.w.WriteString(strconv.Itoa(len(args))); err != nil {
		return err
	}
	if _, err := w.w.WriteString("\r\n"); err != nil {
		return err
	}
	for _, arg := range args {
		if err := w.WriteBulkString(arg); err != nil {
			return err
		}
	}
	return nil
}

func (w *Writer) WriteRaw(data []byte) (int, error) {
	return w.w.Write(data)
}

func (w *Writer) Flush() error {
	return w.w.Flush()
}

// EncodeCommand encodes a command to bytes.
func EncodeCommand(args ...string) []byte {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	_ = w.WriteCommand(args...)
	_ = w.Flush()
	return buf.Bytes()
}

// Request represents a parsed Redis request (client → proxy).
type Request struct {
	Args [][]byte
	Raw  []byte
	Cmd  string
}

func (r *Request) Name() string { return r.Cmd }

func ErrValue(msg string) *Value {
	return &Value{Type: TypeError, Str: []byte(msg)}
}

func OKValue() *Value {
	return &Value{Type: TypeSimpleString, Str: []byte("OK")}
}

func IntValue(n int64) *Value {
	return &Value{Type: TypeInteger, Integer: n}
}

func BulkValue(data []byte) *Value {
	if data == nil {
		return &Value{Type: TypeBulkString, IsNil: true}
	}
	return &Value{Type: TypeBulkString, Str: data}
}

func ReadRequest(rdr *Reader) (*Request, error) {
	return rdr.ReadRequest()
}

var rawBufPool = sync.Pool{New: func() any { b := make([]byte, 0, 128); return &b }}

func ParseRequest(buf []byte) (*Request, int, error) {
	if len(buf) == 0 {
		return nil, 0, nil
	}
	rawPtr := rawBufPool.Get().(*[]byte)
	raw := (*rawPtr)[:0]
	val, n, err := parseRequestDirect(buf, &raw)
	if err == errIncomplete {
		*rawPtr = raw
		rawBufPool.Put(rawPtr)
		return nil, 0, nil
	}
	if err != nil {
		*rawPtr = raw
		rawBufPool.Put(rawPtr)
		return nil, 0, err
	}
	if val == nil {
		*rawPtr = raw
		rawBufPool.Put(rawPtr)
		return nil, 0, ErrInvalidProtocol
	}
	req := &Request{Args: val, Raw: raw}
	if len(val) > 0 {
		upperASCIIInPlace(val[0])
		req.Cmd = unsafe.String(unsafe.SliceData(val[0]), len(val[0]))
	}
	return req, n, nil
}

func upperASCIIInPlace(b []byte) {
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 32
		}
	}
}

func parseRequestDirect(buf []byte, raw *[]byte) ([][]byte, int, error) {
	if len(buf) == 0 {
		return nil, 0, errIncomplete
	}
	if buf[0] != TypeArray {
		return parseInlineRequest(buf, raw)
	}
	line, pos, err := readLine(buf, 1)
	if err != nil {
		return nil, 0, err
	}
	n, e := parseIntBytes(line)
	if e != nil || n <= 0 {
		return nil, 0, ErrInvalidProtocol
	}
	args := make([][]byte, n)
	cur := pos
	for i := int64(0); i < n; i++ {
		if cur >= len(buf) || buf[cur] != TypeBulkString {
			return nil, 0, errIncomplete
		}
		bline, bend, berr := readLine(buf, cur+1)
		if berr != nil {
			return nil, 0, berr
		}
		blen, be := parseIntBytes(bline)
		if be != nil || blen < 0 {
			return nil, 0, ErrInvalidProtocol
		}
		need := bend + int(blen) + 2
		if need > len(buf) {
			return nil, 0, errIncomplete
		}
		args[i] = buf[bend : bend+int(blen)]
		cur = need
	}
	rawOffset := len(*raw)
	*raw = append(*raw, buf[:cur]...)
	for i, a := range args {
		if len(a) == 0 {
			continue
		}
		argOffset := int(uintptr(unsafe.Pointer(&a[0])) - uintptr(unsafe.Pointer(&buf[0])))
		start := rawOffset + argOffset
		args[i] = (*raw)[start : start+len(a)]
	}
	return args, cur, nil
}

func parseInlineRequest(buf []byte, raw *[]byte) ([][]byte, int, error) {
	idx := bytes.IndexByte(buf, '\n')
	if idx < 0 {
		return nil, 0, errIncomplete
	}
	end := idx + 1
	line := buf[:idx]
	if len(line) > 0 && line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return nil, 0, ErrInvalidProtocol
	}
	parts := bytes.Fields(line)
	args := make([][]byte, len(parts))
	for i, p := range parts {
		cp := make([]byte, len(p))
		copy(cp, p)
		args[i] = cp
	}
	*raw = append(*raw, buf[:end]...)
	return args, end, nil
}

// ParseValue parses one RESP value from a byte slice without allocating any
// intermediate buffers. Returned string payloads reference the input slice.
// Returns (val, bytesConsumed, nil) on success.
// Returns (nil, 0, nil) if buf doesn't contain a complete value (need more data).
// Returns (nil, 0, err) on protocol error.
func ParseValue(buf []byte) (*Value, int, error) {
	if len(buf) == 0 {
		return nil, 0, nil
	}
	val, pos, err := parseValueAt(buf, 0)
	if err == errIncomplete {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	return val, pos, nil
}

var errIncomplete = errors.New("incomplete")

// ErrorKind classifies a RESP error for fast routing decisions.
type ErrorKind int

const (
	ErrorKindNone   ErrorKind = iota // not an error
	ErrorKindMoved                   // MOVED <slot> <addr>
	ErrorKindAsk                     // ASK <slot> <addr>
	ErrorKindOther                   // any other error
)

// ScanValue finds the end of the next complete RESP value in buf without
// allocating any heap memory. It returns:
//   - end: number of bytes consumed (0 if incomplete)
//   - kind: ErrorKindNone for non-errors, ErrorKindMoved/Ask/Other for "-" lines
//   - errMsg: slice into buf with the error message body (only valid when kind != None)
//
// The caller can forward buf[0:end] directly to the client without parsing.
func ScanValue(buf []byte) (end int, kind ErrorKind, errMsg []byte) {
	n, k, msg := scanValueAt(buf, 0)
	return n, k, msg
}

func scanValueAt(buf []byte, pos int) (end int, kind ErrorKind, errMsg []byte) {
	if pos >= len(buf) {
		return 0, ErrorKindNone, nil
	}
	typ := buf[pos]
	pos++
	switch typ {
	case TypeSimpleString, TypeInteger:
		line, endPos, err := readLine(buf, pos)
		if err != nil {
			return 0, ErrorKindNone, nil
		}
		_ = line
		return endPos, ErrorKindNone, nil
	case TypeError:
		line, endPos, err := readLine(buf, pos)
		if err != nil {
			return 0, ErrorKindNone, nil
		}
		k := ErrorKindOther
		if bytes.HasPrefix(line, []byte("MOVED ")) {
			k = ErrorKindMoved
		} else if bytes.HasPrefix(line, []byte("ASK ")) {
			k = ErrorKindAsk
		}
		return endPos, k, line
	case TypeBulkString:
		line, end2, err := readLine(buf, pos)
		if err != nil {
			return 0, ErrorKindNone, nil
		}
		n, e := parseIntBytes(line)
		if e != nil {
			return 0, ErrorKindNone, nil
		}
		if n < 0 {
			return end2, ErrorKindNone, nil
		}
		need := end2 + int(n) + 2
		if need > len(buf) {
			return 0, ErrorKindNone, nil
		}
		return need, ErrorKindNone, nil
	case TypeArray:
		line, cur, err := readLine(buf, pos)
		if err != nil {
			return 0, ErrorKindNone, nil
		}
		n, e := parseIntBytes(line)
		if e != nil {
			return 0, ErrorKindNone, nil
		}
		if n < 0 {
			return cur, ErrorKindNone, nil
		}
		for i := int64(0); i < n; i++ {
			next, _, _ := scanValueAt(buf, cur)
			if next == 0 {
				return 0, ErrorKindNone, nil
			}
			cur = next
		}
		return cur, ErrorKindNone, nil
	default:
		// inline: scan to newline
		idx := bytes.IndexByte(buf[pos-1:], '\n')
		if idx < 0 {
			return 0, ErrorKindNone, nil
		}
		return pos - 1 + idx + 1, ErrorKindNone, nil
	}
}

func parseValueAt(buf []byte, pos int) (*Value, int, error) {
	if pos >= len(buf) {
		return nil, 0, errIncomplete
	}
	typ := buf[pos]
	pos++
	switch typ {
	case TypeSimpleString:
		line, end, err := readLine(buf, pos)
		if err != nil {
			return nil, 0, err
		}
		cp := make([]byte, len(line))
		copy(cp, line)
		return &Value{Type: TypeSimpleString, Str: cp}, end, nil
	case TypeError:
		line, end, err := readLine(buf, pos)
		if err != nil {
			return nil, 0, err
		}
		cp := make([]byte, len(line))
		copy(cp, line)
		return &Value{Type: TypeError, Str: cp}, end, nil
	case TypeInteger:
		line, end, err := readLine(buf, pos)
		if err != nil {
			return nil, 0, err
		}
		n, e := parseIntBytes(line)
		if e != nil {
			return nil, 0, ErrInvalidProtocol
		}
		return &Value{Type: TypeInteger, Integer: n}, end, nil
	case TypeBulkString:
		line, end, err := readLine(buf, pos)
		if err != nil {
			return nil, 0, err
		}
		n, e := parseIntBytes(line)
		if e != nil {
			return nil, 0, ErrInvalidProtocol
		}
		if n < 0 {
			return &Value{Type: TypeBulkString, IsNil: true}, end, nil
		}
		need := end + int(n) + 2
		if need > len(buf) {
			return nil, 0, errIncomplete
		}
		data := make([]byte, n)
		copy(data, buf[end:end+int(n)])
		return &Value{Type: TypeBulkString, Str: data}, need, nil
	case TypeArray:
		line, end, err := readLine(buf, pos)
		if err != nil {
			return nil, 0, err
		}
		n, e := parseIntBytes(line)
		if e != nil {
			return nil, 0, ErrInvalidProtocol
		}
		if n < 0 {
			return &Value{Type: TypeArray, IsNil: true}, end, nil
		}
		arr := make([]*Value, n)
		cur := end
		for i := int64(0); i < n; i++ {
			v, next, verr := parseValueAt(buf, cur)
			if verr != nil {
				return nil, 0, verr
			}
			arr[i] = v
			cur = next
		}
		return &Value{Type: TypeArray, Array: arr}, cur, nil
	default:
		return parseInlineAt(buf, pos-1)
	}
}

func readLine(buf []byte, pos int) ([]byte, int, error) {
	idx := bytes.IndexByte(buf[pos:], '\n')
	if idx < 0 {
		return nil, 0, errIncomplete
	}
	end := pos + idx + 1
	line := buf[pos : pos+idx]
	if len(line) > 0 && line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}
	return line, end, nil
}

func parseIntBytes(b []byte) (int64, error) {
	if len(b) == 0 {
		return 0, ErrInvalidProtocol
	}
	neg := false
	i := 0
	if b[0] == '-' {
		neg = true
		i = 1
	}
	var n int64
	for ; i < len(b); i++ {
		c := b[i]
		if c < '0' || c > '9' {
			return 0, ErrInvalidProtocol
		}
		n = n*10 + int64(c-'0')
	}
	if neg {
		n = -n
	}
	return n, nil
}

func parseInlineAt(buf []byte, pos int) (*Value, int, error) {
	idx := bytes.IndexByte(buf[pos:], '\n')
	if idx < 0 {
		return nil, 0, errIncomplete
	}
	end := pos + idx + 1
	line := buf[pos : pos+idx]
	if len(line) > 0 && line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return nil, 0, ErrInvalidProtocol
	}
	parts := bytes.Fields(line)
	arr := make([]*Value, len(parts))
	for i, p := range parts {
		arr[i] = &Value{Type: TypeBulkString, Str: p}
	}
	return &Value{Type: TypeArray, Array: arr}, end, nil
}

var encBufPool = sync.Pool{New: func() any { b := make([]byte, 0, 256); return &b }}

func getEncBuf() []byte  { return (*encBufPool.Get().(*[]byte))[:0] }
func putEncBuf(b []byte) { encBufPool.Put(&b) }

func appendInt(b []byte, n int64) []byte {
	return strconv.AppendInt(b, n, 10)
}

func appendBulk(b, data []byte) []byte {
	b = append(b, '$')
	b = appendInt(b, int64(len(data)))
	b = append(b, '\r', '\n')
	b = append(b, data...)
	b = append(b, '\r', '\n')
	return b
}

func appendValue(b []byte, v *Value) []byte {
	if v == nil {
		return append(b, '$', '-', '1', '\r', '\n')
	}
	switch v.Type {
	case TypeSimpleString:
		b = append(b, '+')
		b = append(b, v.Str...)
		return append(b, '\r', '\n')
	case TypeError:
		b = append(b, '-')
		b = append(b, v.Str...)
		return append(b, '\r', '\n')
	case TypeInteger:
		b = append(b, ':')
		b = appendInt(b, v.Integer)
		return append(b, '\r', '\n')
	case TypeBulkString:
		if v.IsNil {
			return append(b, '$', '-', '1', '\r', '\n')
		}
		return appendBulk(b, v.Str)
	case TypeArray:
		if v.IsNil {
			return append(b, '*', '-', '1', '\r', '\n')
		}
		b = append(b, '*')
		b = appendInt(b, int64(len(v.Array)))
		b = append(b, '\r', '\n')
		for _, elem := range v.Array {
			b = appendValue(b, elem)
		}
		return b
	}
	return b
}

// EncodeValue encodes a RESP value to bytes.
func EncodeValue(v *Value) []byte {
	b := getEncBuf()
	b = appendValue(b, v)
	out := make([]byte, len(b))
	copy(out, b)
	putEncBuf(b)
	return out
}

// EncodeSimpleString encodes a simple string to bytes.
func EncodeSimpleString(s string) []byte {
	b := make([]byte, 0, 1+len(s)+2)
	b = append(b, '+')
	b = append(b, s...)
	b = append(b, '\r', '\n')
	return b
}

// EncodeError encodes an error to bytes.
func EncodeError(msg string) []byte {
	b := make([]byte, 0, 1+len(msg)+2)
	b = append(b, '-')
	b = append(b, msg...)
	b = append(b, '\r', '\n')
	return b
}

// EncodeInteger encodes an integer to RESP bytes.
func EncodeInteger(n int64) []byte {
	b := make([]byte, 0, 24)
	b = append(b, ':')
	b = strconv.AppendInt(b, n, 10)
	b = append(b, '\r', '\n')
	return b
}

// EncodeBulkString encodes a bulk string to bytes.
func EncodeBulkString(data []byte) []byte {
	b := make([]byte, 0, len(data)+16)
	return appendBulk(b, data)
}

// EncodeCommandBytes encodes a command from [][]byte args to RESP bytes.
func EncodeCommandBytes(args ...[]byte) []byte {
	size := 16
	for _, a := range args {
		size += 16 + len(a)
	}
	b := make([]byte, 0, size)
	b = append(b, '*')
	b = appendInt(b, int64(len(args)))
	b = append(b, '\r', '\n')
	for _, a := range args {
		b = appendBulk(b, a)
	}
	return b
}

func (r *Reader) Buffered() int {
	return r.r.Buffered()
}

func (rdr *Reader) ReadRequest() (*Request, error) {
	v, err := rdr.ReadValue()
	if err != nil {
		return nil, err
	}
	if v.Type != TypeArray || len(v.Array) == 0 {
		return nil, ErrInvalidProtocol
	}
	args := make([][]byte, len(v.Array))
	for i, elem := range v.Array {
		if elem == nil || elem.IsNil {
			args[i] = nil
		} else {
			args[i] = elem.Str
		}
	}
	req := &Request{Args: args}
	if len(args) > 0 {
		upperASCIIInPlace(args[0])
		req.Cmd = string(args[0])
	}
	return req, nil
}
