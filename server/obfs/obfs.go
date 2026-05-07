// Package obfs provides the server-side obfuscation framing layer,
// mirroring sdk/obfs.go on the client side.
package obfs

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net"
)

const (
	obfsMagic = uint32(0xDEADBEEF)
	// MaxFrame is the maximum payload size per obfs frame.
	// Larger payloads are automatically split by Write.
	MaxFrame = 256 * 1024
)

// Conn wraps a net.Conn with obfuscation framing (same protocol as sdk/obfs.go).
type Conn struct {
	net.Conn
}

// New wraps c with the obfs framing layer.
func New(c net.Conn) *Conn {
	return &Conn{Conn: c}
}

// Write splits p into obfsMaxFrame-sized chunks and sends each as one frame.
func (o *Conn) Write(p []byte) (int, error) {
	total := 0
	for len(p) > 0 {
		chunk := p
		if len(chunk) > MaxFrame {
			chunk = p[:MaxFrame]
		}
		n, err := o.writeFrame(chunk)
		total += n
		if err != nil {
			return total, err
		}
		p = p[n:]
	}
	return total, nil
}

func (o *Conn) writeFrame(p []byte) (int, error) {
	paddingLen := randomByte() & 0x3F
	frame := make([]byte, 4+4+1+int(paddingLen)+len(p))

	binary.BigEndian.PutUint32(frame[0:4], obfsMagic)
	binary.BigEndian.PutUint32(frame[4:8], uint32(len(p)))
	frame[8] = paddingLen
	if paddingLen > 0 {
		if _, err := rand.Read(frame[9 : 9+paddingLen]); err != nil {
			return 0, err
		}
	}
	copy(frame[9+paddingLen:], p)

	_, err := o.Conn.Write(frame)
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

// Read unwraps one obfs frame. Returns error if frame exceeds obfsMaxFrame.
func (o *Conn) Read(buf []byte) (int, error) {
	header := make([]byte, 9)
	if _, err := io.ReadFull(o.Conn, header); err != nil {
		return 0, err
	}

	magic := binary.BigEndian.Uint32(header[0:4])
	if magic != obfsMagic {
		return 0, fmt.Errorf("obfs: bad magic 0x%08X", magic)
	}

	payloadLen := binary.BigEndian.Uint32(header[4:8])
	if payloadLen > MaxFrame {
		return 0, fmt.Errorf("obfs: frame too large (%d > %d)", payloadLen, MaxFrame)
	}
	paddingLen := header[8]

	if paddingLen > 0 {
		discard := make([]byte, paddingLen)
		if _, err := io.ReadFull(o.Conn, discard); err != nil {
			return 0, err
		}
	}

	if int(payloadLen) > len(buf) {
		return 0, fmt.Errorf("obfs: buf too small (%d < %d)", len(buf), payloadLen)
	}

	return io.ReadFull(o.Conn, buf[:payloadLen])
}

func randomByte() byte {
	b := make([]byte, 1)
	rand.Read(b) //nolint:errcheck
	return b[0]
}
