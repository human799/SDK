package sdk

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net"
)

const (
	obfsMagic    = uint32(0xDEADBEEF)
	obfsMaxFrame = 256 * 1024 // 256KB per frame; larger payloads are split
)

// obfsConn wraps a net.Conn with a simple obfuscation framing layer.
// Frame format:
//
//	[4B magic][4B payload_len][paddingLen byte][padding bytes][payload]
//
// Payloads larger than obfsMaxFrame are automatically split into multiple frames.
type obfsConn struct {
	net.Conn
}

func newObfsConn(c net.Conn) *obfsConn {
	return &obfsConn{Conn: c}
}

// Write splits p into obfsMaxFrame-sized chunks and sends each as one frame.
func (o *obfsConn) Write(p []byte) (int, error) {
	total := 0
	for len(p) > 0 {
		chunk := p
		if len(chunk) > obfsMaxFrame {
			chunk = p[:obfsMaxFrame]
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

func (o *obfsConn) writeFrame(p []byte) (int, error) {
	paddingLen := randomByte() & 0x3F // 0–63 bytes
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

// Read unwraps one obfs frame. buf must be at least obfsMaxFrame bytes.
func (o *obfsConn) Read(buf []byte) (int, error) {
	header := make([]byte, 9)
	if _, err := io.ReadFull(o.Conn, header); err != nil {
		return 0, err
	}

	magic := binary.BigEndian.Uint32(header[0:4])
	if magic != obfsMagic {
		return 0, fmt.Errorf("obfs: bad magic 0x%08X", magic)
	}

	payloadLen := binary.BigEndian.Uint32(header[4:8])
	if payloadLen > obfsMaxFrame {
		return 0, fmt.Errorf("obfs: frame too large (%d > %d)", payloadLen, obfsMaxFrame)
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
