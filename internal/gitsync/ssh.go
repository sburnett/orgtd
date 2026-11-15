package gitsync

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	neturl "net/url"
	"strings"
	"time"

	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"golang.org/x/crypto/ssh"
)

// GenerateKey makes a new ed25519 key pair: the public half in
// authorized_keys form ("ssh-ed25519 AAAA... comment"), for adding to the
// server, and the private half as an OpenSSH PEM block, for the Keychain.
func GenerateKey(comment string) (publicKey string, privatePEM []byte, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", nil, err
	}
	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return "", nil, err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", nil, err
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))
	if comment != "" {
		line += " " + comment
	}
	return line, pem.EncodeToMemory(block), nil
}

// PublicKey derives the authorized_keys line for a private key from
// GenerateKey (without a comment).
func PublicKey(privatePEM []byte) (string, error) {
	signer, err := ssh.ParsePrivateKey(privatePEM)
	if err != nil {
		return "", fmt.Errorf("gitsync: parsing private key: %w", err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))), nil
}

// SSHRemote is the parts of an SSH remote URL that matter for connecting.
type SSHRemote struct {
	User string // "git" if the URL names none
	Host string
	Port string // "22" if the URL names none
}

// Addr is host:port, for dialing.
func (r SSHRemote) Addr() string { return net.JoinHostPort(r.Host, r.Port) }

// ParseSSHURL splits an SSH remote, either "ssh://[user@]host[:port]/path"
// or the scp-like "[user@]host:path". ok is false for anything else (a
// local path, file://, https://).
func ParseSSHURL(url string) (r SSHRemote, ok bool) {
	r = SSHRemote{User: "git", Port: "22"}
	if strings.HasPrefix(url, "ssh://") {
		u, err := neturl.Parse(url)
		if err != nil || u.Hostname() == "" {
			return r, false
		}
		if u.User != nil && u.User.Username() != "" {
			r.User = u.User.Username()
		}
		r.Host = u.Hostname()
		if u.Port() != "" {
			r.Port = u.Port()
		}
		return r, true
	}
	if strings.Contains(url, "://") || strings.HasPrefix(url, "/") || strings.HasPrefix(url, ".") {
		return r, false
	}
	hostPart, path, found := strings.Cut(url, ":")
	if !found || path == "" || hostPart == "" || strings.Contains(hostPart, "/") {
		return r, false
	}
	if user, host, hasUser := strings.Cut(hostPart, "@"); hasUser {
		if user == "" || host == "" {
			return r, false
		}
		r.User, r.Host = user, host
	} else {
		r.Host = hostPart
	}
	return r, true
}

// IsSSHURL reports whether url is an SSH remote (see ParseSSHURL).
func IsSSHURL(url string) bool {
	_, ok := ParseSSHURL(url)
	return ok
}

// HostKey is a server's SSH host key, as found by ScanHostKey and
// confirmed by the user.
type HostKey struct {
	Host        string // host:port it was found at
	Key         string // authorized_keys form, e.g. "ssh-ed25519 AAAA..."
	Fingerprint string // "SHA256:...", as `ssh-keygen -lf` prints it
}

// preferredHostKeyAlgorithms is the order host key types are asked for
// when scanning: ed25519 first, so a server offering it is pinned by it.
var preferredHostKeyAlgorithms = []string{
	ssh.KeyAlgoED25519,
	ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521,
	ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256,
}

var errGotHostKey = errors.New("gitsync: host key captured")

// ScanHostKey connects to url's server just long enough to see its host
// key, without authenticating, so the user can compare its fingerprint
// against the server's own before trusting it (trust on first use, but
// checked). timeout bounds the whole attempt.
func ScanHostKey(url string, timeout time.Duration) (HostKey, error) {
	remote, ok := ParseSSHURL(url)
	if !ok {
		return HostKey{}, fmt.Errorf("gitsync: %q isn't an SSH URL (use user@host:path or ssh://user@host/path)", url)
	}
	var got ssh.PublicKey
	cfg := &ssh.ClientConfig{
		User:              remote.User,
		HostKeyAlgorithms: preferredHostKeyAlgorithms,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			got = key
			return errGotHostKey
		},
		Timeout: timeout,
	}
	conn, err := net.DialTimeout("tcp", remote.Addr(), timeout)
	if err != nil {
		return HostKey{}, fmt.Errorf("gitsync: connecting to %s: %w", remote.Addr(), err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(timeout))
	_, _, _, err = ssh.NewClientConn(conn, remote.Addr(), cfg)
	if got == nil {
		return HostKey{}, fmt.Errorf("gitsync: no SSH host key from %s: %w", remote.Addr(), err)
	}
	return HostKey{
		Host:        remote.Addr(),
		Key:         strings.TrimSpace(string(ssh.MarshalAuthorizedKey(got))),
		Fingerprint: ssh.FingerprintSHA256(got),
	}, nil
}

// Auth returns the transport auth for url: SSH as the URL's user with
// privatePEM, accepting only the server whose host key is hostKey
// (authorized_keys form, from ScanHostKey). It returns nil (no auth) for
// a local path or file:// URL, which tests use.
func Auth(url string, privatePEM []byte, hostKey string) (gitssh.AuthMethod, error) {
	remote, ok := ParseSSHURL(url)
	if !ok {
		return nil, nil
	}
	if hostKey == "" {
		return nil, errors.New("gitsync: no trusted host key for this server; set the repository up again")
	}
	pinned, _, _, _, err := ssh.ParseAuthorizedKey([]byte(hostKey))
	if err != nil {
		return nil, fmt.Errorf("gitsync: parsing trusted host key: %w", err)
	}
	auth, err := gitssh.NewPublicKeys(remote.User, privatePEM, "")
	if err != nil {
		return nil, fmt.Errorf("gitsync: loading SSH key: %w", err)
	}
	auth.HostKeyCallback = pinnedHostKey(pinned)
	auth.HostKeyAlgorithms = hostKeyAlgorithms(pinned.Type())
	return timeoutAuth{auth}, nil
}

// hostKeyAlgorithms asks the server for the pinned key's own type, so it
// presents that key rather than another one it also has.
func hostKeyAlgorithms(keyType string) []string {
	if keyType == ssh.KeyAlgoRSA {
		return []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA}
	}
	return []string{keyType}
}

// ErrHostKeyMismatch means the server presented a different host key
// than the one trusted at setup — a reinstalled server, or someone in
// the middle.
var ErrHostKeyMismatch = errors.New("the server's SSH host key doesn't match the one trusted at setup")

func pinnedHostKey(pinned ssh.PublicKey) ssh.HostKeyCallback {
	return func(hostname string, _ net.Addr, key ssh.PublicKey) error {
		if key.Type() == pinned.Type() && string(key.Marshal()) == string(pinned.Marshal()) {
			return nil
		}
		return fmt.Errorf("gitsync: %s: %w (got %s, trusted %s)", hostname, ErrHostKeyMismatch, ssh.FingerprintSHA256(key), ssh.FingerprintSHA256(pinned))
	}
}
