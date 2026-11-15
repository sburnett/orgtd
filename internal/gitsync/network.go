package gitsync

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sync"
	"syscall"
	"time"

	"github.com/go-git/go-git/v5/plumbing/transport"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"golang.org/x/crypto/ssh"
	"golang.org/x/net/proxy"
)

// Network limits. The phone is often somewhere its server can't be
// reached (a server on the home network, with the phone out of the
// house), where a TCP connect is simply never answered. Without a limit
// each attempt would wait out the OS's connect timeout (over a minute),
// and every other operation waits behind it.
var (
	// ConnectTimeout bounds the TCP connect and SSH handshake.
	ConnectTimeout = 8 * time.Second
	// OperationTimeout bounds one whole fetch or push.
	OperationTimeout = 2 * time.Minute
	// IdleTimeout fails a connection the server has gone quiet on, once
	// it has said anything at all (before that, ConnectTimeout applies).
	IdleTimeout = 45 * time.Second
	// CloneTimeout bounds a clone, which moves the whole history.
	CloneTimeout = 10 * time.Minute
)

// ErrUnreachable means the server couldn't be reached at all: no route,
// no answer, refused, unknown host, or a timeout. Nothing was changed on
// either side; trying again later is the whole remedy.
var ErrUnreachable = errors.New("can't reach the git server")

// unreachable wraps err in ErrUnreachable when it is a network failure
// (as opposed to, say, an authentication or host key problem), and
// returns it unchanged otherwise.
func unreachable(err error) error {
	if err == nil || errors.Is(err, ErrUnreachable) || errors.Is(err, ErrHostKeyMismatch) {
		return err
	}
	var netErr net.Error
	var dnsErr *net.DNSError
	var opErr *net.OpError
	switch {
	case errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, syscall.ECONNREFUSED), errors.Is(err, syscall.EHOSTUNREACH),
		errors.Is(err, syscall.ENETUNREACH), errors.Is(err, syscall.ETIMEDOUT),
		errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.ENETDOWN),
		errors.As(err, &dnsErr), errors.As(err, &opErr),
		errors.As(err, &netErr) && netErr.Timeout():
		return fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	return err
}

// timeoutAuth is go-git SSH auth whose connections give up after
// ConnectTimeout: go-git dials with the client config's Timeout, which
// its own PublicKeys leaves at zero (wait forever).
type timeoutAuth struct {
	*gitssh.PublicKeys
}

func (a timeoutAuth) ClientConfig() (*ssh.ClientConfig, error) {
	cfg, err := a.PublicKeys.ClientConfig()
	if err != nil {
		return nil, err
	}
	cfg.Timeout = ConnectTimeout
	return cfg, nil
}

func withTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// go-git bounds a fetch's or push's data transfer by its context, but not
// the SSH handshake before it, and it takes no custom dialer — only a
// proxy URL, which it resolves through golang.org/x/net/proxy. So SSH
// connections go through a "proxy" scheme of our own, deadlineProxy,
// that dials directly and puts deadlines on the connection: a server that
// accepts the TCP connection and then says nothing gives up after
// ConnectTimeout; one that goes quiet later, after IdleTimeout.
const deadlineScheme = "orgtd-direct"

// sshProxy is the ProxyOptions every fetch, push and clone uses.
var sshProxy = transport.ProxyOptions{URL: deadlineScheme + "://direct"}

var registerProxy sync.Once

func init() {
	registerProxy.Do(func() {
		proxy.RegisterDialerType(deadlineScheme, func(*url.URL, proxy.Dialer) (proxy.Dialer, error) {
			return deadlineProxy{}, nil
		})
	})
}

type deadlineProxy struct{}

func (deadlineProxy) Dial(network, addr string) (net.Conn, error) {
	return deadlineProxy{}.DialContext(context.Background(), network, addr)
}

func (deadlineProxy) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	d := net.Dialer{Timeout: ConnectTimeout}
	c, err := d.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	dc := &deadlineConn{Conn: c}
	dc.SetDeadline(time.Now().Add(ConnectTimeout))
	return dc, nil
}

// deadlineConn pushes its deadline out by IdleTimeout whenever data moves
// either way, so it only fails when the connection goes quiet. Writes
// count only once the server has sent something: until then the client's
// own opening lines must not stretch ConnectTimeout.
type deadlineConn struct {
	net.Conn
	mu    sync.Mutex
	heard bool
}

func (c *deadlineConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.mu.Lock()
		c.heard = true
		c.mu.Unlock()
		c.Conn.SetDeadline(time.Now().Add(IdleTimeout))
	}
	return n, err
}

func (c *deadlineConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.mu.Lock()
	heard := c.heard
	c.mu.Unlock()
	if n > 0 && heard {
		c.Conn.SetDeadline(time.Now().Add(IdleTimeout))
	}
	return n, err
}
