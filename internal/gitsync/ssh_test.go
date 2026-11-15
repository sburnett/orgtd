package gitsync

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"golang.org/x/crypto/ssh"
)

// sshServer is a minimal git-over-SSH server for tests: it accepts one
// client key and runs the git-upload-pack/git-receive-pack command each
// session asks for, like a real server's shell would.
type sshServer struct {
	addr    string
	hostKey ssh.PublicKey
	logins  []string // the SSH user of each authenticated connection
}

func startSSHServer(t *testing.T, clientKey ssh.PublicKey) *sshServer {
	t.Helper()
	if _, err := exec.LookPath("git-upload-pack"); err != nil {
		if _, err := exec.LookPath("git"); err != nil {
			t.Skip("git not installed")
		}
	}
	_, hostPriv, _ := ed25519.GenerateKey(rand.Reader)
	hostSigner, err := ssh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatal(err)
	}
	srv := &sshServer{hostKey: hostSigner.PublicKey()}
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if string(key.Marshal()) != string(clientKey.Marshal()) {
				return nil, errors.New("unknown key")
			}
			srv.logins = append(srv.logins, meta.User())
			return nil, nil
		},
	}
	cfg.AddHostKey(hostSigner)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	srv.addr = ln.Addr().String()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveSSH(conn, cfg)
		}
	}()
	return srv
}

func serveSSH(conn net.Conn, cfg *ssh.ServerConfig) {
	defer conn.Close()
	_, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			nc.Reject(ssh.UnknownChannelType, "")
			continue
		}
		ch, chReqs, err := nc.Accept()
		if err != nil {
			return
		}
		go func() {
			defer ch.Close()
			for req := range chReqs {
				if req.Type != "exec" {
					req.Reply(false, nil)
					continue
				}
				n := binary.BigEndian.Uint32(req.Payload[:4])
				command := string(req.Payload[4 : 4+n])
				req.Reply(true, nil)
				cmd := exec.Command("sh", "-c", command)
				cmd.Stdout, cmd.Stderr = ch, ch.Stderr()
				stdin, _ := cmd.StdinPipe()
				go func() { io.Copy(stdin, ch); stdin.Close() }()
				status := uint32(0)
				if err := cmd.Run(); err != nil {
					status = 1
				}
				ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
				return
			}
		}()
	}
}

// overSSH points the fixture's phone at f.remote through srv, with the
// given client key and trusted host key.
func overSSH(srv *sshServer, f *fixture, user string) string {
	return "ssh://" + user + "@" + srv.addr + f.remote
}

func clientKey(t *testing.T) (ssh.PublicKey, []byte) {
	t.Helper()
	pub, priv, err := GenerateKey("")
	if err != nil {
		t.Fatal(err)
	}
	key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(pub))
	if err != nil {
		t.Fatal(err)
	}
	return key, priv
}

func TestScanCloneAndSyncOverSSH(t *testing.T) {
	f := newFixture(t) // also makes f.phone's local clone; replaced below
	pub, priv := clientKey(t)
	srv := startSSHServer(t, pub)
	url := overSSH(srv, f, "sam")

	hk, err := ScanHostKey(url, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if hk.Fingerprint != ssh.FingerprintSHA256(srv.hostKey) || hk.Host != srv.addr {
		t.Errorf("ScanHostKey = %+v, want fingerprint %s", hk, ssh.FingerprintSHA256(srv.hostKey))
	}

	auth, err := Auth(url, priv, hk.Key)
	if err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(f.phone.Dir)
	os.Remove(f.phone.StatePath)
	f.phone.Auth = auth
	if err := f.phone.Clone(url, hk); err != nil {
		t.Fatalf("Clone over SSH: %v", err)
	}
	if s, _ := f.phone.LoadState(); s.HostKey != hk {
		t.Errorf("state host key = %+v, want %+v", s.HostKey, hk)
	}
	f.capture("Over SSH", 1)
	f.sync()
	f.assertAppendOnly("Over SSH")
	for _, u := range srv.logins {
		if u != "sam" {
			t.Errorf("logged in as %q, want the URL's user sam", u)
		}
	}
}

func TestWrongHostKeyIsRefused(t *testing.T) {
	f := newFixture(t)
	pub, priv := clientKey(t)
	srv := startSSHServer(t, pub)
	url := overSSH(srv, f, "git")
	impostor, _ := clientKey(t) // any other key will do
	auth, err := Auth(url, priv, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(impostor))))
	if err != nil {
		t.Fatal(err)
	}
	_, err = git.PlainClone(t.TempDir(), false, &git.CloneOptions{URL: url, Auth: auth})
	if err == nil || !strings.Contains(err.Error(), ErrHostKeyMismatch.Error()) {
		t.Errorf("clone with the wrong host key = %v, want a host key mismatch", err)
	}
	if len(srv.logins) != 0 {
		t.Error("client authenticated to a server whose host key didn't match")
	}
}

func TestUnknownClientKeyIsRefused(t *testing.T) {
	f := newFixture(t)
	pub, _ := clientKey(t)
	_, otherPriv := clientKey(t)
	srv := startSSHServer(t, pub)
	url := overSSH(srv, f, "git")
	hk, err := ScanHostKey(url, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	auth, _ := Auth(url, otherPriv, hk.Key)
	_, err = git.PlainClone(t.TempDir(), false, &git.CloneOptions{URL: url, Auth: auth, ReferenceName: plumbing.Main})
	if err == nil {
		t.Error("clone with an unauthorized client key succeeded")
	}
}

func TestAuthNeedsATrustedHostKeyForSSH(t *testing.T) {
	_, priv := clientKey(t)
	if _, err := Auth("git@example.com:org.git", priv, ""); err == nil {
		t.Error("Auth with no trusted host key succeeded")
	}
	if a, err := Auth("/tmp/remote.git", priv, ""); a != nil || err != nil {
		t.Errorf("Auth(local path) = %v, %v; want no auth", a, err)
	}
}

func TestScanHostKeyRejectsNonSSHAndUnreachable(t *testing.T) {
	if _, err := ScanHostKey("https://example.com/org.git", time.Second); err == nil {
		t.Error("scanning an https URL succeeded")
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	if _, err := ScanHostKey("ssh://git@"+addr+"/org.git", time.Second); err == nil {
		t.Error("scanning a closed port succeeded")
	}
}

func TestParseSSHURL(t *testing.T) {
	cases := []struct {
		url  string
		want SSHRemote
		ok   bool
	}{
		{"git@github.com:me/org.git", SSHRemote{"git", "github.com", "22"}, true},
		{"sam@nas.tailnet.ts.net:git/org.git", SSHRemote{"sam", "nas.tailnet.ts.net", "22"}, true},
		{"nas:org.git", SSHRemote{"git", "nas", "22"}, true},
		{"ssh://sam@100.64.0.7:2222/home/sam/org.git", SSHRemote{"sam", "100.64.0.7", "2222"}, true},
		{"ssh://server/srv/org.git", SSHRemote{"git", "server", "22"}, true},
		{"https://github.com/me/org.git", SSHRemote{}, false},
		{"/tmp/remote.git", SSHRemote{}, false},
		{"file:///tmp/remote.git", SSHRemote{}, false},
		{"@host:path", SSHRemote{}, false},
		{"host:", SSHRemote{}, false},
	}
	for _, c := range cases {
		got, ok := ParseSSHURL(c.url)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("ParseSSHURL(%q) = %+v, %v; want %+v, %v", c.url, got, ok, c.want, c.ok)
		}
	}
}
