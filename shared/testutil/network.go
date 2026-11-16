package testutil

import (
	"net"
	"sync"
)

// InterruptConn cuts a real connection after independent byte budgets in each
// direction. Negative budgets disable injection. Once exhausted, that direction
// returns ErrInterrupted until the connection is replaced. Close is caller-owned.
type InterruptConn struct {
	net.Conn
	readMu, writeMu     sync.Mutex
	readLeft, writeLeft int64
}

func NewInterruptConn(conn net.Conn, readBytes, writeBytes int64) *InterruptConn {
	return &InterruptConn{Conn: conn, readLeft: readBytes, writeLeft: writeBytes}
}
func (c *InterruptConn) Read(p []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	return interruptIO(p, &c.readLeft, c.Conn.Read)
}
func (c *InterruptConn) Write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return interruptIO(p, &c.writeLeft, c.Conn.Write)
}
func interruptIO(p []byte, left *int64, op func([]byte) (int, error)) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if *left < 0 {
		return op(p)
	}
	if *left == 0 {
		return 0, ErrInterrupted
	}
	limited := int64(len(p)) > *left
	if limited {
		p = p[:int(*left)]
	}
	n, err := op(p)
	*left -= int64(n)
	if limited && err == nil {
		err = ErrInterrupted
	}
	return n, err
}
