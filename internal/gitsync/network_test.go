package gitsync

import (
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

// TestClosedPortIsUnreachable: nothing listening fails fast, as
// ErrUnreachable, and leaves the local state usable.
func TestClosedPortIsUnreachable(t *testing.T) {
	f := newFixture(t)
	_, priv := clientKey(t)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	url := "ssh://git@" + addr + f.remote
	setRemoteURL(t, f, url)
	auth, err := Auth(url, priv, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl")
	if err != nil {
		t.Fatal(err)
	}
	f.phone.Auth = auth
	f.capture("While away", 1)

	start := time.Now()
	err = f.phone.Refresh()
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("Refresh = %v, want ErrUnreachable", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("failing took %v", d)
	}
	if s, _ := f.phone.LoadState(); !strings.Contains(s.LastError, ErrUnreachable.Error()) {
		t.Errorf("state error = %q", s.LastError)
	}
	if err := f.phone.Publish(); !errors.Is(err, ErrUnreachable) {
		t.Errorf("Publish = %v, want ErrUnreachable", err)
	}
	if got := f.queueTitles(); len(got) != 1 {
		t.Errorf("queue = %v; offline must not lose captures", got)
	}
}

// TestStalledServerHitsTheTimeouts: a server that accepts the TCP
// connection and then says nothing must not hang the phone.
func TestStalledServerHitsTheTimeouts(t *testing.T) {
	f := newFixture(t)
	_, priv := clientKey(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	// The accept loop owns held until it exits; the cleanup closes the
	// listener to end the loop, waits for it, and only then reads held.
	var held []net.Conn
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			held = append(held, c) // accept, then never speak
		}
	}()
	defer func() {
		ln.Close()
		<-acceptDone
		for _, c := range held {
			c.Close()
		}
	}()
	oldConnect, oldOp := ConnectTimeout, OperationTimeout
	ConnectTimeout, OperationTimeout = 300*time.Millisecond, 2*time.Second
	t.Cleanup(func() { ConnectTimeout, OperationTimeout = oldConnect, oldOp })

	url := "ssh://git@" + ln.Addr().String() + f.remote
	setRemoteURL(t, f, url)
	auth, _ := Auth(url, priv, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl")
	f.phone.Auth = auth

	done := make(chan error, 1)
	go func() { done <- f.phone.Fetch() }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrUnreachable) {
			t.Errorf("Fetch = %v, want ErrUnreachable", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Fetch against a silent server didn't give up")
	}
}

func TestRefreshLocalNeedsNoNetwork(t *testing.T) {
	f := newFixture(t)
	setRemoteURL(t, f, "ssh://git@192.0.2.1/org.git") // TEST-NET: never routable
	f.phone.Auth = nil
	if _, err := capturequeueAdd(f, "Offline"); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := f.phone.RefreshLocal(); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 2*time.Second {
		t.Error("RefreshLocal touched the network")
	}
}

func TestUnreachableLeavesOtherErrorsAlone(t *testing.T) {
	if err := unreachable(ErrHostKeyMismatch); errors.Is(err, ErrUnreachable) {
		t.Error("host key mismatch classified as unreachable")
	}
	if err := unreachable(errors.New("ssh: unable to authenticate")); errors.Is(err, ErrUnreachable) {
		t.Error("auth failure classified as unreachable")
	}
	if err := unreachable(&net.DNSError{Err: "no such host", Name: "nas"}); !errors.Is(err, ErrUnreachable) {
		t.Error("DNS failure not classified as unreachable")
	}
}
