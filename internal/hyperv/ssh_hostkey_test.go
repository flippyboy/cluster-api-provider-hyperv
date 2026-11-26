package hyperv

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/pem"
	"net"
	"reflect"
	"strconv"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func mustSigner(t *testing.T, key any) ssh.Signer {
	t.Helper()
	s, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func knownLine(addr string, keys ...ssh.PublicKey) []byte {
	var out []byte
	for _, k := range keys {
		out = append(out, knownhosts.Line([]string{addr}, k)+"\n"...)
	}
	return out
}

func TestKnownHostKeyAlgorithms(t *testing.T) {
	_, edPriv, _ := ed25519.GenerateKey(rand.Reader)
	rsaPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ed := mustSigner(t, edPriv).PublicKey()
	rs := mustSigner(t, rsaPriv).PublicKey()
	addr := "10.0.10.100:22"

	cases := []struct {
		name  string
		known []byte
		want  []string
	}{
		{"ed25519 only", knownLine(addr, ed), []string{ssh.KeyAlgoED25519}},
		{"rsa expands to sha2", knownLine(addr, rs), []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA}},
		{"other host keeps defaults", knownLine("10.0.10.200:22", ed), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cb, err := hostKeyCallback(tc.known, false)
			if err != nil {
				t.Fatal(err)
			}
			if got := knownHostKeyAlgorithms(cb, addr); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// The server has ECDSA and ED25519 host keys, and known_hosts lists only
// ED25519. Without HostKeyAlgorithms the client negotiates ECDSA and fails
// with "knownhosts: key mismatch".
func TestDialSSHUsesKnownHostKeyType(t *testing.T) {
	_, edPriv, _ := ed25519.GenerateKey(rand.Reader)
	ecPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, clientPriv, _ := ed25519.GenerateKey(rand.Reader)
	edSigner := mustSigner(t, edPriv)

	srv := &ssh.ServerConfig{
		PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) { return nil, nil },
	}
	srv.AddHostKey(mustSigner(t, ecPriv))
	srv.AddHostKey(edSigner)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_, chans, reqs, err := ssh.NewServerConn(c, srv)
				if err != nil {
					c.Close()
					return
				}
				go ssh.DiscardRequests(reqs)
				for ch := range chans {
					_ = ch.Reject(ssh.Prohibited, "test")
				}
			}()
		}
	}()

	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)
	block, err := ssh.MarshalPrivateKey(clientPriv, "")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := dialSSH(context.Background(), HostConfig{
		Address:    host,
		Port:       port,
		Username:   "agent",
		PrivateKey: pem.EncodeToMemory(block),
		KnownHosts: knownLine(ln.Addr().String(), edSigner.PublicKey()),
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = sess.Close()
}
